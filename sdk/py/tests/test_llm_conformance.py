"""llm-v1 conformance runner for the Python SDK.

Loads ``contracts/llm-v1/vectors.json`` from the repo root and runs every
section: the provider-neutral core (``redact_uri``, ``parse_uri``,
``key_endpoint``, ``usage_cost``, ``tool_names``), the bus payloads
(``bus_*``, against :mod:`hop_top_kit.llm_events`' builders: the Python
client publishes no bus events) and the anthropic adapter
(``anthropic_*``, ``credential_headers``) against a loopback server. The vectors are the source of truth: a failure means this port
drifted from Go. Strict: a section the runner does not consume, or a
vector field it does not know, fails the run.
"""

from __future__ import annotations

import base64
import json
import os
import urllib.request
from pathlib import Path
from typing import Any

import pytest
import yaml

from hop_top_kit import llm, llm_events
from hop_top_kit.llm_anthropic import AnthropicAdapter
from hop_top_kit.llm_endpoint import redact_uri
from hop_top_kit.llm_toolnames import (
    ANTHROPIC_TOOL_NAMES,
    GEMINI_TOOL_NAMES,
    OPENAI_TOOL_NAMES,
    ToolNameMode,
    ToolNames,
)
from tests.llm_fakeserver import Canned, serve

RULES = {
    "anthropic": ANTHROPIC_TOOL_NAMES,
    "openai": OPENAI_TOOL_NAMES,
    "gemini": GEMINI_TOOL_NAMES,
}

CONSUMED = {
    "redact_uri",
    "parse_uri",
    "anthropic_request",
    "anthropic_request_error",
    "anthropic_headers",
    "anthropic_stream",
    "anthropic_accumulate",
    "anthropic_error",
    "key_endpoint",
    "credential_headers",
    "anthropic_limits",
    "anthropic_response",
    "usage_cost",
    "tool_names",
    "anthropic_tool_names",
    "anthropic_tool_choice",
    "anthropic_hermetic",
    "anthropic_stream_events",
    "bus_request_started",
    "bus_response_received",
    "media_fetch",
}

_META = {"version", "description"}
_TOOL_NAMES_FIELDS = {"name", "rule", "mode", "tools", "want", "error", "calls", "want_calls"}
_ERROR_FIELDS = {"reason", "name", "other"}

# The key error vectors run with; no error text may hold it.
CONF_KEY = "conf-key-must-not-leak"

# Environment the anthropic adapter and config loading read; cleared so
# the host's own settings never reach a vector.
_ENV_VARS = (
    "ANTHROPIC_API_KEY",
    "ANTHROPIC_BASE_URL",
    "ANTHROPIC_AUTH_TOKEN",
    "ANTHROPIC_WORKSPACE_ID",
    "LLM_API_KEY",
    "LLM_BASE_URL",
    "LLM_PROVIDER",
    "LLM_FALLBACK",
)


def _locate() -> Path:
    here = Path(__file__).resolve().parent
    for candidate in (here, *here.parents):
        target = candidate / "contracts" / "llm-v1" / "vectors.json"
        if target.is_file():
            return target
    raise FileNotFoundError(f"contracts/llm-v1/vectors.json: not found walking up from {here}")


_VECTORS: dict[str, Any] = json.loads(_locate().read_text(encoding="utf-8"))


def _cases(section: str) -> list[Any]:
    vs = _VECTORS[section]
    assert vs, f"{section} is empty"
    return [pytest.param(v, id=v["name"]) for v in vs]


@pytest.fixture
def clean_env(monkeypatch, tmp_path):
    for k in _ENV_VARS:
        monkeypatch.delenv(k, raising=False)
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    return tmp_path


def test_version_is_v1() -> None:
    assert _VECTORS["version"] == "v1"
    assert _VECTORS["description"]


def test_every_section_is_consumed() -> None:
    sections = set(_VECTORS) - _META
    assert sections == CONSUMED, (
        f"not consumed: {sorted(sections - CONSUMED)}; gone: {sorted(CONSUMED - sections)}"
    )


def test_no_unconsumed_files() -> None:
    names = {p.name for p in _locate().parent.iterdir()}
    assert names <= {"vectors.json", "README.md", "tool-names.md"}, names


# ---------------------------------------------------------------------------
# Core
# ---------------------------------------------------------------------------


