"""
hop_top_kit.llm_openai — the OpenAI Chat Completions adapter, over
:mod:`hop_top_kit.llm_http` (no SDK).

Registers the schemes ``openai``, ``openrouter``, ``xai``, ``lmstudio``,
``groq``, ``together``, ``fireworks``, ``deepseek`` and ``mistral``, each
with its default base URL. ``lmstudio`` defaults to LM Studio's local
server, ``http://localhost:1234/v1``, takes no key, and appends ``/v1``
to any base URL that lacks it. An ``openai`` host-form URI
(``openai://host:port/model``) names a self-hosted OpenAI-compatible
server and reaches it at ``http://host:port/v1``; an ``openai``
``base_url`` is used as given.

Implements :class:`~hop_top_kit.llm.Completer`,
:class:`~hop_top_kit.llm.Streamer`, :class:`~hop_top_kit.llm.ToolCaller`
and :class:`~hop_top_kit.llm.ToolStreamer`. The key is sent as a bearer
token when not blank; no credential is read from the environment, and
none goes over plain http to a remote host. Provider params:
``tool_names`` (:mod:`hop_top_kit.llm_toolnames`, rule
:data:`~hop_top_kit.llm_toolnames.OPENAI_TOOL_NAMES`),
``max_body_bytes`` and ``max_event_bytes``.
:attr:`~hop_top_kit.llm.Request.tool_choice` maps by :func:`tool_choice`
(``any`` is ``"required"``).

The wire mapping (:func:`map_messages`, :func:`map_tools`,
:func:`parse_completion`, :class:`ChunkAccumulator`) is shared with
:mod:`hop_top_kit.routellm_adapter`, which speaks the same shape.

Go ``ai/llm/openai``.
"""

from __future__ import annotations

import base64
import json
import time
from collections.abc import Callable, Iterator
from dataclasses import dataclass, field
from typing import Any

from hop_top_kit import llm
from hop_top_kit.llm_endpoint import check_key_endpoint, is_blank, redact_uri
from hop_top_kit.llm_http import (
    DEFAULT_MAX_RETRIES,
    Limits,
    SSEReader,
    Transport,
    classify,
    error_body,
)
from hop_top_kit.llm_toolnames import OPENAI_TOOL_NAMES, ToolNames, tool_name_mode_of

__all__ = [
    "DEFAULT_BASE_URLS",
    "SCHEMES",
    "ChunkAccumulator",
    "OpenAIAdapter",
    "OpenAIStream",
    "map_messages",
    "map_tools",
    "parse_completion",
]

SCHEMES = (
    "openai",
    "openrouter",
    "xai",
    "lmstudio",
    "groq",
    "together",
    "fireworks",
    "deepseek",
    "mistral",
)

OPENAI_BASE_URL = "https://api.openai.com/v1"

# Each scheme's default API base URL; unlisted schemes use OpenAI's.
DEFAULT_BASE_URLS = {
    "openai": OPENAI_BASE_URL,
    "openrouter": "https://openrouter.ai/api/v1",
    "xai": "https://api.x.ai/v1",
    "groq": "https://api.groq.com/openai/v1",
    "together": "https://api.together.xyz/v1",
    "fireworks": "https://api.fireworks.ai/inference/v1",
    "deepseek": "https://api.deepseek.com",
    "mistral": "https://api.mistral.ai/v1",
    "lmstudio": "http://localhost:1234/v1",
}

_q = llm._go_quote


# ---------------------------------------------------------------------------
# Wire mapping (shared with routellm)
# ---------------------------------------------------------------------------


def map_messages(
    messages: list[llm.Message], names: ToolNames | None = None, *, provider: str = "openai"
) -> list[dict[str, Any]]:
    """Map :class:`~hop_top_kit.llm.Message` values to chat messages.

    An assistant turn carrying ``tool_calls`` becomes a message with
    ``tool_calls`` (arguments as a JSON string); a role ``"tool"`` result
    a ``tool`` message with ``tool_call_id``; ``parts`` a user content
    array. Other messages map to ``{"role", "content"}``. With *names*,
    a replayed call's name goes out as its wire name. Raises
    :class:`~hop_top_kit.llm.LLMError` (prefixed *provider*) for linkage
    or parts the wire cannot express instead of degrading them.
    """
    out: list[dict[str, Any]] = []
    for m in messages:
        linked = _map_tool_linkage(m, names, provider)
        if linked is not None:
            out.append(linked)
            continue
        if not m.parts:
            out.append({"role": m.role, "content": m.content})
            continue
        # Multimodal path: only user messages support content parts.
        if m.role not in ("", "user"):
            raise llm.LLMError(
                f"{provider}: message role {m.role!r} does not support content parts"
            )
        out.append({"role": "user", "content": [_map_part(p, provider) for p in m.parts]})
    return out


