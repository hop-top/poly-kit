"""
hop_top_kit.llm_anthropic — the Anthropic Messages API adapter for the
``anthropic://`` scheme, over :mod:`hop_top_kit.llm_http` (no SDK).

Implements :class:`~hop_top_kit.llm.Completer`,
:class:`~hop_top_kit.llm.Streamer`, :class:`~hop_top_kit.llm.ToolCaller`
and :class:`~hop_top_kit.llm.ToolStreamer`, with extended thinking
(:attr:`~hop_top_kit.llm.Request.thinking`) and prompt caching
(``cache_control`` on messages and tools).

Credentials: the key comes from the resolved config and is required.
``ANTHROPIC_BASE_URL`` sets the endpoint below a configured base URL;
``ANTHROPIC_AUTH_TOKEN`` (sent as a bearer token when not blank) and
``ANTHROPIC_WORKSPACE_ID`` go only to a trusted origin: the default
endpoint, ``ANTHROPIC_BASE_URL``, llm.yaml ``base_url``, ``LLM_BASE_URL``
or loopback. No credential goes over plain http to a remote host. A
hermetic config (:attr:`~hop_top_kit.llm.ResolvedConfig.hermetic`) reads
none of these, nor ``LLM_BASE_URL`` or llm.yaml: only the config counts.

:attr:`~hop_top_kit.llm.Request.tool_choice` maps to the body's
``tool_choice`` (a named tool by its sent name);
:attr:`~hop_top_kit.llm.Request.stream_events` adds tool-call and usage
tokens to a stream (see :class:`AnthropicStream`).

Provider params: ``tool_names`` (``sanitize``, the default, or
``strict``; see :mod:`hop_top_kit.llm_toolnames`), ``max_body_bytes``
and ``max_event_bytes`` (see :class:`~hop_top_kit.llm_http.Limits`).

Go ``ai/llm/anthropic``; pinned by ``contracts/llm-v1`` (the
``anthropic_*`` and ``credential_headers`` sections).
"""

from __future__ import annotations

import base64
import json
import os
import time
from collections.abc import Callable, Iterator, Mapping
from dataclasses import dataclass, field
from typing import Any

from hop_top_kit import llm, llm_config
from hop_top_kit.llm_endpoint import check_key_endpoint, credentials_trusted, is_blank, redact_uri
from hop_top_kit.llm_http import (
    DEFAULT_MAX_RETRIES,
    Limits,
    SSEReader,
    Transport,
    classify,
    error_body,
)
from hop_top_kit.llm_toolnames import ANTHROPIC_TOOL_NAMES, ToolNames, tool_name_mode_of

__all__ = [
    "API_VERSION",
    "DEFAULT_BASE_URL",
    "DEFAULT_MAX_TOKENS",
    "SCHEME",
    "AnthropicAdapter",
    "AnthropicStream",
]

SCHEME = "anthropic"
DEFAULT_BASE_URL = "https://api.anthropic.com"
API_VERSION = "2023-06-01"
# max_tokens when a request leaves it 0.
DEFAULT_MAX_TOKENS = 1024
# The API's least thinking budget.
MIN_THINKING_BUDGET = 1024

_q = llm._go_quote

Env = Callable[[str], str | None]


def _invalid(msg: str) -> llm.LLMError:
    return llm.LLMError(f"{SCHEME}: {msg}")


# ---------------------------------------------------------------------------
# Adapter
# ---------------------------------------------------------------------------