@pytest.mark.parametrize("v", _cases("redact_uri"))
def test_redact_uri(v: dict[str, Any]) -> None:
    assert redact_uri(v["in"]) == v["want"]


@pytest.mark.parametrize("v", _cases("parse_uri"))
def test_parse_uri(v: dict[str, Any]) -> None:
    if v["error"]:
        with pytest.raises(llm.LLMError) as ei:
            llm.parse_uri(v["uri"])
        assert str(ei.value) == v["error"]
        return
    u = llm.parse_uri(v["uri"])
    assert (u.scheme, u.model, u.host) == (v["scheme"], v["model"], v["host"])
    assert u.params == (v["params"] or {})


@pytest.mark.parametrize("v", _cases("usage_cost"))
def test_usage_cost(v: dict[str, Any]) -> None:
    u = llm.Usage(**v["usage"])
    p = llm.Price(**v["price"])
    assert abs(u.cost(p) - v["want"]) < 1e-9


@pytest.mark.parametrize("v", _cases("key_endpoint"))
def test_key_endpoint(v: dict[str, Any], clean_env, monkeypatch) -> None:
    if v["yaml"]:
        (clean_env / "hop").mkdir()
        (clean_env / "hop" / "llm.yaml").write_text(v["yaml"])
    for k, val in v["env"].items():
        monkeypatch.setenv(k, val)
    op = v["op"]
    try:
        if op == "apply":
            got = llm.apply_api_key(v["uri"])
            assert got == v["want_uri"]
        elif op == "load":
            llm.load_config(v["uri"])
        elif op == "resolve":
            llm.resolve(v["uri"])
        else:
            pytest.fail(f"unknown op {op!r}")
    except llm.KeyEndpointError:
        assert v["refused"], "refused"
        return
    assert not v["refused"], "not refused"


@pytest.mark.parametrize("v", _cases("tool_names"))
def test_tool_names(v: dict[str, Any]) -> None:
    extra = set(v) - _TOOL_NAMES_FIELDS
    assert not extra, f"unknown fields {sorted(extra)}"
    assert v["rule"] in RULES, f"unknown rule {v['rule']!r}"
    rule = RULES[v["rule"]]
    mode = ToolNameMode(v.get("mode") or "sanitize")
    defs = [llm.ToolDef(name=n, description="") for n in v["tools"]]

    if "error" in v:
        assert "want" not in v
        assert not set(v["error"]) - _ERROR_FIELDS
        with pytest.raises(llm.ToolNameError) as ei:
            ToolNames.build(rule, mode, defs)
        err = ei.value
        assert err.reason == v["error"]["reason"]
        assert err.name == v["error"]["name"]
        assert err.other == v["error"]["other"]
        assert err.provider == rule.provider
        assert not llm.is_fallbackable(err)
        return

    names = ToolNames.build(rule, mode, defs)
    got = [names.wire(n) for n in v["tools"]]
    assert got == v["want"]
    for w in got:
        assert rule.valid(w), f"wire name {w!r} invalid for {v['rule']}"
    if mode is ToolNameMode.SANITIZE:
        assert [rule.sanitize(n) for n in v["tools"]] == v["want"]
    back = [names.original(c) for c in v.get("calls", [])]
    assert back == v.get("want_calls", [])


# ---------------------------------------------------------------------------
# Anthropic adapter
# ---------------------------------------------------------------------------

_OK_MESSAGE = (
    '{"id":"msg_1","type":"message","role":"assistant","model":"m",'
    '"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn",'
    '"usage":{"input_tokens":1,"output_tokens":1}}'
)


def _no_sleep(_: float) -> None:
    pass


def _adapter(
    base: str,
    model: str = "m",
    key: str = "k",
    *,
    env: dict[str, str] | None = None,
    params: dict[str, str] | None = None,
    config_file: dict[str, Any] | None = None,
    sleep=_no_sleep,
) -> AnthropicAdapter:
    cfg = llm.ResolvedConfig(
        uri=llm.URI(scheme="anthropic", model=model),
        provider=llm.ProviderConfig(
            api_key=key, base_url=base, model=model, params=dict(params or {})
        ),
    )
    e = dict(env or {})
    return AnthropicAdapter(
        cfg, env=e.get, config_file=config_file or {}, sleep=sleep, max_retries=2
    )


def _cache(c: dict[str, Any] | None) -> llm.CacheControl | None:
    return None if c is None else llm.CacheControl(ttl=c["ttl"])