def _map_tool_linkage(
    m: llm.Message, names: ToolNames | None, provider: str
) -> dict[str, Any] | None:
    if m.role == "tool":
        if not m.tool_call_id:
            raise llm.LLMError(f"{provider}: tool result needs tool_call_id")
        if m.tool_calls or m.parts:
            raise llm.LLMError(f"{provider}: tool result {m.tool_call_id!r} carries only content")
        return {"role": "tool", "content": m.content, "tool_call_id": m.tool_call_id}
    if m.tool_call_id:
        raise llm.LLMError(f"{provider}: tool_call_id is valid only on role tool, got {m.role!r}")
    if not m.tool_calls:
        return None
    if m.role != "assistant":
        raise llm.LLMError(
            f"{provider}: tool_calls are valid only on role assistant, got {m.role!r}"
        )
    if m.parts:
        raise llm.LLMError(f"{provider}: assistant tool calls do not support content parts")
    msg: dict[str, Any] = {"role": "assistant"}
    if m.content:
        msg["content"] = m.content
    msg["tool_calls"] = [
        {
            "id": _require_call_id(tc, provider),
            "type": "function",
            "function": {
                "name": names.wire(tc.name) if names else tc.name,
                "arguments": _arguments_json(tc, provider),
            },
        }
        for tc in m.tool_calls
    ]
    return msg


def _require_call_id(tc: llm.ToolCall, provider: str) -> str:
    if not tc.id:
        raise llm.LLMError(f"{provider}: tool call {tc.name!r} needs an id")
    return tc.id


def _arguments_json(tc: llm.ToolCall, provider: str) -> str:
    """The call's arguments as a JSON string; empty means ``{}``."""
    args = tc.arguments
    if args is None or args in ("", b""):
        return "{}"
    if isinstance(args, (bytes, bytearray)):
        args = bytes(args).decode("utf-8")
    if isinstance(args, str):
        try:
            json.loads(args)
        except ValueError:
            raise llm.LLMError(
                f"{provider}: tool call {tc.id!r} arguments are not valid JSON"
            ) from None
        return args
    try:
        return json.dumps(args, separators=(",", ":"))
    except (TypeError, ValueError) as exc:
        raise llm.LLMError(f"{provider}: tool call {tc.id!r} arguments are not valid JSON") from exc


def _map_part(p: llm.ContentPart, provider: str) -> dict[str, Any]:
    if p.type == llm.PartType.TEXT:
        return {"type": "text", "text": p.text}
    if p.type != llm.PartType.IMAGE:
        raise llm.LLMError(f"{provider}: unsupported modality {str(p.type)!r}")
    mime = p.mime_type or (p.source.mime_type if p.source is not None else "")
    if mime == "application/pdf":
        return {"type": "file", "file": {"file_data": _read_base64(p.source, provider)}}
    if p.source is not None and p.source.url:
        return {"type": "image_url", "image_url": {"url": p.source.url}}
    if not mime:
        raise llm.LLMError(f"{provider}: image part has no MIME type")
    data = _read_base64(p.source, provider)
    return {"type": "image_url", "image_url": {"url": f"data:{mime};base64,{data}"}}


def _read_base64(src: llm.MediaSource | None, provider: str) -> str:
    if src is None:
        raise llm.LLMError(f"{provider}: content part has no media source")
    return base64.b64encode(src.read()).decode("ascii")


def check_tool_choice(provider: str, req: llm.Request, tools: list[llm.ToolDef] | None) -> None:
    """Raise :class:`~hop_top_kit.llm.LLMError` when the request's
    tool_choice cannot go with *tools* (``None`` for a call without)."""
    if (why := llm.check_tool_choice(req.tool_choice, tools)) is not None:
        raise llm.LLMError(f"{provider}: {why}")