class AnthropicAdapter:
    """Completer, Streamer, ToolCaller and ToolStreamer for the Messages API.

    Keyword arguments beyond *cfg* exist for tests and embedding: *env*
    reads ``ANTHROPIC_*`` (default: the process environment),
    *config_file* is the llm.yaml data whose
    ``providers.anthropic.base_url`` counts as trusted (default: read
    from disk), *sleep* waits between retries (``None`` disables
    retries), *max_retries* counts retries after the first attempt. A
    hermetic *cfg* ignores *env* and *config_file*: it reads neither.
    """

    def __init__(
        self,
        cfg: llm.ResolvedConfig,
        *,
        env: Env | None = None,
        config_file: Mapping[str, Any] | None = None,
        sleep: Callable[[float], None] | None = time.sleep,
        max_retries: int = DEFAULT_MAX_RETRIES,
        timeout: float = 600.0,
    ) -> None:
        p = cfg.provider
        if cfg.hermetic:
            # A hermetic adapter reads no environment variable and no file.
            env, config_file = (lambda _k: None), {}
        env = env or os.environ.get
        if is_blank(p.api_key):
            raise _invalid("API key required")
        env_base = env("ANTHROPIC_BASE_URL")
        if p.base_url:
            what, base = "base URL", p.base_url
        elif env_base:
            what, base = "ANTHROPIC_BASE_URL", env_base
        else:
            what, base = "base URL", DEFAULT_BASE_URL
        if not base.lower().startswith(("http://", "https://")):
            raise _invalid(f"invalid {what}: {_q(redact_uri(base))}")
        self._tool_names = tool_name_mode_of(p.params)
        limits = Limits.from_params(SCHEME, p.params)

        check_key_endpoint(SCHEME, p.api_key, base)
        creds: dict[str, str] = {"x-api-key": p.api_key}
        env_creds: dict[str, str] = {}
        token = env("ANTHROPIC_AUTH_TOKEN") or ""
        if not is_blank(token):
            check_key_endpoint(SCHEME, token, base)
            env_creds["authorization"] = f"Bearer {token}"
        workspace = env("ANTHROPIC_WORKSPACE_ID") or ""
        if workspace:
            env_creds["anthropic-workspace-id"] = workspace
        # Without env credentials, what is trusted (llm.yaml,
        # LLM_BASE_URL) is not asked.
        if env_creds:
            file = config_file if config_file is not None else llm_config._load_yaml_config()
            section = ((file or {}).get("providers") or {}).get(SCHEME) or {}
            trusted = [
                DEFAULT_BASE_URL,
                env_base or "",
                env("LLM_BASE_URL") or "",
                str(section.get("base_url") or ""),
            ]
            if credentials_trusted(base, trusted):
                creds.update(env_creds)
        self._credentials = creds
        self._model = p.model
        self._transport = Transport(
            provider=SCHEME,
            url=base.rstrip("/") + "/v1/messages",
            headers={"anthropic-version": API_VERSION, **creds},
            secrets=[p.api_key, token],
            limits=limits,
            max_retries=max_retries,
            sleep=sleep,
            timeout=timeout,
        )

    def __repr__(self) -> str:
        return (
            f"AnthropicAdapter(url={self._transport.destination!r}, model={self._model!r}, "
            f"credentials={self.credential_headers()!r})"
        )

    def credential_headers(self) -> list[str]:
        """The names of the credential headers this adapter sends, never
        their values."""
        return list(self._credentials)

    def close(self) -> None:
        """No-op: each request opens and closes its own connection."""

    def _model_for(self, req: llm.Request) -> str:
        return req.model or self._model

    def _names(self, tools: list[llm.ToolDef] | None) -> ToolNames:
        return ToolNames.build(ANTHROPIC_TOOL_NAMES, self._tool_names, tools or [])

    def _message(self, req: llm.Request, tools: list[llm.ToolDef] | None) -> _Parsed:
        names = self._names(tools)
        body = build_body(self._model, req, tools, stream=False, names=names)
        resp = self._transport.post(body, stream=False, model=self._model_for(req))
        parsed = parse_message(self._transport.read_json(resp))
        parsed.tool_calls = names.calls(parsed.tool_calls)
        return parsed

    def _open_stream(self, req: llm.Request, tools: list[llm.ToolDef] | None) -> AnthropicStream:
        names = self._names(tools)
        body = build_body(self._model, req, tools, stream=True, names=names)
        model = self._model_for(req)
        resp = self._transport.post(body, stream=True, model=model)
        return AnthropicStream(
            SSEReader(self._transport, resp),
            model=model,
            scrub=self._transport.scrub,
            thinking=req.thinking is not None,
            names=names,
            events=req.stream_events,
        )

    # -- Completer / Streamer / ToolCaller / ToolStreamer ---------------

    def complete(self, req: llm.Request) -> llm.Response:
        p = self._message(req, None)
        return llm.Response(
            content=p.content,
            role="assistant",
            usage=p.usage,
            finish_reason=p.stop_reason,
            thinking=p.thinking,
        )

    def stream(self, req: llm.Request) -> AnthropicStream:
        return self._open_stream(req, None)

    def call_with_tools(self, req: llm.Request, tools: list[llm.ToolDef]) -> llm.ToolResponse:
        p = self._message(req, tools)
        return llm.ToolResponse(
            content=p.content,
            tool_calls=p.tool_calls,
            thinking=p.thinking,
            finish_reason=p.stop_reason,
            usage=p.usage,
        )

    def stream_with_tools(self, req: llm.Request, tools: list[llm.ToolDef]) -> AnthropicStream:
        return self._open_stream(req, tools)


