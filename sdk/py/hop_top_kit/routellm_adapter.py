"""
hop_top_kit.routellm_adapter — RouteLLM provider for the ``routellm://`` scheme.

The model field takes either shape a RouteLLM server advertises on
``/v1/models`` (:func:`parse_model_field`, as Go and TS):

- ``router_name:threshold`` — ``routellm://mf:0.7``. The client picks
  the router and the threshold (in [0, 1]).
- tier name — ``routellm://private``. The server resolves router and
  threshold from its own tier config.

Two modes, chosen by whether a RouteLLM server base URL is configured,
first non-empty wins (Go's order):

1. the provider's ``base_url`` (URI ``base_url`` param or host form,
   ``LLM_BASE_URL``, llm.yaml ``base_url``);
2. ``ROUTELLM_BASE_URL``, then extras ``routellm.base_url``
   (:func:`hop_top_kit.llm.parse_router_config`; a hermetic config reads
   no ``ROUTELLM_*`` variable).

HTTP mode (a base URL is configured): completions go through
:class:`hop_top_kit.llm_openai.OpenAIAdapter` pointed at the server's
OpenAI-compatible ``/v1`` API, so streaming, stream events, tool calling,
tool names (openai rule, ``?tool_names=``), ``max_body_bytes`` /
``max_event_bytes``, retries and the credential guard are the openai
adapter's. A router pair goes out as the server's
``router-<name>-<threshold>`` model; a tier goes out verbatim.

In-process mode (no base URL): a RouteLLM ``Controller`` routes in this
process (``pip install routellm``). Only ``router_name:threshold``
works: a tier needs the server's tier config, so it raises
:class:`~hop_top_kit.llm.LLMError`. Go's default server URL
(``http://localhost:6060``) does not select HTTP mode here. Extras
``api_base`` sets the Controller's upstream model API base.

Optional Eva contract validation (extras ``eva``) runs after
:meth:`RouteLLMAdapter.complete` in both modes.

:attr:`~hop_top_kit.llm.Request.tool_choice` goes out in the OpenAI
shape (:func:`hop_top_kit.llm_openai.tool_choice`); a choice without
tools, or an invalid one, is refused before routing.
"""

from __future__ import annotations

import logging
import math
import os
import re
from collections.abc import Iterator
from dataclasses import dataclass
from decimal import Decimal
from typing import Any