def tool_choice(c: llm.ToolChoice, names: ToolNames) -> Any:
    """A checked choice on the wire: ``any`` is ``"required"``, a named
    tool goes by the name the request sends for it."""
    if c.mode == llm.ToolChoiceMode.ANY:
        return "required"
    if c.mode == llm.ToolChoiceMode.TOOL:
        return {"type": "function", "function": {"name": names.wire(c.name)}}
    return str(c.mode)


def map_tools(defs: list[llm.ToolDef], names: ToolNames) -> list[dict[str, Any]]:
    """Function tools under their *names* wire names."""
    out = []
    for d in defs:
        fn: dict[str, Any] = {"name": names.wire(d.name)}
        if d.description:
            fn["description"] = d.description
        schema = llm._schema_object(d.parameters)
        if schema is not None:
            fn["parameters"] = schema
        out.append({"type": "function", "function": fn})
    return out


def _s(v: Any) -> str:
    return v if isinstance(v, str) else ""


def _n(v: Any) -> int:
    return v if isinstance(v, int) and not isinstance(v, bool) else 0


def usage_of(u: Any) -> llm.Usage | None:
    if not isinstance(u, dict):
        return None
    return llm.Usage(
        prompt_tokens=_n(u.get("prompt_tokens")),
        completion_tokens=_n(u.get("completion_tokens")),
        total_tokens=_n(u.get("total_tokens")),
    )


def _arguments_text(v: Any) -> str:
    if isinstance(v, str):
        return v
    return json.dumps(v, separators=(",", ":")) if v is not None else ""


@dataclass
class Completion:
    """A parsed Chat Completions response (first choice)."""

    content: str = ""
    role: str = ""
    finish_reason: str = ""
    tool_calls: list[llm.ToolCall] = field(default_factory=list)
    usage: llm.Usage | None = None


def parse_completion(v: Any, names: ToolNames | None = None) -> Completion:
    """The first choice of a Chat Completions response; each call
    carries the name the caller defined, through *names*."""
    v = v if isinstance(v, dict) else {}
    out = Completion(usage=usage_of(v.get("usage")))
    choices = v.get("choices") or []
    if choices and isinstance(choices[0], dict):
        c = choices[0]
        msg = c.get("message") or {}
        out.content = _s(msg.get("content"))
        out.role = _s(msg.get("role"))
        out.finish_reason = _s(c.get("finish_reason"))
        for tc in msg.get("tool_calls") or []:
            fn = tc.get("function") or {}
            name = _s(fn.get("name"))
            out.tool_calls.append(
                llm.ToolCall(
                    id=_s(tc.get("id")),
                    name=names.original(name) if names else name,
                    arguments=_arguments_text(fn.get("arguments")),
                )
            )
    return out


@dataclass
class _PartialCall:
    id: str = ""
    name: str = ""
    arguments: str = ""


class ChunkAccumulator:
    """Folds streamed Chat Completions chunks into a turn: content, tool
    calls (by index, arguments concatenated), finish reason, usage."""

    def __init__(self) -> None:
        self.content = ""
        self.finish_reason = ""
        self.usage: llm.Usage | None = None
        self._calls: dict[int, _PartialCall] = {}

    def apply(self, chunk: Any) -> str:
        """Apply one chunk; return its content delta."""
        if not isinstance(chunk, dict):
            return ""
        u = usage_of(chunk.get("usage"))
        if u is not None:
            self.usage = u
        choices = chunk.get("choices") or []
        if not choices or not isinstance(choices[0], dict):
            return ""
        c = choices[0]
        if c.get("finish_reason"):
            self.finish_reason = _s(c.get("finish_reason"))
        delta = c.get("delta") or {}
        for tc in delta.get("tool_calls") or []:
            if not isinstance(tc, dict):
                continue
            idx = tc.get("index")
            idx = idx if isinstance(idx, int) else len(self._calls)
            pc = self._calls.setdefault(idx, _PartialCall())
            fn = tc.get("function") or {}
            if tc.get("id") and not pc.id:
                pc.id = _s(tc.get("id"))
            if fn.get("name") and not pc.name:
                pc.name = _s(fn.get("name"))
            pc.arguments += _arguments_text(fn.get("arguments"))
        text = _s(delta.get("content"))
        self.content += text
        return text

    def outcome(self, names: ToolNames | None = None) -> llm.StreamOutcome:
        calls = [
            llm.ToolCall(
                id=pc.id,
                name=names.original(pc.name) if names else pc.name,
                arguments=pc.arguments or "{}",
            )
            for _, pc in sorted(self._calls.items())
        ]
        return llm.StreamOutcome(
            content=self.content,
            tool_calls=calls,
            finish_reason=self.finish_reason,
            usage=self.usage or llm.Usage(),
        )