# ---------------------------------------------------------------------------
# Request shaping
# ---------------------------------------------------------------------------


def build_body(
    default_model: str,
    req: llm.Request,
    tools: list[llm.ToolDef] | None,
    *,
    stream: bool,
    names: ToolNames,
) -> dict[str, Any]:
    """The Messages API body for *req*; tool definitions and replayed
    calls go out under their *names* wire names. Raises
    :class:`~hop_top_kit.llm.LLMError` for a request the API cannot
    express, before anything is sent."""
    if (why := llm.check_tool_choice(req.tool_choice, tools)) is not None:
        raise _invalid(why)
    system, messages = _messages(req.messages, names)
    max_tokens = req.max_tokens if req.max_tokens > 0 else DEFAULT_MAX_TOKENS
    b: dict[str, Any] = {
        "model": req.model or default_model,
        "max_tokens": max_tokens,
        "messages": messages,
    }
    if system:
        b["system"] = system
    if req.thinking is not None:
        budget = req.thinking.budget_tokens
        if budget < MIN_THINKING_BUDGET:
            raise _invalid(
                f"thinking budget_tokens must be at least {MIN_THINKING_BUDGET}, got {budget}"
            )
        if budget >= max_tokens:
            raise _invalid(
                f"thinking budget_tokens ({budget}) must be less than max_tokens ({max_tokens})"
            )
        b["thinking"] = {"type": "enabled", "budget_tokens": budget}
    if req.temperature is not None:
        b["temperature"] = req.temperature
    if req.stop_sequences:
        b["stop_sequences"] = list(req.stop_sequences)
    if tools is not None:
        b["tools"] = [_tool_def(d, names) for d in tools]
    if (c := req.tool_choice) is not None:
        b["tool_choice"] = _tool_choice(c, names)
    if stream:
        b["stream"] = True
    return b


def _tool_choice(c: llm.ToolChoice, names: ToolNames) -> dict[str, Any]:
    """A checked choice as the body's ``tool_choice``; a named tool goes
    by the name the request sends for it."""
    if c.mode == llm.ToolChoiceMode.TOOL:
        return {"type": "tool", "name": names.wire(c.name)}
    return {"type": str(c.mode)}


def _text_block(text: str) -> dict[str, Any]:
    return {"type": "text", "text": text}


def _cache_value(cc: llm.CacheControl) -> dict[str, Any]:
    if cc.ttl == "":
        return {"type": "ephemeral"}
    if cc.ttl in ("5m", "1h"):
        return {"type": "ephemeral", "ttl": cc.ttl}
    raise _invalid(f'cache_control ttl must be "5m" or "1h", got {_q(cc.ttl)}')


def _set_cache(block: dict[str, Any], cc: llm.CacheControl | None) -> None:
    if cc is not None:
        block["cache_control"] = _cache_value(cc)


def _replay_thinking(tbs: list[llm.ThinkingBlock]) -> list[dict[str, Any]]:
    out = []
    for t in tbs:
        if t.redacted:
            out.append({"type": "redacted_thinking", "data": t.redacted})
        else:
            out.append({"type": "thinking", "thinking": t.thinking, "signature": t.signature})
    return out


