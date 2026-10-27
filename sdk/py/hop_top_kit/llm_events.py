"""
hop_top_kit.llm_events — the metadata-only payloads of the llm lifecycle
events ``kit.ai.request.started`` and ``kit.ai.response.received``.

A payload describes a call without its content: no message text, tool
names or arguments, extension values or media, so a bus that forwards
events (to a network peer, a log file, a database) carries no prompt.
:func:`request_start_payload` and :func:`request_end_payload` build them
as Go's client does; :meth:`RequestStartPayload.to_dict` and
:meth:`RequestEndPayload.to_dict` give their JSON.

The Python :class:`~hop_top_kit.llm.Client` publishes no bus events: its
``on_request`` and ``on_response`` hooks run in process and get the full
request and response, as Go's ``OnRequest`` and ``OnResponse`` do. Code
that publishes llm events from Python builds the payload here, never
from the request itself.

Go ``ai/llm`` (``RequestStartPayload``, ``RequestEndPayload``); pinned by
``contracts/llm-v1`` (``bus_request_started``, ``bus_response_received``).
"""

from __future__ import annotations

import json
from collections.abc import Iterable
from dataclasses import dataclass, field
from typing import Any

from hop_top_kit import llm

__all__ = [
    "RequestEndPayload",
    "RequestStartPayload",
    "request_end_payload",
    "request_start_payload",
]


@dataclass
class RequestStartPayload:
    """``kit.ai.request.started``: a request described, without content.

    ``op`` is the call: ``complete``, ``stream``, ``tool_call``,
    ``stream_tools``, or a media call. ``content_bytes`` is the UTF-8
    length of the messages' text: content, text parts, tool call
    arguments and thinking text; media is not counted. ``tools`` counts
    the tools offered; ``extension_keys`` lists the request's extension
    keys, sorted, never their values.
    """

    op: str
    model: str = ""
    messages: int = 0
    roles: dict[str, int] = field(default_factory=dict)
    parts: dict[str, int] = field(default_factory=dict)
    content_bytes: int = 0
    tools: int = 0
    max_tokens: int = 0
    has_temperature: bool = False
    thinking: bool = False
    stop_sequences: int = 0
    extension_keys: list[str] = field(default_factory=list)

    def to_dict(self) -> dict[str, Any]:
        """The payload as JSON-ready data; empty optional fields left out."""
        out: dict[str, Any] = {"op": self.op}
        if self.model:
            out["model"] = self.model
        out["messages"] = self.messages
        if self.roles:
            out["roles"] = dict(self.roles)
        if self.parts:
            out["parts"] = dict(self.parts)
        out.update(
            content_bytes=self.content_bytes,
            tools=self.tools,
            max_tokens=self.max_tokens,
            has_temperature=self.has_temperature,
            thinking=self.thinking,
            stop_sequences=self.stop_sequences,
        )
        if self.extension_keys:
            out["extension_keys"] = list(self.extension_keys)
        return out


@dataclass
class RequestEndPayload:
    """``kit.ai.response.received``: a successful call's response
    described, without content. ``content_bytes`` counts the response's
    text: content, tool call arguments and thinking text; ``tool_calls``
    counts the calls the model made. ``duration`` is in seconds and goes
    out as integer ``duration_ms``."""

    op: str
    duration: float = 0.0
    finish_reason: str = ""
    usage: llm.Usage = field(default_factory=llm.Usage)
    content_bytes: int = 0
    tool_calls: int = 0

    def to_dict(self) -> dict[str, Any]:
        """The payload as JSON-ready data; usage spelled as the bus
        contract does."""
        out: dict[str, Any] = {"op": self.op}
        if self.finish_reason:
            out["finish_reason"] = self.finish_reason
        u = self.usage
        out.update(
            content_bytes=self.content_bytes,
            tool_calls=self.tool_calls,
            duration_ms=int(self.duration * 1000),
            usage={
                "prompt_tokens": u.prompt_tokens,
                "completion_tokens": u.completion_tokens,
                "cache_read_tokens": u.cache_read_tokens,
                "cache_creation_tokens": u.cache_creation_tokens,
                "total_tokens": u.total_tokens,
            },
        )
        return out


def _len(s: str) -> int:
    return len(s.encode("utf-8"))


def _arguments_bytes(args: Any) -> int:
    """A call's arguments as sent: JSON text or bytes as given, any other
    value as compact JSON."""
    if args is None:
        return 0
    if isinstance(args, (bytes, bytearray)):
        return len(args)
    if isinstance(args, str):
        return _len(args)
    try:
        return _len(json.dumps(args, separators=(",", ":")))
    except (TypeError, ValueError):
        return 0


def _calls_bytes(calls: Iterable[llm.ToolCall]) -> int:
    return sum(_arguments_bytes(c.arguments) for c in calls)


def _thinking_bytes(tbs: Iterable[llm.ThinkingBlock]) -> int:
    return sum(_len(t.thinking) for t in tbs)


def request_start_payload(op: str, req: llm.Request, *, tools: int = 0) -> RequestStartPayload:
    """*req* described for the bus, as call *op* offering *tools* tools."""
    p = RequestStartPayload(
        op=op,
        model=req.model,
        messages=len(req.messages),
        tools=tools,
        max_tokens=req.max_tokens,
        has_temperature=req.temperature is not None,
        thinking=req.thinking is not None,
        stop_sequences=len(req.stop_sequences),
        extension_keys=sorted(req.extensions),
    )
    for m in req.messages:
        p.roles[m.role] = p.roles.get(m.role, 0) + 1
        p.content_bytes += (
            _len(m.content) + _calls_bytes(m.tool_calls) + _thinking_bytes(m.thinking)
        )
        for part in m.parts:
            kind = str(part.type)
            p.parts[kind] = p.parts.get(kind, 0) + 1
            if part.type == llm.PartType.TEXT:
                p.content_bytes += _len(part.text)
    return p


def request_end_payload(
    op: str,
    resp: llm.Response,
    tool_calls: Iterable[llm.ToolCall] = (),
    *,
    duration: float = 0.0,
) -> RequestEndPayload:
    """*resp*, with the *tool_calls* the model made, described for the
    bus; *duration* in seconds."""
    calls = list(tool_calls)
    return RequestEndPayload(
        op=op,
        duration=duration,
        finish_reason=resp.finish_reason,
        usage=resp.usage or llm.Usage(),
        content_bytes=_len(resp.content) + _calls_bytes(calls) + _thinking_bytes(resp.thinking),
        tool_calls=len(calls),
    )
