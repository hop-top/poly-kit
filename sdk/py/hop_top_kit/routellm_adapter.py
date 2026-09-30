"""
hop_top_kit.routellm_adapter — RouteLLM provider for the ``routellm://`` scheme.

Parses URIs of the form ``routellm://router:threshold`` (e.g.
``routellm://mf:0.7``), creates a RouteLLM Controller, and delegates
completions through its routing logic.

Optional Eva contract validation runs post-completion when configured.
"""

from __future__ import annotations

import base64
import json
import logging
from collections.abc import Iterator
from typing import Any

from hop_top_kit import llm

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


def map_messages(messages: list[llm.Message]) -> list[dict[str, Any]]:
    """Map :class:`llm.Message` values to OpenAI chat messages.

    RouteLLM speaks the OpenAI chat shape. An assistant turn carrying
    ``tool_calls`` becomes a message with ``tool_calls`` (arguments as a
    JSON string); a role ``"tool"`` result becomes a ``tool`` message with
    ``tool_call_id``; ``parts`` become a user content array. Messages
    without those fields map to ``{"role", "content"}`` as before.

    Raises :class:`llm.LLMError` for linkage or parts the wire cannot
    express instead of degrading them to plain text.
    """
    out: list[dict[str, Any]] = []
    for m in messages:
        linked = _map_tool_linkage(m)
        if linked is not None:
            out.append(linked)
            continue
        if not m.parts:
            out.append({"role": m.role, "content": m.content})
            continue
        # Multimodal path: only user messages support content parts.
        if m.role not in ("", "user"):
            raise llm.LLMError(f"routellm: message role {m.role!r} does not support content parts")
        out.append({"role": "user", "content": [_map_part(p) for p in m.parts]})
    return out


def _map_tool_linkage(m: llm.Message) -> dict[str, Any] | None:
    """Map the tool-call half of a conversation, or return None."""
    if m.role == "tool":
        if not m.tool_call_id:
            raise llm.LLMError("routellm: tool result needs tool_call_id")
        if m.tool_calls or m.parts:
            raise llm.LLMError(f"routellm: tool result {m.tool_call_id!r} carries only content")
        return {"role": "tool", "content": m.content, "tool_call_id": m.tool_call_id}
    if m.tool_call_id:
        raise llm.LLMError(f"routellm: tool_call_id is valid only on role tool, got {m.role!r}")
    if not m.tool_calls:
        return None
    if m.role != "assistant":
        raise llm.LLMError(f"routellm: tool_calls are valid only on role assistant, got {m.role!r}")
    if m.parts:
        raise llm.LLMError("routellm: assistant tool calls do not support content parts")

    msg: dict[str, Any] = {"role": "assistant"}
    if m.content:
        msg["content"] = m.content
    msg["tool_calls"] = [
        {
            "id": _require_call_id(tc),
            "type": "function",
            "function": {"name": tc.name, "arguments": _arguments_json(tc)},
        }
        for tc in m.tool_calls
    ]
    return msg


def _require_call_id(tc: llm.ToolCall) -> str:
    if not tc.id:
        raise llm.LLMError(f"routellm: tool call {tc.name!r} needs an id")
    return tc.id


def _arguments_json(tc: llm.ToolCall) -> str:
    """Return the call's arguments as a JSON string; empty means ``{}``."""
    args = tc.arguments
    if args is None or args in ("", b""):
        return "{}"
    if isinstance(args, bytes):
        args = args.decode("utf-8")
    if isinstance(args, str):
        try:
            json.loads(args)
        except ValueError:
            raise llm.LLMError(
                f"routellm: tool call {tc.id!r} arguments are not valid JSON"
            ) from None
        return args
    try:
        return json.dumps(args, separators=(",", ":"))
    except (TypeError, ValueError) as exc:
        raise llm.LLMError(f"routellm: tool call {tc.id!r} arguments are not valid JSON") from exc


def _map_part(p: llm.ContentPart) -> dict[str, Any]:
    if p.type == llm.PartType.TEXT:
        return {"type": "text", "text": p.text}
    if p.type != llm.PartType.IMAGE:
        raise llm.LLMError(f"routellm: unsupported modality {str(p.type)!r}")

    mime = p.mime_type or (p.source.mime_type if p.source is not None else "")
    if mime == "application/pdf":
        return {"type": "file", "file": {"file_data": _read_base64(p.source)}}
    if p.source is not None and p.source.url:
        return {"type": "image_url", "image_url": {"url": p.source.url}}
    if not mime:
        raise llm.LLMError("routellm: image part has no MIME type")
    data = _read_base64(p.source)
    return {"type": "image_url", "image_url": {"url": f"data:{mime};base64,{data}"}}


def _read_base64(src: llm.MediaSource | None) -> str:
    if src is None:
        raise llm.LLMError("routellm: content part has no media source")
    return base64.b64encode(src.read()).decode("ascii")


# ------------------------------------------------------------------
# Adapter
# ------------------------------------------------------------------


class RouteLLMAdapter:
    """Completer + Streamer backed by RouteLLM's Controller."""

    def __init__(self, cfg: llm.ResolvedConfig) -> None:
        if Controller is None:
            raise ImportError("routellm is not installed. Install it with: pip install routellm")

        extras: dict[str, Any] = cfg.provider.extras
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

    # -- Completer protocol ------------------------------------

    def complete(self, req: llm.Request) -> llm.Response:
        messages = map_messages(req.messages)

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
        messages = map_messages(req.messages)

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