# ---------------------------------------------------------------------------
# Adapter
# ---------------------------------------------------------------------------


def _api_root_v1(base: str) -> str:
    trimmed = base.rstrip("/")
    return base if trimmed.endswith("/v1") else trimmed + "/v1"


def _is_host_form(cfg: llm.ResolvedConfig) -> bool:
    if "base_url" in cfg.uri.params:
        return False
    return bool(cfg.uri.host) and cfg.provider.base_url == f"http://{cfg.uri.host}"


class OpenAIAdapter:
    """Completer, Streamer, ToolCaller and ToolStreamer for Chat
    Completions and OpenAI-compatible servers. *sleep*, *max_retries*
    and *timeout* as for :class:`~hop_top_kit.llm_anthropic.AnthropicAdapter`.
    """

    def __init__(
        self,
        cfg: llm.ResolvedConfig,
        *,
        sleep: Callable[[float], None] | None = time.sleep,
        max_retries: int = DEFAULT_MAX_RETRIES,
        timeout: float = 600.0,
    ) -> None:
        scheme = cfg.uri.scheme or "openai"
        self.scheme = scheme
        p = cfg.provider
        base = p.base_url or DEFAULT_BASE_URLS.get(scheme, OPENAI_BASE_URL)
        if scheme == "lmstudio" or (scheme == "openai" and _is_host_form(cfg)):
            base = _api_root_v1(base)
        if not base.lower().startswith(("http://", "https://")):
            raise llm.LLMError(f"{scheme}: invalid base URL: {_q(redact_uri(base))}")
        self._tool_names = tool_name_mode_of(p.params)
        limits = Limits.from_params(scheme, p.params)
        key = "" if is_blank(p.api_key) else p.api_key
        check_key_endpoint(scheme, key, base)
        headers = {"authorization": f"Bearer {key}"} if key else {}
        self._model = p.model or cfg.uri.model
        self._transport = Transport(
            provider=scheme,
            url=base.rstrip("/") + "/chat/completions",
            headers=headers,
            secrets=[key],
            limits=limits,
            max_retries=max_retries,
            sleep=sleep,
            timeout=timeout,
        )

    def __repr__(self) -> str:
        return f"OpenAIAdapter(url={self._transport.destination!r}, model={self._model!r})"

    def close(self) -> None:
        """No-op: each request opens and closes its own connection."""

    def _names(self, tools: list[llm.ToolDef] | None) -> ToolNames:
        return ToolNames.build(OPENAI_TOOL_NAMES, self._tool_names, tools or [])

    def build_body(
        self,
        req: llm.Request,
        tools: list[llm.ToolDef] | None,
        *,
        stream: bool,
        names: ToolNames,
    ) -> dict[str, Any]:
        check_tool_choice(self.scheme, req, tools)
        b: dict[str, Any] = {
            "model": req.model or self._model,
            "messages": map_messages(req.messages, names, provider=self.scheme),
        }
        if req.temperature is not None:
            b["temperature"] = req.temperature
        if req.max_tokens > 0:
            b["max_tokens"] = req.max_tokens
        if req.stop_sequences:
            b["stop"] = list(req.stop_sequences)
        if tools is not None:
            b["tools"] = map_tools(tools, names)
        if req.tool_choice is not None:
            b["tool_choice"] = tool_choice(req.tool_choice, names)
        if stream:
            b["stream"] = True
        return b

    def _call(self, req: llm.Request, tools: list[llm.ToolDef] | None) -> Completion:
        names = self._names(tools)
        body = self.build_body(req, tools, stream=False, names=names)
        resp = self._transport.post(body, stream=False, model=req.model or self._model)
        return parse_completion(self._transport.read_json(resp), names)

    def _open_stream(self, req: llm.Request, tools: list[llm.ToolDef] | None) -> OpenAIStream:
        names = self._names(tools)
        body = self.build_body(req, tools, stream=True, names=names)
        model = req.model or self._model
        resp = self._transport.post(body, stream=True, model=model)
        return OpenAIStream(
            SSEReader(self._transport, resp),
            provider=self.scheme,
            model=model,
            scrub=self._transport.scrub,
            names=names,
        )

    def complete(self, req: llm.Request) -> llm.Response:
        c = self._call(req, None)
        return llm.Response(
            content=c.content, role=c.role, usage=c.usage, finish_reason=c.finish_reason
        )

    def stream(self, req: llm.Request) -> OpenAIStream:
        return self._open_stream(req, None)

    def call_with_tools(self, req: llm.Request, tools: list[llm.ToolDef]) -> llm.ToolResponse:
        c = self._call(req, tools)
        return llm.ToolResponse(
            content=c.content,
            tool_calls=c.tool_calls,
            finish_reason=c.finish_reason,
            usage=c.usage,
        )

    def stream_with_tools(self, req: llm.Request, tools: list[llm.ToolDef]) -> OpenAIStream:
        return self._open_stream(req, tools)