def _messages(
    msgs: list[llm.Message], names: ToolNames
) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    """The top-level system blocks (see :func:`_system_blocks`) and the
    conversation without them."""
    system = _system_blocks(msgs)
    out: list[dict[str, Any]] = []
    for i, m in enumerate(msgs):
        if m.role == "system":
            continue
        _check_linkage_role(m)
        if m.role == "tool":
            block = _tool_result(m)
            _set_cache(block, m.cache_control)
            # Consecutive results answer one assistant turn: one user turn.
            if i > 0 and msgs[i - 1].role == "tool" and out:
                out[-1]["content"].append(block)
                continue
            out.append({"role": "user", "content": [block]})
            continue
        if m.parts:
            blocks = [_part(p) for p in m.parts]
        elif not m.tool_calls or m.content:
            blocks = [_text_block(m.content)]
        else:
            blocks = []
        blocks.extend(_tool_use(tc, names) for tc in m.tool_calls)
        if blocks:
            _set_cache(blocks[-1], m.cache_control)
        out.append({"role": m.role, "content": _replay_thinking(m.thinking) + blocks})
    return system, out


def _system_blocks(msgs: list[llm.Message]) -> list[dict[str, Any]]:
    """Every system message, wherever it sits, as Go joins them: the
    contents in order joined with a blank line, empty ones skipped; a
    cache_control on a system message ends a text block there, carrying
    that breakpoint. No system text: no blocks."""
    out: list[dict[str, Any]] = []
    text = ""
    for m in msgs:
        if m.role != "system":
            continue
        if m.content:
            text = f"{text}\n\n{m.content}" if text else m.content
        if m.cache_control is None or not text:
            continue
        block = _text_block(text)
        _set_cache(block, m.cache_control)
        out.append(block)
        text = ""
    if text:
        out.append(_text_block(text))
    return out


def _check_linkage_role(m: llm.Message) -> None:
    if m.tool_calls and m.role != "assistant":
        raise _invalid(f"ToolCalls are valid only on role assistant, got {_q(m.role)}")
    if m.thinking and m.role != "assistant":
        raise _invalid(f"Thinking is valid only on role assistant, got {_q(m.role)}")
    if m.tool_call_id and m.role != "tool":
        raise _invalid(f"ToolCallID is valid only on role tool, got {_q(m.role)}")


def _tool_result(m: llm.Message) -> dict[str, Any]:
    if not m.tool_call_id:
        raise _invalid("tool result needs ToolCallID")
    if m.parts:
        raise _invalid(f"tool result {_q(m.tool_call_id)} carries only Content")
    b: dict[str, Any] = {"type": "tool_result", "tool_use_id": m.tool_call_id}
    # The API rejects empty text blocks: an empty result sends none.
    if m.content:
        b["content"] = [_text_block(m.content)]
    return b


def arguments_value(provider: str, tc: llm.ToolCall) -> Any:
    """A call's arguments as a JSON value; empty means ``{}``."""
    args = tc.arguments
    if args is None or args in ("", b""):
        return {}
    if isinstance(args, (str, bytes, bytearray)):
        try:
            return json.loads(args)
        except ValueError:
            raise llm.LLMError(
                f"{provider}: tool call {_q(tc.id)} arguments are not valid JSON"
            ) from None
    try:
        json.dumps(args)
    except (TypeError, ValueError) as exc:
        raise llm.LLMError(
            f"{provider}: tool call {_q(tc.id)} arguments are not valid JSON"
        ) from exc
    return args


def _tool_use(tc: llm.ToolCall, names: ToolNames) -> dict[str, Any]:
    if not tc.id:
        raise _invalid(f"tool call {_q(tc.name)} needs an ID")
    return {
        "type": "tool_use",
        "id": tc.id,
        "name": names.wire(tc.name),
        "input": arguments_value(SCHEME, tc),
    }


def _part(p: llm.ContentPart) -> dict[str, Any]:
    if p.type == llm.PartType.TEXT:
        return _text_block(p.text)
    if p.type != llm.PartType.IMAGE:
        raise llm.UnsupportedModalityError(str(p.type), SCHEME)
    mime = p.mime_type or (p.source.mime_type if p.source is not None else "")
    if not mime:
        raise llm.InvalidFormatError("", SCHEME)
    if p.source is None:
        raise _invalid("content part has no media source")
    data = base64.b64encode(p.source.read()).decode("ascii")
    kind = "document" if mime == "application/pdf" else "image"
    return {"type": kind, "source": {"type": "base64", "media_type": mime, "data": data}}


