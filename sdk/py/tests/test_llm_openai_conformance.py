"""llm-v1 conformance runner for the Python openai adapter.

Runs the ``openai_*`` sections of ``contracts/llm-v1/vectors.json``
against :class:`~hop_top_kit.llm_openai.OpenAIAdapter` on a loopback
server; ``tests/test_llm_conformance.py`` runs the rest and counts these
sections consumed (:data:`SECTIONS`). Vectors this port does not pass
yet are listed in :data:`PENDING` with the reason; any other failure
means this port drifted from Go. Strict: an unknown vector field fails.
"""

from __future__ import annotations

import json
from typing import Any

import pytest

from hop_top_kit import llm
from hop_top_kit.llm_openai import OpenAIAdapter
from tests.llm_fakeserver import Canned, serve
from tests.test_llm_conformance import _VECTORS, _drain, _event_json, _kind, _numeric

SECTIONS = {
    "openai_request",
    "openai_stream",
    "openai_stream_events",
    "openai_accumulate",
    "openai_error",
    "openai_limits",
}

# Vectors not run here, by section/name, with why.
PENDING: dict[str, str] = {}

_FIELDS = {
    "openai_request": {"name", "call", "adapter_model", "request", "tools", "want_body"},
    "openai_stream": {"name", "sse", "want_tokens", "error_kind"},
    "openai_stream_events": {
        "name",
        "sse",
        "sse_from",
        "tools",
        "stream_events",
        "want_events",
        "error_kind",
    },
    "openai_accumulate": {"name", "sse", "sse_from", "tools", "want"},
    "openai_error": {
        "name",
        "responses",
        "want_kind",
        "want_status",
        "fallbackable",
        "attempts",
        "stream",
    },
    "openai_limits": {"name", "params", "stream", "content_type", "body", "too_large"},
}

CONF_KEY = "conf-key-must-not-leak"


def _cases(section: str) -> list[Any]:
    vs = _VECTORS[section]
    assert vs, f"{section} is empty"
    out = []
    for v in vs:
        extra = set(v) - _FIELDS[section]
        assert not extra, f"{section}/{v['name']}: unknown fields {sorted(extra)}"
        why = PENDING.get(f"{section}/{v['name']}")
        marks = [pytest.mark.skip(reason=f"pending: {why}")] if why else []
        out.append(pytest.param(v, id=v["name"], marks=marks))
    return out


def test_runs_every_openai_section() -> None:
    assert {k for k in _VECTORS if k.startswith("openai_")} == SECTIONS


def test_pending_vectors_exist() -> None:
    names = {f"{s}/{v['name']}" for s in SECTIONS for v in _VECTORS[s]}
    assert set(PENDING) <= names, sorted(set(PENDING) - names)


def _no_sleep(_: float) -> None:
    pass


def _adapter(base: str, model: str = "m", *, params: dict[str, str] | None = None, sleep=_no_sleep):
    cfg = llm.ResolvedConfig(
        uri=llm.URI(scheme="openai", model=model),
        provider=llm.ProviderConfig(
            api_key=CONF_KEY, base_url=base, model=model, params=dict(params or {})
        ),
    )
    return OpenAIAdapter(cfg, sleep=sleep, max_retries=2)


def _request(r: dict[str, Any]) -> llm.Request:
    tc = r.get("tool_choice")
    return llm.Request(
        messages=[
            llm.Message(
                role=m["role"],
                content=m.get("content", ""),
                tool_calls=[
                    llm.ToolCall(id=c["id"], name=c["name"], arguments=c.get("arguments", ""))
                    for c in m.get("tool_calls", [])
                ],
                tool_call_id=m.get("tool_call_id", ""),
            )
            for m in r["messages"]
        ],
        model=r.get("model", ""),
        temperature=r.get("temperature"),
        max_tokens=r.get("max_tokens", 0),
        stop_sequences=r.get("stop_sequences", []),
        tool_choice=llm.ToolChoice(tc["mode"], tc.get("name", "")) if tc else None,
        stream_events=r.get("stream_events", False),
    )


def _tools(ts: list[dict[str, Any]]) -> list[llm.ToolDef]:
    return [
        llm.ToolDef(
            name=t["name"],
            description=t.get("description", ""),
            parameters=t.get("parameters") or None,
        )
        for t in ts
    ]


def _named(names: list[str]) -> list[llm.ToolDef]:
    return [llm.ToolDef(name=n, description="") for n in names]


def _hi(stream_events: bool = False) -> llm.Request:
    return llm.Request(
        messages=[llm.Message(role="user", content="Hi")], stream_events=stream_events
    )


def _sse(v: dict[str, Any]) -> str:
    if not v.get("sse_from"):
        return v["sse"]
    streams = {f"openai_stream/{s['name']}": s["sse"] for s in _VECTORS["openai_stream"]}
    return streams[v["sse_from"]]