def _thoughts(ts: list[dict[str, Any]] | None) -> list[llm.ThinkingBlock]:
    return [llm.ThinkingBlock(**t) for t in ts or []]


def _part(p: dict[str, Any]) -> llm.ContentPart:
    if p["type"] == "text":
        return llm.ContentPart(type=llm.PartType.TEXT, text=p.get("text", ""))
    assert p["type"] == "image", p
    if p.get("url"):
        return llm.ContentPart(
            type=llm.PartType.IMAGE,
            source=_UnfetchedURL(p["url"]),
            mime_type=p.get("mime_type", ""),
        )
    return llm.ContentPart(
        type=llm.PartType.IMAGE,
        source=llm.InlineSource(base64.b64decode(p.get("data", "")), p.get("mime_type", "")),
        mime_type=p.get("mime_type", ""),
    )


class _UnfetchedURL:
    """A URL media source the adapter must send as a URL: reading it fails."""

    def __init__(self, url: str) -> None:
        self.url = url
        self.mime_type = ""

    def read(self) -> bytes:
        raise AssertionError(f"url source {self.url} was fetched; it must be sent as a url source")


def _message(m: dict[str, Any]) -> llm.Message:
    return llm.Message(
        role=m["role"],
        content=m.get("content", ""),
        parts=[_part(p) for p in m.get("parts", [])],
        tool_calls=[
            llm.ToolCall(id=c.get("id", ""), name=c["name"], arguments=c.get("arguments", ""))
            for c in m.get("tool_calls", [])
        ],
        tool_call_id=m.get("tool_call_id", ""),
        cache_control=_cache(m.get("cache_control")),
        thinking=_thoughts(m.get("thinking")),
    )


def _request(r: dict[str, Any]) -> llm.Request:
    budget = r.get("thinking_budget", 0)
    return llm.Request(
        messages=[_message(m) for m in r["messages"]],
        model=r.get("model", ""),
        temperature=r.get("temperature"),
        max_tokens=r.get("max_tokens", 0),
        stop_sequences=r.get("stop_sequences", []),
        extensions=dict(r.get("extensions") or {}),
        thinking=llm.ThinkingConfig(budget) if budget else None,
    )


def _tools(ts: list[dict[str, Any]]) -> list[llm.ToolDef]:
    return [
        llm.ToolDef(
            name=t["name"],
            description=t.get("description", ""),
            parameters=t.get("parameters") or None,
            cache_control=_cache(t.get("cache_control")),
        )
        for t in ts
    ]


def _hi() -> llm.Request:
    return llm.Request(messages=[llm.Message(role="user", content="Hi")])


def _numeric(v: Any) -> Any:
    """JSON compared as Go's JSONEq does: every number as a float."""
    if isinstance(v, bool) or v is None or isinstance(v, str):
        return v
    if isinstance(v, (int, float)):
        return float(v)
    if isinstance(v, list):
        return [_numeric(x) for x in v]
    return {k: _numeric(x) for k, x in v.items()}


def _kind(e: BaseException) -> tuple[str, int]:
    if isinstance(e, llm.StreamTruncatedError):
        return "truncated", 0
    if isinstance(e, llm.ResponseTooLargeError):
        return "too_large", 0
    if isinstance(e, llm.PermissionDeniedError):
        return (
            "permission_denied"
            if isinstance(e, llm.AuthError)
            else "permission_denied without auth"
        ), 0
    if isinstance(e, llm.AuthError):
        return "auth", 0
    if isinstance(e, llm.RateLimitError):
        return "rate_limit", 0
    if isinstance(e, llm.ModelError):
        return "model", 0
    if isinstance(e, llm.HTTPStatusError):
        return "http_status", e.status_code
    if isinstance(e, llm.APIError):
        return "api", e.status_code
    return "other", 0


def _drain(stream) -> list[llm.Token]:
    out = []
    for t in stream:
        out.append(t)
        if t.done:
            break
    return out


@pytest.mark.parametrize("v", _cases("anthropic_request"))
def test_anthropic_request(v: dict[str, Any], clean_env) -> None:
    with serve(Canned.json(200, _OK_MESSAGE)) as srv:
        a = _adapter(srv.url, v["adapter_model"])
        req = _request(v["request"])
        if v.get("tools") is not None:
            a.call_with_tools(req, _tools(v["tools"]))
        else:
            a.complete(req)
        got = srv.only()
    assert got.path == "/v1/messages"
    assert _numeric(got.json()) == _numeric(v["want_body"])