def _tool_def(d: llm.ToolDef, names: ToolNames) -> dict[str, Any]:
    # The API requires input_schema: a tool without parameters, or with
    # ones that are not an object, takes an empty object schema.
    schema = llm._schema_object(d.parameters)
    if schema is None:
        schema = {"properties": {}}
    schema.setdefault("type", "object")
    out = {"name": names.wire(d.name), "description": d.description, "input_schema": schema}
    _set_cache(out, d.cache_control)
    return out


# ---------------------------------------------------------------------------
# Response mapping
# ---------------------------------------------------------------------------


@dataclass
class _Parsed:
    content: str = ""
    thinking: list[llm.ThinkingBlock] = field(default_factory=list)
    tool_calls: list[llm.ToolCall] = field(default_factory=list)
    stop_reason: str = ""
    usage: llm.Usage = field(default_factory=llm.Usage)


def _n(v: Any) -> int:
    return v if isinstance(v, int) and not isinstance(v, bool) else 0


def usage_of(u: Any) -> llm.Usage:
    u = u if isinstance(u, dict) else {}
    out = llm.Usage(
        prompt_tokens=_n(u.get("input_tokens")),
        completion_tokens=_n(u.get("output_tokens")),
        cache_read_tokens=_n(u.get("cache_read_input_tokens")),
        cache_creation_tokens=_n(u.get("cache_creation_input_tokens")),
    )
    return _total(out)


def _total(u: llm.Usage) -> llm.Usage:
    u.total_tokens = (
        u.prompt_tokens + u.completion_tokens + u.cache_read_tokens + u.cache_creation_tokens
    )
    return u


def _s(v: Any) -> str:
    return v if isinstance(v, str) else ""


def parse_message(v: Any) -> _Parsed:
    """A non-streamed Messages API response."""
    v = v if isinstance(v, dict) else {}
    out = _Parsed(stop_reason=_s(v.get("stop_reason")), usage=usage_of(v.get("usage")))
    for block in v.get("content") or []:
        if not isinstance(block, dict):
            continue
        kind = block.get("type")
        if kind == "text":
            out.content += _s(block.get("text"))
        elif kind == "thinking":
            out.thinking.append(
                llm.ThinkingBlock(
                    thinking=_s(block.get("thinking")), signature=_s(block.get("signature"))
                )
            )
        elif kind == "redacted_thinking":
            out.thinking.append(llm.ThinkingBlock(redacted=_s(block.get("data"))))
        elif kind == "tool_use":
            out.tool_calls.append(
                llm.ToolCall(
                    id=_s(block.get("id")),
                    name=_s(block.get("name")),
                    arguments=json.dumps(block.get("input", {}), separators=(",", ":")),
                )
            )
    return out


# ---------------------------------------------------------------------------
# Streaming
# ---------------------------------------------------------------------------

# An SSE error event's type, as the status it stands for.
_EVENT_STATUS = {
    "invalid_request_error": 400,
    "authentication_error": 401,
    "billing_error": 402,
    "permission_error": 403,
    "not_found_error": 404,
    "request_too_large": 413,
    "rate_limit_error": 429,
    "api_error": 500,
    "timeout_error": 504,
    "overloaded_error": 529,
}

_EVENTS = frozenset(
    {
        "message_start",
        "message_delta",
        "message_stop",
        "content_block_start",
        "content_block_delta",
        "content_block_stop",
    }
)


@dataclass
class _Block:
    kind: str = ""
    text: str = ""
    thinking: str = ""
    signature: str = ""
    data: str = ""
    id: str = ""
    name: str = ""
    input: str = ""


