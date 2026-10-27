"""
hop_top_kit.routellm_adapter — RouteLLM provider for the ``routellm://`` scheme.

Parses URIs of the form ``routellm://router:threshold`` (e.g.
``routellm://mf:0.7``), creates a RouteLLM Controller, and delegates
completions through its routing logic.

Optional Eva contract validation runs post-completion when configured.

:attr:`~hop_top_kit.llm.Request.tool_choice` goes to the Controller in
the OpenAI shape (:func:`hop_top_kit.llm_openai.tool_choice`); a choice
without tools, or an invalid one, is refused before routing.
"""

from __future__ import annotations

import logging
from collections.abc import Iterator
from typing import Any

from hop_top_kit import llm, llm_openai
from hop_top_kit.llm_toolnames import (
    OPENAI_TOOL_NAMES,
    ToolNameMode,
    ToolNames,
    tool_name_mode_of,
)

_log = logging.getLogger(__name__)

try:
    from routellm.controller import Controller
except ImportError:
    Controller = None  # type: ignore[assignment,misc]

try:
    import eva  # type: ignore[import-untyped]
except ImportError:
    eva = None  # type: ignore[assignment]


# ------------------------------------------------------------------
# URI helpers
# ------------------------------------------------------------------


def parse_router_threshold(model: str) -> tuple[str, float]:
    """Extract ``(router, threshold)`` from a model string like ``mf:0.7``.

    Raises :class:`llm.ThresholdInvalidError` when the threshold is not
    a float in [0, 1].
    """
    if ":" not in model:
        raise llm.LLMError(f"routellm model must be 'router:threshold', got {model!r}")
    router, raw_thresh = model.rsplit(":", 1)
    if not router.strip():
        raise llm.LLMError("router name must not be empty")
    try:
        threshold = float(raw_thresh)
    except ValueError:
        raise llm.LLMError(f"threshold {raw_thresh!r} is not a valid float") from None
    if not 0.0 <= threshold <= 1.0:
        raise llm.ThresholdInvalidError(threshold)
    return router.strip(), threshold


# ------------------------------------------------------------------
# Message mapping (OpenAI chat wire shape)
# ------------------------------------------------------------------


def map_messages(
    messages: list[llm.Message], tool_names: ToolNames | None = None
) -> list[dict[str, Any]]:
    """Map :class:`llm.Message` values to OpenAI chat messages.

    RouteLLM speaks the OpenAI chat shape; this is
    :func:`hop_top_kit.llm_openai.map_messages` with errors prefixed
    ``routellm``. With *tool_names*, a replayed call's name goes out as
    its wire name (:meth:`ToolNames.wire`); without, names go out as
    given.
    """
    return llm_openai.map_messages(messages, tool_names, provider="routellm")


def _plain(obj: Any) -> Any:
    """A Controller result (litellm objects, dicts) as plain JSON data."""
    if isinstance(obj, dict):
        return {k: _plain(v) for k, v in obj.items()}
    if isinstance(obj, (list, tuple)):
        return [_plain(v) for v in obj]
    if obj is None or isinstance(obj, (str, int, float, bool)):
        return obj
    dump = getattr(obj, "model_dump", None)
    if callable(dump):
        return _plain(dump())
    if hasattr(obj, "__dict__"):
        return {k: _plain(v) for k, v in vars(obj).items() if not k.startswith("_")}
    return None


# ------------------------------------------------------------------
# Adapter
# ------------------------------------------------------------------