@pytest.mark.parametrize("v", _cases("anthropic_request_error"))
def test_anthropic_request_error(v: dict[str, Any], clean_env) -> None:
    with serve(Canned.json(200, _OK_MESSAGE)) as srv:
        a = _adapter(srv.url)
        with pytest.raises(llm.LLMError) as ei:
            a.complete(_request(v["request"]))
        assert str(ei.value) == v["error"]
        assert srv.attempts == 0, "a rejected request is never sent"


@pytest.mark.parametrize("v", _cases("anthropic_headers"))
def test_anthropic_headers(v: dict[str, Any], clean_env) -> None:
    with serve(Canned.json(200, _OK_MESSAGE)) as srv:
        a = _adapter(srv.url, key=v["api_key"], env=v["env"])
        a.complete(_hi())
        got = srv.only()
    for k, want in v["want"].items():
        assert got.headers.get(k) == want, k
    for k in v["absent"]:
        assert k not in got.headers, k


@pytest.mark.parametrize("v", _cases("anthropic_stream"))
def test_anthropic_stream(v: dict[str, Any], clean_env) -> None:
    with serve(Canned.sse(v["sse"])) as srv:
        a = _adapter(srv.url)
        req = _hi()
        if v["thinking_budget"]:
            req.max_tokens = 4 * v["thinking_budget"]
            req.thinking = llm.ThinkingConfig(v["thinking_budget"])
        s = a.stream(req)
        tokens: list[str] = []
        thinking: list[str] = []
        err: BaseException | None = None
        try:
            for t in s:
                if t.done:
                    assert next(s, None) is None, "next after done is end"
                    break
                if t.thinking:
                    thinking.append(t.thinking)
                else:
                    tokens.append(t.content)
        except llm.LLMError as e:
            err = e
        body = srv.only().json()
    assert tokens == v["want_tokens"]
    assert thinking == v["want_thinking"]
    assert (err is not None) == v["want_error"], err
    assert (_kind(err)[0] if err else "") == v["error_kind"], err
    assert body["stream"] is True


@pytest.mark.parametrize("v", _cases("anthropic_accumulate"))
def test_anthropic_accumulate(v: dict[str, Any], clean_env) -> None:
    streams = {f"anthropic_stream/{s['name']}": s["sse"] for s in _VECTORS["anthropic_stream"]}
    sse = streams[v["sse_from"]] if v.get("sse_from") else v["sse"]
    w = v["want"]
    with serve(Canned.sse(sse)) as srv:
        a = _adapter(srv.url)
        s = a.stream_with_tools(_hi(), [llm.ToolDef(name="get_weather", description="")])
        failed = False
        try:
            _drain(s)
        except llm.LLMError:
            failed = True
    assert failed == w["error"]
    if w["error"]:
        assert s.outcome() is None
        return
    out = s.outcome()
    assert out is not None
    assert out.content == w["text"]
    assert "".join(b.thinking for b in out.thinking) == w["thinking"]
    if w.get("thinking_blocks") is not None:
        assert out.thinking == _thoughts(w["thinking_blocks"])
    assert out.finish_reason == w["stop_reason"]
    u = w["usage"]
    assert (
        out.usage.prompt_tokens,
        out.usage.completion_tokens,
        out.usage.cache_read_tokens,
        out.usage.cache_creation_tokens,
    ) == (
        u["input_tokens"],
        u["output_tokens"],
        u.get("cache_read_tokens", 0),
        u.get("cache_creation_tokens", 0),
    )
    assert [(c.id, c.name, json.loads(c.arguments)) for c in out.tool_calls] == [
        (c["id"], c["name"], c["input"]) for c in w["tool_calls"]
    ]


@pytest.mark.parametrize("v", _cases("anthropic_error"))
def test_anthropic_error(v: dict[str, Any], clean_env) -> None:
    canned = []
    for r in v["responses"]:
        headers = dict(r["headers"])
        headers.setdefault("content-type", "application/json")
        canned.append(Canned(r["status"], headers, r["body"]))
    waits: list[float] = []
    with serve(*canned) as srv:
        a = _adapter(srv.url, key=CONF_KEY, sleep=waits.append)
        err: BaseException | None = None
        try:
            if v["stream"]:
                _drain(a.stream(_hi()))
            else:
                a.complete(_hi())
        except llm.LLMError as e:
            err = e
        attempts = srv.attempts
    if err is not None:
        assert CONF_KEY not in str(err)
        assert CONF_KEY not in repr(err)
    kind, status = _kind(err) if err else ("", 0)
    assert kind == v["want_kind"], err
    assert status == v["want_status"]
    assert (err is not None and llm.is_fallbackable(err)) == v["fallbackable"]
    assert attempts == v["attempts"]
    # A server wait is honored only within (0, 60s]; else backoff (<= 8s).
    assert all(0 <= w <= 60 for w in waits), waits
    assert len(waits) == attempts - 1