from hop_top_kit import llm, llm_openai
from hop_top_kit.llm_toolnames import (
    OPENAI_TOOL_NAMES,
    TOOL_NAMES_PARAM,
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
# Model field
# ------------------------------------------------------------------

_MODEL_REQUIRED = "model field is required (router_name:threshold or tier name)"

_DECIMAL_FLOAT = re.compile(r"[+-]?(?:[0-9]+\.?[0-9]*|\.[0-9]+)(?:[eE][+-]?[0-9]+)?")
_HEX_FLOAT = re.compile(r"[+-]?0[xX](?:[0-9a-fA-F]+\.?[0-9a-fA-F]*|\.[0-9a-fA-F]+)[pP][+-]?[0-9]+")


@dataclass(frozen=True)
class ParsedModel:
    """The model field classified. ``is_tier`` selects ``tier``;
    otherwise ``router_name`` and ``threshold`` hold the client-selected
    pair. The unused shape stays at ``""`` / ``0.0``."""

    is_tier: bool
    tier: str = ""
    router_name: str = ""
    threshold: float = 0.0


def _underscore_ok(text: str) -> bool:
    """Go's underscore rule for number literals: an underscore sits
    between digits, or between a base prefix and a digit."""
    s = text[1:] if text[:1] in "+-" else text
    saw = "^"  # ^ start, 0 digit or prefix, _ underscore, ! other
    i = 0
    hexa = False
    if len(s) >= 2 and s[0] == "0" and s[1] in "bBoOxX":
        i, saw, hexa = 2, "0", s[1] in "xX"
    for c in s[i:]:
        if c in "0123456789" or (hexa and c in "abcdefABCDEF"):
            saw = "0"
        elif c == "_":
            if saw != "0":
                return False
            saw = "_"
        else:
            if saw == "_":
                return False
            saw = "!"
    return saw != "_"


def _parse_finite_float(text: str) -> float | None:
    """A finite float in the syntax Go's ``strconv.ParseFloat`` accepts
    (decimal or hex mantissa with a binary exponent, underscores between
    digits), else ``None``: no NaN, no infinity, no overflow, no
    surrounding whitespace."""
    if "_" in text:
        if not _underscore_ok(text):
            return None
        text = text.replace("_", "")
    try:
        if _DECIMAL_FLOAT.fullmatch(text):
            v = float(text)
        elif _HEX_FLOAT.fullmatch(text):
            v = float.fromhex(text)
        else:
            return None
    except (OverflowError, ValueError):
        return None
    return v if math.isfinite(v) else None


def parse_model_field(model: str) -> ParsedModel:
    """Classify the model field as a ``router_name:threshold`` pair or a
    tier name, exactly as Go ``parseModelField``.

    The text after the *last* colon decides: a finite float in Go
    ``ParseFloat`` syntax makes a router pair (so a router name may
    contain colons); anything else is a tier, verbatim (so a tier name
    may contain colons, and ``mf:abc`` is a tier). A leading or trailing
    colon leaves a tier. The threshold range is not checked here; an
    unknown tier fails at the server.

    Raises :class:`~hop_top_kit.llm.LLMError` on an empty model.
    """
    if not model:
        raise llm.LLMError(_MODEL_REQUIRED)
    idx = model.rfind(":")
    if 0 < idx < len(model) - 1:
        threshold = _parse_finite_float(model[idx + 1 :])
        if threshold is not None:
            return ParsedModel(is_tier=False, router_name=model[:idx], threshold=threshold)
    return ParsedModel(is_tier=True, tier=model)


def format_threshold(v: float) -> str:
    """*v* as Go ``strconv.FormatFloat(v, 'f', -1, 64)``: the shortest
    round-tripping digits, never an exponent, ``-0`` kept."""
    return format(Decimal(repr(v)).normalize(), "f")


def server_model(parsed: ParsedModel) -> str:
    """The model a RouteLLM server expects: a tier verbatim, a router
    pair as ``router-<name>-<threshold>``."""
    if parsed.is_tier:
        return parsed.tier
    return f"router-{parsed.router_name}-{format_threshold(parsed.threshold)}"


def parse_router_threshold(model: str) -> tuple[str, float]:
    """Extract ``(router, threshold)`` from a model string like ``mf:0.7``,
    classified by :func:`parse_model_field`.

    Raises :class:`~hop_top_kit.llm.LLMError` when *model* is empty or a
    tier name, and :class:`~hop_top_kit.llm.ThresholdInvalidError` when
    the threshold is outside [0, 1].
    """
    parsed = parse_model_field(model)
    if parsed.is_tier:
        raise llm.LLMError(_tier_needs_server(model))
    if not 0.0 <= parsed.threshold <= 1.0:
        raise llm.ThresholdInvalidError(parsed.threshold)
    return parsed.router_name, parsed.threshold


def _tier_needs_server(model: str) -> str:
    return (
        f"routellm model {llm._go_quote(model)} is a tier name, not router_name:threshold; "
        "a tier resolves on a RouteLLM server: set its base URL (URI base_url param or "
        "host form, ROUTELLM_BASE_URL, or extras routellm.base_url)"
    )


class _ThresholdRangeError(llm.ThresholdInvalidError):
    """A router pair's threshold outside [0, 1], with Go ``New``'s text."""

    def __init__(self, threshold: float) -> None:
        self.threshold = threshold
        llm.LLMError.__init__(self, f"routellm: threshold {threshold:.4f} out of range [0, 1]")


def _router_base_url(extras: dict[str, Any], hermetic: bool) -> str:
    """The RouteLLM server base URL the router config names, or ``""``:
    ``ROUTELLM_BASE_URL`` (unless *hermetic*), then extras
    ``routellm.base_url``. Go's default URL does not count."""
    if not hermetic and (v := os.environ.get("ROUTELLM_BASE_URL", "")):
        return v
    raw = extras.get("routellm")
    if isinstance(raw, dict):
        v = raw.get("base_url")
        if isinstance(v, str):
            return v
    return ""


def server_base_url(cfg: llm.ResolvedConfig) -> str:
    """The RouteLLM server base URL for *cfg*, ``""`` for in-process
    mode: the provider's ``base_url``, then ``ROUTELLM_BASE_URL``
    (unless hermetic), then extras ``routellm.base_url``."""
    return cfg.provider.base_url or _router_base_url(cfg.provider.extras or {}, cfg.hermetic)


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
    """Completer, Streamer, ToolCaller and ToolStreamer for RouteLLM.

    HTTP mode (:attr:`mode` ``"http"``) delegates to an inner
    :class:`~hop_top_kit.llm_openai.OpenAIAdapter` pointed at the
    RouteLLM server (:attr:`base_url`). In-process mode
    (``"in_process"``) routes through RouteLLM's Controller, with tool
    calling over the OpenAI wire mapping (:mod:`hop_top_kit.llm_openai`):
    tools under :data:`~hop_top_kit.llm_toolnames.OPENAI_TOOL_NAMES` wire
    names, passed to the Controller as ``tools``, and the routed model's
    calls mapped back to the caller's names.

    :attr:`router_name` and :attr:`threshold` describe a client-selected
    router pair; for a tier they stay ``""`` / ``0.0`` and :attr:`tier`
    holds the name. Use :meth:`is_tier`, not ``threshold == 0``.
    """

    def __init__(self, cfg: llm.ResolvedConfig) -> None:
        extras: dict[str, Any] = cfg.provider.extras or {}
        # Validates the extras shape (a non-map routellm entry fails), as Go.
        llm.parse_router_config(extras, hermetic=cfg.hermetic)
        tool_name_mode = tool_name_mode_of(cfg.provider.params)

        model = cfg.provider.model or cfg.uri.model
        try:
            parsed = parse_model_field(model)
        except llm.LLMError as e:
            raise llm.LLMError(f"routellm: {e}") from None
        if not parsed.is_tier and not 0.0 <= parsed.threshold <= 1.0:
            raise _ThresholdRangeError(parsed.threshold)

        self.router_name = parsed.router_name
        self.threshold = parsed.threshold
        self.tier = parsed.tier
        self.base_url = server_base_url(cfg)
        self.mode = "http" if self.base_url else "in_process"
        self._eva_cfg = extras.get("eva")
        self._tool_name_mode: ToolNameMode = tool_name_mode
        self._inner: llm_openai.OpenAIAdapter | None = None
        self._controller: Any = None

        if self.base_url:
            # The inner adapter takes the params it owns.
            params = {
                k: cfg.provider.params[k]
                for k in (TOOL_NAMES_PARAM, "max_body_bytes", "max_event_bytes")
                if k in cfg.provider.params
            }
            wire = server_model(parsed)
            self._inner = llm_openai.OpenAIAdapter(
                llm.ResolvedConfig(
                    uri=llm.URI(scheme="routellm", model=wire),
                    provider=llm.ProviderConfig(
                        api_key=cfg.provider.api_key,
                        base_url=llm_openai._api_root_v1(self.base_url),
                        model=wire,
                        params=params,
                    ),
                    hermetic=cfg.hermetic,
                )
            )
            return

        if parsed.is_tier:
            raise llm.LLMError(_tier_needs_server(model))
        if Controller is None:
            raise ImportError("routellm is not installed. Install it with: pip install routellm")

        strong = extras.get("strong_model", "gpt-4-1106-preview")
        weak = extras.get("weak_model", "mixtral-8x7b-instruct-v0.1")
        routers = extras.get("routers", [self.router_name])
        router_config = extras.get("router_config")

        self._controller = Controller(
            routers=routers,
            strong_model=strong,
            weak_model=weak,
            config=router_config,
            api_base=extras.get("api_base") or None,
            api_key=cfg.provider.api_key or None,
        )

    def is_tier(self) -> bool:
        """Whether the model is a server-resolved tier rather than a
        client-selected ``router_name:threshold`` pair."""
        return bool(self.tier)

    # -- Completer protocol ------------------------------------

    def complete(self, req: llm.Request) -> llm.Response:
        if self._inner is not None:
            resp = self._inner.complete(req)
            self._validate_eva(resp)
            return resp
        llm_openai.check_tool_choice("routellm", req, None)
        messages = map_messages(req.messages, self._history_names())

        kwargs: dict[str, Any] = {}
        if req.temperature is not None:
            kwargs["temperature"] = req.temperature
        if req.max_tokens:
            kwargs["max_tokens"] = req.max_tokens

        result = self._controller.completion(
            router=self.router_name,
            threshold=self.threshold,
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
        if self._inner is not None:
            return self._inner.stream(req)
        return self._stream_in_process(req)

    def _stream_in_process(self, req: llm.Request) -> Iterator[llm.Token]:
        llm_openai.check_tool_choice("routellm", req, None)
        messages = map_messages(req.messages, self._history_names())

        kwargs: dict[str, Any] = {"stream": True}
        if req.temperature is not None:
            kwargs["temperature"] = req.temperature
        if req.max_tokens:
            kwargs["max_tokens"] = req.max_tokens

        result = self._controller.completion(
            router=self.router_name,
            threshold=self.threshold,
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
        if self._inner is not None:
            return self._inner.call_with_tools(req, tools)
        names = self._history_names(tools)
        messages = map_messages(req.messages, names)
        result = self._controller.completion(
            router=self.router_name,
            threshold=self.threshold,
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

    def stream_with_tools(
        self, req: llm.Request, tools: list[llm.ToolDef]
    ) -> RouteLLMToolStream | llm_openai.OpenAIStream:
        if self._inner is not None:
            return self._inner.stream_with_tools(req, tools)
        names = self._history_names(tools)
        messages = map_messages(req.messages, names)
        result = self._controller.completion(
            router=self.router_name,
            threshold=self.threshold,
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
        if self._inner is not None:
            self._inner.close()


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