_OK_COMPLETION = {
    "id": "c",
    "object": "chat.completion",
    "choices": [
        {"index": 0, "message": {"role": "assistant", "content": "ok"}, "finish_reason": "stop"}
    ],
}
_OK_SSE = 'data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\n'


@pytest.mark.parametrize("v", _cases("openai_request"))
def test_openai_request(v: dict[str, Any]) -> None:
    streamed = v["call"] in ("stream", "stream_tools")
    canned = Canned.sse(_OK_SSE) if streamed else Canned.json(200, _OK_COMPLETION)
    with serve(canned) as srv:
        a = _adapter(srv.url, v["adapter_model"])
        req = _request(v["request"])
        call = v["call"]
        if call == "complete":
            a.complete(req)
        elif call == "tools":
            a.call_with_tools(req, _tools(v["tools"]))
        elif call == "stream":
            _drain(a.stream(req))
        elif call == "stream_tools":
            _drain(a.stream_with_tools(req, _tools(v["tools"])))
        else:
            pytest.fail(f"unknown call {call!r}")
        got = srv.only()
    assert got.path == "/chat/completions"
    assert _numeric(got.json()) == _numeric(v["want_body"])


@pytest.mark.parametrize("v", _cases("openai_stream"))
def test_openai_stream(v: dict[str, Any]) -> None:
    with serve(Canned.sse(v["sse"])) as srv:
        s = _adapter(srv.url).stream(_hi())
        tokens: list[str] = []
        err: BaseException | None = None
        try:
            for t in s:
                if t.done:
                    assert next(s, None) is None, "next after done is end"
                    break
                tokens.append(t.content)
        except llm.LLMError as e:
            err = e
    assert tokens == v["want_tokens"]
    assert (_kind(err)[0] if err else "") == v["error_kind"], err
    if err is not None:
        assert CONF_KEY not in str(err)


@pytest.mark.parametrize("v", _cases("openai_stream_events"))
def test_openai_stream_events(v: dict[str, Any]) -> None:
    req = _hi(v["stream_events"])
    got: list[dict[str, Any]] = []
    err: BaseException | None = None
    with serve(Canned.sse(_sse(v))) as srv:
        a = _adapter(srv.url)
        s = a.stream_with_tools(req, _named(v["tools"])) if v["tools"] else a.stream(req)
        try:
            for t in s:
                got.append(_event_json(t))
                if t.done:
                    assert next(s, None) is None, "next after done is end"
                    break
        except llm.LLMError as e:
            err = e
    assert got == v["want_events"]
    assert (_kind(err)[0] if err else "") == v["error_kind"], err


@pytest.mark.parametrize("v", _cases("openai_accumulate"))
def test_openai_accumulate(v: dict[str, Any]) -> None:
    w = v["want"]
    with serve(Canned.sse(_sse(v))) as srv:
        s = _adapter(srv.url).stream_with_tools(_hi(), _named(v["tools"]))
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
    assert out.finish_reason == w["finish_reason"]
    u = w["usage"]
    assert (out.usage.prompt_tokens, out.usage.completion_tokens, out.usage.total_tokens) == (
        u["prompt_tokens"],
        u["completion_tokens"],
        u["total_tokens"],
    )
    assert [(c.id, c.name, json.loads(c.arguments)) for c in out.tool_calls] == [
        (c["id"], c["name"], c["arguments"]) for c in w["tool_calls"]
    ]


@pytest.mark.parametrize("v", _cases("openai_error"))
def test_openai_error(v: dict[str, Any]) -> None:
    canned = []
    for r in v["responses"]:
        headers = dict(r["headers"])
        headers.setdefault("content-type", "application/json")
        canned.append(Canned(r["status"], headers, r["body"]))
    with serve(*canned) as srv:
        a = _adapter(srv.url)
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
    kind, status = _kind(err) if err else ("", 0)
    assert kind == v["want_kind"], err
    assert status == v["want_status"]
    assert (err is not None and llm.is_fallbackable(err)) == v["fallbackable"]
    assert attempts == v["attempts"]


@pytest.mark.parametrize("v", _cases("openai_limits"))
def test_openai_limits(v: dict[str, Any]) -> None:
    with serve(Canned(200, {"content-type": v["content_type"]}, v["body"])) as srv:
        a = _adapter(srv.url, params=v["params"])
        err: BaseException | None = None
        try:
            if v["stream"]:
                _drain(a.stream(_hi()))
            else:
                a.complete(_hi())
        except llm.LLMError as e:
            err = e
        attempts = srv.attempts
    assert attempts == 1, "an overrun is not retried"
    if v["too_large"]:
        assert isinstance(err, llm.ResponseTooLargeError), err
        assert not llm.is_fallbackable(err)
        return
    assert err is None, err