@pytest.mark.parametrize("v", _cases("credential_headers"))
def test_credential_headers(v: dict[str, Any], clean_env) -> None:
    file = yaml.safe_load(v["yaml"]) if v["yaml"] else {}
    try:
        a = _adapter(v["base_url"], key="kit-key", env=v["env"], config_file=file)
    except llm.KeyEndpointError:
        assert v["refused"], "refused"
        return
    assert not v["refused"], "not refused"
    names = a.credential_headers()
    for h in v["sent"]:
        assert h in names, h
    for h in v["withheld"]:
        assert h not in names, h


@pytest.mark.parametrize("v", _cases("anthropic_limits"))
def test_anthropic_limits(v: dict[str, Any], clean_env) -> None:
    with serve(Canned(200, {"content-type": v["content_type"]}, v["body"])) as srv:
        a = _adapter(srv.url, params=v["params"])
        try:
            if v["stream"]:
                _drain(a.stream(_hi()))
            else:
                a.complete(_hi())
        except llm.ResponseTooLargeError:
            assert v["too_large"]
            return
    assert not v["too_large"], "not refused"


@pytest.mark.parametrize("v", _cases("anthropic_response"))
def test_anthropic_response(v: dict[str, Any], clean_env) -> None:
    with serve(Canned.json(200, v["body"])) as srv:
        a = _adapter(srv.url)
        if v["tools"]:
            r = a.call_with_tools(_hi(), [llm.ToolDef(name="f", description="")])
            content, stop, usage, thinking, calls = (
                r.content,
                r.finish_reason,
                r.usage,
                r.thinking,
                r.tool_calls,
            )
        else:
            c = a.complete(_hi())
            content, stop, usage, thinking, calls = (
                c.content,
                c.finish_reason,
                c.usage,
                c.thinking,
                [],
            )
    w = v["want"]
    assert content == w["content"]
    assert stop == w["stop_reason"]
    assert usage is not None
    wu = w["usage"]
    assert (
        usage.prompt_tokens,
        usage.completion_tokens,
        usage.cache_read_tokens,
        usage.cache_creation_tokens,
        usage.total_tokens,
    ) == (
        wu["prompt_tokens"],
        wu["completion_tokens"],
        wu["cache_read_tokens"],
        wu["cache_creation_tokens"],
        wu.get("total_tokens", 0),
    )
    assert thinking == _thoughts(w["thinking_blocks"])
    assert [(c.id, json.loads(c.arguments)) for c in calls] == [
        (c["id"], c["input"]) for c in w["tool_calls"]
    ]


def _sent_tool_names(body: dict[str, Any]) -> tuple[list[str], list[str]]:
    tools = [t.get("name", "") for t in body.get("tools") or []]
    history = [
        c.get("name", "")
        for m in body.get("messages") or []
        for c in m.get("content") or []
        if c.get("type") == "tool_use"
    ]
    return tools, history