class OpenAIStream:
    """A streamed Chat Completions turn: iterate for content tokens, then
    one ``done`` token; :meth:`outcome` is the folded turn after it.

    ``data: [DONE]`` ends the stream; a body ending without it raises
    :class:`~hop_top_kit.llm.StreamTruncatedError` unless a chunk already
    carried a ``finish_reason``. A chunk carrying ``error`` raises the
    error it maps to.
    """

    def __init__(
        self,
        reader: SSEReader,
        *,
        provider: str,
        model: str,
        scrub: Callable[[str], str],
        names: ToolNames,
    ) -> None:
        self._reader = reader
        self._provider = provider
        self._model = model
        self._scrub = scrub
        self._names = names
        self._acc = ChunkAccumulator()
        self._finished = False
        self._outcome: llm.StreamOutcome | None = None

    def __iter__(self) -> Iterator[llm.Token]:
        return self

    def __enter__(self) -> OpenAIStream:
        return self

    def __exit__(self, *exc: object) -> None:
        self.close()

    def outcome(self) -> llm.StreamOutcome | None:
        return self._outcome

    def close(self) -> None:
        self._finished = True
        self._reader.close()

    def _done(self) -> llm.Token:
        self._outcome = self._acc.outcome(self._names)
        self.close()
        return llm.Token(done=True)

    def _fail(self, err: Exception) -> Exception:
        self.close()
        return err

    def __next__(self) -> llm.Token:
        while True:
            if self._finished:
                raise StopIteration
            ev = self._reader.next_event()
            if ev is None:
                if self._acc.finish_reason:
                    return self._done()
                raise self._fail(llm.StreamTruncatedError(self._provider))
            data = ev.data.strip()
            if not data:
                continue
            if data == "[DONE]":
                return self._done()
            try:
                chunk = json.loads(data)
            except ValueError as e:
                raise self._fail(
                    llm.LLMError(f"{self._provider}: decode stream chunk: {e}")
                ) from None
            if isinstance(chunk, dict) and isinstance(chunk.get("error"), dict):
                etype, msg = error_body(self._scrub(data))
                status = chunk["error"].get("code")
                status = status if isinstance(status, int) else 0
                raise self._fail(classify(self._provider, status, None, etype, msg, self._model))
            text = self._acc.apply(chunk)
            if text:
                return llm.Token(content=text)


# ---------------------------------------------------------------------------
# Registration
# ---------------------------------------------------------------------------


def _factory(cfg: llm.ResolvedConfig) -> OpenAIAdapter:
    return OpenAIAdapter(cfg)


def _register() -> None:
    import contextlib

    for s in SCHEMES:
        with contextlib.suppress(llm.LLMError):
            llm.register(s, _factory, endpoints=[DEFAULT_BASE_URLS.get(s, OPENAI_BASE_URL)])


_register()