class RouteLLMAdapter:
    """Completer, Streamer, ToolCaller and ToolStreamer backed by
    RouteLLM's Controller.

    Tool calling goes through the OpenAI wire mapping
    (:mod:`hop_top_kit.llm_openai`): tools under
    :data:`~hop_top_kit.llm_toolnames.OPENAI_TOOL_NAMES` wire names,
    passed to the Controller as ``tools``, and the routed model's calls
    mapped back to the caller's names.
    """

    def __init__(self, cfg: llm.ResolvedConfig) -> None:
        if Controller is None:
            raise ImportError("routellm is not installed. Install it with: pip install routellm")

        extras: dict[str, Any] = cfg.provider.extras
        tool_name_mode = tool_name_mode_of(cfg.provider.params)
        self._router_name, self._threshold = parse_router_threshold(
            cfg.uri.model,
        )

        strong = extras.get("strong_model", "gpt-4-1106-preview")
        weak = extras.get("weak_model", "mixtral-8x7b-instruct-v0.1")
        routers = extras.get("routers", [self._router_name])
        router_config = extras.get("router_config")

        self._controller = Controller(
            routers=routers,
            strong_model=strong,
            weak_model=weak,
            config=router_config,
            api_base=cfg.provider.base_url or None,
            api_key=cfg.provider.api_key or None,
        )

        self._eva_cfg = extras.get("eva")
        self._tool_name_mode: ToolNameMode = tool_name_mode

    # -- Completer protocol ------------------------------------

    def complete(self, req: llm.Request) -> llm.Response:
        llm_openai.check_tool_choice("routellm", req, None)
        messages = map_messages(req.messages, self._history_names())

        kwargs: dict[str, Any] = {}
        if req.temperature is not None:
            kwargs["temperature"] = req.temperature
        if req.max_tokens:
            kwargs["max_tokens"] = req.max_tokens

        result = self._controller.completion(
            router=self._router_name,
            threshold=self._threshold,
            messages=messages,
            **kwargs,
        )

        content = result.choices[0].message.content or ""
        usage_data = getattr(result, "usage", None)
        usage = None
        if usage_data:
            usage = llm.Usage(
                prompt_tokens=getattr(usage_data, "prompt_tokens", 0),
                completion_tokens=getattr(usage_data, "completion_tokens", 0),
                total_tokens=getattr(usage_data, "total_tokens", 0),
            )

        resp = llm.Response(
            content=content,
            role="assistant",
            usage=usage,
            finish_reason=getattr(result.choices[0], "finish_reason", "") or "",
        )

        self._validate_eva(resp)
        return resp

    # -- Streamer protocol -------------------------------------

    def stream(self, req: llm.Request) -> Iterator[llm.Token]:
        llm_openai.check_tool_choice("routellm", req, None)
        messages = map_messages(req.messages, self._history_names())

        kwargs: dict[str, Any] = {"stream": True}
        if req.temperature is not None:
            kwargs["temperature"] = req.temperature
        if req.max_tokens:
            kwargs["max_tokens"] = req.max_tokens

        result = self._controller.completion(
            router=self._router_name,
            threshold=self._threshold,
            messages=messages,
            **kwargs,
        )

        for chunk in result:
            delta = chunk.choices[0].delta
            content = getattr(delta, "content", "") or ""
            done = chunk.choices[0].finish_reason is not None
            yield llm.Token(content=content, done=done)

    def _history_names(self, tools: list[llm.ToolDef] | None = None) -> ToolNames:
        """The request's name table: RouteLLM speaks the OpenAI chat shape,
        so tool definitions and replayed calls follow the openai rule."""
        return ToolNames.build(OPENAI_TOOL_NAMES, self._tool_name_mode, tools or [])

    def _tool_kwargs(
        self, req: llm.Request, tools: list[llm.ToolDef], names: ToolNames
    ) -> dict[str, Any]:
        llm_openai.check_tool_choice("routellm", req, tools)
        kwargs: dict[str, Any] = {}
        if req.tool_choice is not None:
            kwargs["tool_choice"] = llm_openai.tool_choice(req.tool_choice, names)
        if req.temperature is not None:
            kwargs["temperature"] = req.temperature
        if req.max_tokens:
            kwargs["max_tokens"] = req.max_tokens
        if req.stop_sequences:
            kwargs["stop"] = list(req.stop_sequences)
        return kwargs

    # -- ToolCaller protocol -----------------------------------

    def call_with_tools(self, req: llm.Request, tools: list[llm.ToolDef]) -> llm.ToolResponse:
        names = self._history_names(tools)
        messages = map_messages(req.messages, names)
        result = self._controller.completion(
            router=self._router_name,
            threshold=self._threshold,
            messages=messages,
            tools=llm_openai.map_tools(tools, names),
            **self._tool_kwargs(req, tools, names),
        )
        c = llm_openai.parse_completion(_plain(result), names)
        return llm.ToolResponse(
            content=c.content,
            tool_calls=c.tool_calls,
            finish_reason=c.finish_reason,
            usage=c.usage,
        )

    # -- ToolStreamer protocol ---------------------------------

    def stream_with_tools(self, req: llm.Request, tools: list[llm.ToolDef]) -> RouteLLMToolStream:
        names = self._history_names(tools)
        messages = map_messages(req.messages, names)
        result = self._controller.completion(
            router=self._router_name,
            threshold=self._threshold,
            messages=messages,
            tools=llm_openai.map_tools(tools, names),
            stream=True,
            **self._tool_kwargs(req, tools, names),
        )
        return RouteLLMToolStream(iter(result), names)

    # -- Eva validation ----------------------------------------

    def _validate_eva(self, resp: llm.Response) -> None:
        if not self._eva_cfg or eva is None:
            return
        contract_name = self._eva_cfg.get("contract", "")
        if not contract_name:
            return
        enforce = self._eva_cfg.get("enforce", False)
        try:
            violations = eva.validate(contract_name, resp.content)
        except Exception:
            _log.warning(
                "eva validation error for contract %r",
                contract_name,
                exc_info=True,
            )
            if enforce:
                raise
            return
        if violations:
            raise llm.ContractViolationError(contract_name, violations)

    # -- Provider protocol -------------------------------------

    def close(self) -> None:
        pass


class RouteLLMToolStream:
    """A streamed tool-calling turn from the Controller: content tokens,
    then one ``done`` token; :meth:`outcome` is the folded turn after it.
    The chunks ending before any carried a ``finish_reason`` raises
    :class:`~hop_top_kit.llm.StreamTruncatedError`."""

    def __init__(self, chunks: Iterator[Any], names: ToolNames) -> None:
        self._chunks = chunks
        self._names = names
        self._acc = llm_openai.ChunkAccumulator()
        self._finished = False
        self._outcome: llm.StreamOutcome | None = None

    def __iter__(self) -> Iterator[llm.Token]:
        return self

    def outcome(self) -> llm.StreamOutcome | None:
        return self._outcome

    def close(self) -> None:
        self._finished = True
        closer = getattr(self._chunks, "close", None)
        if callable(closer):
            closer()

    def __next__(self) -> llm.Token:
        while not self._finished:
            try:
                chunk = next(self._chunks)
            except StopIteration:
                self._finished = True
                if not self._acc.finish_reason:
                    raise llm.StreamTruncatedError("routellm") from None
                self._outcome = self._acc.outcome(self._names)
                return llm.Token(done=True)
            text = self._acc.apply(_plain(chunk))
            if text:
                return llm.Token(content=text)
        raise StopIteration


# ------------------------------------------------------------------
# Factory + registration
# ------------------------------------------------------------------


def _factory(cfg: llm.ResolvedConfig) -> RouteLLMAdapter:
    return RouteLLMAdapter(cfg)


def _register() -> None:
    """Register the ``routellm`` scheme if not already present."""
    import contextlib

    with contextlib.suppress(llm.LLMError):
        llm.register("routellm", _factory)


_register()