@pytest.mark.parametrize("v", _cases("anthropic_tool_names"))
def test_anthropic_tool_names(v: dict[str, Any], clean_env) -> None:
    streamed = v["call"] in ("stream", "stream_tools")
    body = v.get("body", "")
    canned = Canned.sse(body) if streamed else Canned.json(200, body or "{}")
    with serve(canned) as srv:
        try:
            a = _adapter(srv.url, params=v.get("params"))
        except llm.LLMError:
            assert v.get("refused"), "refused at construction"
            return
        assert not v.get("refused"), "not refused"
        req = _request({"messages": v["messages"]}) if v.get("messages") else _hi()
        defs = [llm.ToolDef(name=n, description="") for n in v.get("tools", [])]
        call = v["call"]
        try:
            if call == "tools":
                calls = a.call_with_tools(req, defs).tool_calls
            elif call == "stream_tools":
                s = a.stream_with_tools(req, defs)
                _drain(s)
                out = s.outcome()
                assert out is not None, "outcome after done"
                calls = out.tool_calls
            elif call == "complete":
                a.complete(req)
                calls = []
            elif call == "stream":
                _drain(a.stream(req))
                calls = []
            else:
                pytest.fail(f"unknown call {call!r}")
        except llm.ToolNameError as e:
            want = v.get("error")
            assert want, f"unexpected {e!r}"
            assert not llm.is_fallbackable(e)
            assert (e.reason, e.name, e.other) == (want["reason"], want["name"], want["other"])
            assert srv.attempts == 0, "a rejected request reaches no server"
            return
        assert not v.get("error"), "want a tool-name error"
        tools, history = _sent_tool_names(srv.only().json())
    assert tools == v.get("want_tools", [])
    assert history == v.get("want_history", [])
    want_calls = [(c["id"], c["name"]) for c in v.get("want_calls", [])]
    assert [(c.id, c.name) for c in calls] == want_calls


# A complete, empty streamed turn.
_MINIMAL_SSE = (
    "event: message_start\n"
    'data: {"type":"message_start","message":{"id":"msg_1","type":"message",'
    '"role":"assistant","content":[],"model":"m","stop_reason":null,'
    '"usage":{"input_tokens":1,"output_tokens":0}}}\n\n'
    "event: message_stop\n"
    'data: {"type":"message_stop"}\n\n'
)

_TOOL_CHOICE_FIELDS = {"name", "call", "tools", "tool_choice", "want_tool_choice", "error"}


@pytest.mark.parametrize("v", _cases("anthropic_tool_choice"))
def test_anthropic_tool_choice(v: dict[str, Any], clean_env) -> None:
    assert not set(v) - _TOOL_CHOICE_FIELDS, (
        f"unknown fields {sorted(set(v) - _TOOL_CHOICE_FIELDS)}"
    )
    call = v["call"]
    streamed = call in ("stream", "stream_tools")
    canned = Canned.sse(_MINIMAL_SSE) if streamed else Canned.json(200, _OK_MESSAGE)
    req = _hi()
    if (c := v["tool_choice"]) is not None:
        assert not set(c) - {"mode", "name"}, c
        req.tool_choice = llm.ToolChoice(mode=c["mode"], name=c.get("name", ""))
    defs = [llm.ToolDef(name=n, description="") for n in v["tools"]]
    with serve(canned) as srv:
        a = _adapter(srv.url)
        try:
            if call == "complete":
                a.complete(req)
            elif call == "stream":
                a.stream(req).close()
            elif call == "tools":
                a.call_with_tools(req, defs)
            elif call == "stream_tools":
                a.stream_with_tools(req, defs).close()
            else:
                pytest.fail(f"unknown call {call!r}")
        except llm.LLMError as e:
            assert v.get("error"), f"unexpected {e!r}"
            assert str(e) == v["error"]
            assert not llm.is_fallbackable(e)
            assert srv.attempts == 0, "a rejected request is never sent"
            return
        assert not v.get("error"), "want an error"
        body = srv.only().json()
    if v["want_tool_choice"] is None:
        assert "tool_choice" not in body, "no tool_choice sent"
        return
    assert body["tool_choice"] == v["want_tool_choice"]


class _Capture:
    """An opener recording the request the adapter makes, answered by a
    loopback server: the vector's host is never reached."""

    def __init__(self, target: str) -> None:
        self.target = target
        self.requests: list[urllib.request.Request] = []

    def open(self, req: urllib.request.Request, timeout: float | None = None) -> Any:
        self.requests.append(req)
        path = urllib.request.urlparse(req.full_url).path
        fwd = urllib.request.Request(
            self.target + path, data=req.data, headers=dict(req.header_items()), method="POST"
        )
        return urllib.request.build_opener(urllib.request.ProxyHandler({})).open(
            fwd, timeout=timeout
        )


_HERMETIC_FIELDS = {"name", "base_url", "yaml", "env", "want_host", "sent", "withheld"}