class _Accumulator:
    """Folds stream events into blocks, stop reason and usage, rule for
    rule the Go SDK's ``Message.Accumulate``."""

    def __init__(self) -> None:
        self.blocks: list[_Block] = []
        self.stop_reason = ""
        self.usage = llm.Usage()

    def apply(self, kind: str, ev: dict[str, Any]) -> tuple[str, str] | None:
        """Apply one event; return ``("text"|"thinking", delta)`` when it
        carried one."""
        if kind == "message_start":
            m = ev.get("message") or {}
            self.blocks = []
            self.stop_reason = _s(m.get("stop_reason"))
            self.usage = usage_of(m.get("usage"))
        elif kind == "message_delta":
            # Cumulative totals: they overwrite, never add.
            self.stop_reason = _s((ev.get("delta") or {}).get("stop_reason"))
            u = ev.get("usage") or {}
            self.usage.completion_tokens = _n(u.get("output_tokens"))
            for src, dst in (
                ("input_tokens", "prompt_tokens"),
                ("cache_read_input_tokens", "cache_read_tokens"),
                ("cache_creation_input_tokens", "cache_creation_tokens"),
            ):
                if isinstance(u.get(src), int):
                    setattr(self.usage, dst, u[src])
        elif kind == "content_block_start":
            index = ev.get("index")
            if index != len(self.blocks):
                raise llm.LLMError(
                    f"{SCHEME}: received event of type {kind} for content block at index "
                    f"{index}, expected index {len(self.blocks)}"
                )
            cb = ev.get("content_block") or {}
            btype = _s(cb.get("type"))
            if "input" in cb:
                inp = json.dumps(cb["input"], separators=(",", ":"))
            else:
                inp = "{}" if btype == "tool_use" else ""
            self.blocks.append(
                _Block(
                    kind=btype,
                    text=_s(cb.get("text")),
                    thinking=_s(cb.get("thinking")),
                    signature=_s(cb.get("signature")),
                    data=_s(cb.get("data")),
                    id=_s(cb.get("id")),
                    name=_s(cb.get("name")),
                    input=inp,
                )
            )
        elif kind == "content_block_delta":
            index = ev.get("index")
            if not isinstance(index, int) or not 0 <= index < len(self.blocks):
                raise llm.LLMError(
                    f"{SCHEME}: received event of type {kind} for content block at index "
                    f"{index}, have {len(self.blocks)} blocks"
                )
            b = self.blocks[index]
            d = ev.get("delta") or {}
            dtype = d.get("type")
            if dtype == "text_delta":
                b.text += _s(d.get("text"))
                return ("text", _s(d.get("text")))
            if dtype == "input_json_delta":
                part = _s(d.get("partial_json"))
                if part:
                    b.input = part if b.input == "{}" else b.input + part
            elif dtype == "thinking_delta":
                b.thinking += _s(d.get("thinking"))
                return ("thinking", _s(d.get("thinking")))
            elif dtype == "signature_delta":
                b.signature += _s(d.get("signature"))
        return None

    def outcome(self) -> llm.StreamOutcome:
        out = llm.StreamOutcome(
            finish_reason=self.stop_reason, usage=_total(llm.Usage(**vars(self.usage)))
        )
        for b in self.blocks:
            if b.kind == "text":
                out.content += b.text
            elif b.kind == "thinking":
                out.thinking.append(llm.ThinkingBlock(thinking=b.thinking, signature=b.signature))
            elif b.kind == "redacted_thinking":
                out.thinking.append(llm.ThinkingBlock(redacted=b.data))
            elif b.kind == "tool_use":
                out.tool_calls.append(llm.ToolCall(id=b.id, name=b.name, arguments=b.input))
        return out


class AnthropicStream:
    """A streamed Messages API turn: iterate for tokens; :meth:`outcome`
    is the folded turn once the ``done`` token was yielded.

    Text deltas yield :attr:`Token.content`; thinking deltas yield
    :attr:`Token.thinking` when the request enabled thinking. With
    :attr:`~hop_top_kit.llm.Request.stream_events`, ``message_start`` and
    ``message_delta`` also yield a cumulative :attr:`Token.usage` snapshot,
    and each ``tool_use`` block a start, its argument deltas and an end
    :attr:`Token.tool_call` event (see
    :class:`~hop_top_kit.llm.ToolCallEvent`); a call still open when the
    stream fails gets no end. The body
    ending without ``message_stop`` raises
    :class:`~hop_top_kit.llm.StreamTruncatedError`; an SSE ``error``
    event raises the error its type maps to (``overloaded_error`` an
    :class:`~hop_top_kit.llm.HTTPStatusError` 529, fallbackable).
    """

    def __init__(
        self,
        reader: SSEReader,
        *,
        model: str,
        scrub: Callable[[str], str],
        thinking: bool,
        names: ToolNames,
        events: bool = False,
    ) -> None:
        self._reader = reader
        self._model = model
        self._scrub = scrub
        self._thinking = thinking
        self._names = names
        self._events = events
        # A tool_use block's content index to its call index.
        self._calls: dict[int, int] = {}
        self._acc = _Accumulator()
        self._finished = False
        self._outcome: llm.StreamOutcome | None = None

    def __iter__(self) -> Iterator[llm.Token]:
        return self

    def __enter__(self) -> AnthropicStream:
        return self

    def __exit__(self, *exc: object) -> None:
        self.close()

    def outcome(self) -> llm.StreamOutcome | None:
        return self._outcome

    def close(self) -> None:
        self._finished = True
        self._reader.close()

    def _fail(self, err: Exception) -> Exception:
        self.close()
        return err

    def __next__(self) -> llm.Token:
        while True:
            if self._finished:
                raise StopIteration
            ev = self._reader.next_event()
            if ev is None:
                # No message_stop: the response is incomplete.
                raise self._fail(llm.StreamTruncatedError(SCHEME))
            if ev.event == "error":
                etype, msg = error_body(self._scrub(ev.data))
                status = _EVENT_STATUS.get(etype, 0)
                raise self._fail(classify(SCHEME, status, None, etype, msg, self._model))
            if ev.event not in _EVENTS:
                continue
            try:
                data = json.loads(ev.data)
            except ValueError as e:
                raise self._fail(llm.LLMError(f"{SCHEME}: decode {ev.event} event: {e}")) from None
            try:
                delta = self._acc.apply(ev.event, data if isinstance(data, dict) else {})
            except llm.LLMError as e:
                raise self._fail(e) from None
            if delta is not None:
                kind, text = delta
                if kind == "text":
                    return llm.Token(content=text)
                if self._thinking:
                    return llm.Token(thinking=text)
                continue
            if self._events and (tok := self._event(ev.event, data)) is not None:
                return tok
            if ev.event == "message_stop":
                out = self._acc.outcome()
                out.tool_calls = self._names.calls(out.tool_calls)
                self._outcome = out
                self.close()
                return llm.Token(done=True)

    def _event(self, kind: str, data: Any) -> llm.Token | None:
        """An accumulated event as a tool-call or usage token, if it is one."""
        if kind in ("message_start", "message_delta"):
            return llm.Token(usage=_total(llm.Usage(**vars(self._acc.usage))))
        index = data.get("index") if isinstance(data, dict) else None
        if kind == "content_block_start":
            b = self._acc.blocks[-1]
            if b.kind != "tool_use":
                return None
            i = self._calls[index] = len(self._calls)
            return llm.Token(
                tool_call=llm.ToolCallEvent(
                    kind=llm.ToolCallEventKind.START,
                    index=i,
                    id=b.id,
                    name=self._names.original(b.name),
                )
            )
        if kind == "content_block_delta":
            d = data.get("delta") or {}
            part = _s(d.get("partial_json"))
            if index not in self._calls or d.get("type") != "input_json_delta" or not part:
                return None
            return llm.Token(
                tool_call=llm.ToolCallEvent(
                    kind=llm.ToolCallEventKind.DELTA, index=self._calls[index], arguments_delta=part
                )
            )
        if kind == "content_block_stop" and index in self._calls:
            b = self._acc.blocks[index]
            call = llm.ToolCall(id=b.id, name=self._names.original(b.name), arguments=b.input)
            return llm.Token(
                tool_call=llm.ToolCallEvent(
                    kind=llm.ToolCallEventKind.END,
                    index=self._calls[index],
                    id=call.id,
                    name=call.name,
                    call=call,
                )
            )
        return None


# ---------------------------------------------------------------------------
# Registration
# ---------------------------------------------------------------------------


def _factory(cfg: llm.ResolvedConfig) -> AnthropicAdapter:
    return AnthropicAdapter(cfg)


def _register() -> None:
    import contextlib

    with contextlib.suppress(llm.LLMError):
        llm.register(SCHEME, _factory, endpoints=[DEFAULT_BASE_URL])


_register()