@pytest.mark.parametrize("v", _cases("anthropic_hermetic"))
def test_anthropic_hermetic(v: dict[str, Any], clean_env, monkeypatch) -> None:
    assert not set(v) - _HERMETIC_FIELDS, f"unknown fields {sorted(set(v) - _HERMETIC_FIELDS)}"
    if v["yaml"]:
        (clean_env / "hop").mkdir()
        (clean_env / "hop" / "llm.yaml").write_text(v["yaml"])
    for k, val in v["env"].items():
        monkeypatch.setenv(k, val)
    # No env or config_file override: the adapter would read the process
    # environment and llm.yaml, were it not hermetic.
    a = AnthropicAdapter(
        llm.ResolvedConfig(
            uri=llm.URI(scheme="anthropic", model="m"),
            provider=llm.ProviderConfig(api_key="kit-key", base_url=v["base_url"], model="m"),
            hermetic=True,
        ),
        sleep=None,
    )
    with serve(Canned.json(200, _OK_MESSAGE)) as srv:
        cap = _Capture(srv.url)
        a._transport._opener = cap  # type: ignore[assignment]
        a.complete(_hi())
    assert len(cap.requests) == 1
    req = cap.requests[0]
    assert urllib.request.urlparse(req.full_url).hostname == v["want_host"]
    headers = {k.lower(): val for k, val in req.header_items()}
    for h in v["sent"]:
        assert headers.get(h), h
    for h in v["withheld"]:
        assert h not in headers, h


def _usage_json(u: llm.Usage) -> dict[str, int]:
    return {
        "prompt_tokens": u.prompt_tokens,
        "completion_tokens": u.completion_tokens,
        "cache_read_tokens": u.cache_read_tokens,
        "cache_creation_tokens": u.cache_creation_tokens,
        "total_tokens": u.total_tokens,
    }


def _event_json(t: llm.Token) -> dict[str, Any]:
    """One streamed token as the vectors spell it."""
    if t.done:
        return {"type": "done"}
    if (ev := t.tool_call) is not None:
        out: dict[str, Any] = {"type": "tool_call", "kind": str(ev.kind), "index": ev.index}
        if ev.kind == llm.ToolCallEventKind.START:
            out.update(id=ev.id, name=ev.name)
        elif ev.kind == llm.ToolCallEventKind.DELTA:
            out["arguments"] = ev.arguments_delta
        else:
            assert ev.kind == llm.ToolCallEventKind.END, ev
            assert ev.call is not None
            assert (ev.id, ev.name) == (ev.call.id, ev.call.name), "end repeats id and name"
            out.update(id=ev.call.id, name=ev.call.name, arguments=json.loads(ev.call.arguments))
        return out
    if t.usage is not None:
        return {"type": "usage", "usage": _usage_json(t.usage)}
    if t.thinking:
        return {"type": "thinking", "thinking": t.thinking}
    return {"type": "text", "text": t.content}


_STREAM_EVENTS_FIELDS = {
    "name",
    "sse",
    "sse_from",
    "tools",
    "stream_events",
    "want_events",
    "error_kind",
}


@pytest.mark.parametrize("v", _cases("anthropic_stream_events"))
def test_anthropic_stream_events(v: dict[str, Any], clean_env) -> None:
    extra = set(v) - _STREAM_EVENTS_FIELDS
    assert not extra, f"unknown fields {sorted(extra)}"
    streams = {f"anthropic_stream/{s['name']}": s["sse"] for s in _VECTORS["anthropic_stream"]}
    sse = streams[v["sse_from"]] if v.get("sse_from") else v["sse"]
    req = _hi()
    req.stream_events = v["stream_events"]
    got: list[dict[str, Any]] = []
    ends: list[llm.ToolCallEvent] = []
    last: llm.Usage | None = None
    err: BaseException | None = None
    with serve(Canned.sse(sse)) as srv:
        a = _adapter(srv.url)
        if v["tools"]:
            s = a.stream_with_tools(req, [llm.ToolDef(name=n, description="") for n in v["tools"]])
        else:
            s = a.stream(req)
        try:
            for t in s:
                got.append(_event_json(t))
                if t.tool_call is not None and t.tool_call.kind == llm.ToolCallEventKind.END:
                    ends.append(t.tool_call)
                if t.usage is not None:
                    last = t.usage
                if t.done:
                    assert next(s, None) is None, "next after done is end"
                    break
        except llm.LLMError as e:
            err = e
    assert got == v["want_events"]
    assert (_kind(err)[0] if err else "") == v["error_kind"], err
    if not v["tools"] or err is not None:
        return
    # The events and the outcome tell the same story.
    out = s.outcome()
    assert out is not None
    if v["stream_events"]:
        assert len(out.tool_calls) == len(ends)
        for ev in ends:
            assert out.tool_calls[ev.index] == ev.call, "end call is outcome.tool_calls[index]"
        if last is not None:
            assert out.usage == last, "the last usage snapshot is outcome.usage"


_BUS_START_FIELDS = {"name", "op", "tools", "request", "want_payload"}


@pytest.mark.parametrize("v", _cases("bus_request_started"))
def test_bus_request_started(v: dict[str, Any]) -> None:
    assert not set(v) - _BUS_START_FIELDS, f"unknown fields {sorted(set(v) - _BUS_START_FIELDS)}"
    p = llm_events.request_start_payload(v["op"], _request(v["request"]), tools=len(v["tools"]))
    got = json.loads(json.dumps(p.to_dict()))
    assert _numeric(got) == _numeric(v["want_payload"])


_BUS_END_FIELDS = {"name", "op", "response", "tool_calls", "want_payload"}


@pytest.mark.parametrize("v", _cases("bus_response_received"))
def test_bus_response_received(v: dict[str, Any]) -> None:
    assert not set(v) - _BUS_END_FIELDS, f"unknown fields {sorted(set(v) - _BUS_END_FIELDS)}"
    r = v["response"]
    resp = llm.Response(
        content=r.get("content", ""),
        finish_reason=r.get("finish_reason", ""),
        usage=llm.Usage(**r["usage"]),
        thinking=_thoughts(r.get("thinking")),
    )
    calls = [
        llm.ToolCall(id=c["id"], name=c["name"], arguments=c["arguments"]) for c in v["tool_calls"]
    ]
    p = llm_events.request_end_payload(v["op"], resp, calls, duration=0.0123)
    got = json.loads(json.dumps(p.to_dict()))
    ms = got.pop("duration_ms")
    assert isinstance(ms, int) and not isinstance(ms, bool) and ms >= 0, ms
    assert _numeric(got) == _numeric(v["want_payload"])


def test_runner_env_is_clean(clean_env) -> None:
    """The runner clears the host's provider settings."""
    assert not any(os.environ.get(k) for k in _ENV_VARS)


# --- media_fetch ------------------------------------------------------------

_MEDIA_FETCH_FIELDS = {
    "name",
    "url",
    "max_bytes",
    "server",
    "error_kind",
    "message",
    "want_body",
    "want_size",
    "attempts",
}


def _media_kind(e: BaseException) -> str:
    if isinstance(e, llm.MediaTooLargeError):
        return "too_large"
    if isinstance(e, llm.HTTPStatusError) and 300 <= e.status_code < 400:
        return "redirect"
    if isinstance(e, llm.LLMError) and "refusing to fetch" in str(e):
        return "refused"
    if isinstance(e, llm.LLMError) and ": http " in str(e):
        return "http_status"
    return f"other: {e!r}"


@pytest.mark.parametrize("v", _cases("media_fetch"), ids=lambda v: v["name"])
def test_media_fetch(v: dict[str, Any], monkeypatch) -> None:
    from hop_top_kit import llm_media

    extra = set(v) - _MEDIA_FETCH_FIELDS
    assert not extra, f"unknown fields {sorted(extra)}"
    kw = {"max_bytes": v["max_bytes"]} if v.get("max_bytes") else {}

    def read(url: str) -> bytes:
        return llm.URLSource(url, **kw).read()

    def check(srv=None) -> None:
        url = v["url"] if srv is None else srv.url + v["url"]
        if v.get("error_kind"):
            with pytest.raises(llm.LLMError) as ei:
                read(url)
            assert _media_kind(ei.value) == v["error_kind"]
            if v.get("message"):
                assert str(ei.value) == v["message"]
        else:
            got = read(url)
            if v.get("want_body"):
                assert got.decode() == v["want_body"]
            if v.get("want_size"):
                assert len(got) == v["want_size"]
        if srv is not None and "attempts" in v:
            assert srv.attempts == v["attempts"]

    s = v.get("server")
    if s is None:
        check()
        return
    assert v["url"].startswith("/"), "a served url is a path"
    real = llm_media.is_public_ip
    monkeypatch.setattr(llm_media, "is_public_ip", lambda ip: ip.is_loopback or real(ip))
    body = "x" * s["body_size"] if s.get("body_size") else s.get("body", "")
    with serve(Canned(s["status"], s.get("headers", {}), body)) as srv:
        check(srv)
