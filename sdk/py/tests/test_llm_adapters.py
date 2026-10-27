"""Tests for the hand-rolled anthropic and openai adapters and the shared
transport, beyond what contracts/llm-v1 pins. Every server is loopback."""

from __future__ import annotations

import json
import socket
from typing import Any
from unittest import mock

import pytest

from hop_top_kit import llm, netpolicy
from hop_top_kit.llm_anthropic import AnthropicAdapter
from hop_top_kit.llm_openai import OpenAIAdapter
from tests.llm_fakeserver import Canned, serve

_ENV_VARS = (
    "ANTHROPIC_API_KEY",
    "ANTHROPIC_BASE_URL",
    "ANTHROPIC_AUTH_TOKEN",
    "ANTHROPIC_WORKSPACE_ID",
    "OPENAI_API_KEY",
    "LLM_API_KEY",
    "LLM_BASE_URL",
    "LLM_PROVIDER",
    "LLM_FALLBACK",
)


@pytest.fixture(autouse=True)
def clean_env(monkeypatch, tmp_path):
    for k in _ENV_VARS:
        monkeypatch.delenv(k, raising=False)
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    return tmp_path


def _cfg(scheme: str, base: str, key: str = "k", model: str = "m", **params: str):
    return llm.ResolvedConfig(
        uri=llm.URI(scheme=scheme, model=model, params=dict(params)),
        provider=llm.ProviderConfig(api_key=key, base_url=base, model=model, params=dict(params)),
    )


def _anthropic(base: str, key: str = "k", sleep=None, **params: str) -> AnthropicAdapter:
    return AnthropicAdapter(
        _cfg("anthropic", base, key, **params),
        env=lambda _k: None,
        config_file={},
        sleep=sleep or (lambda _s: None),
    )


def _openai(
    base: str, key: str = "k", sleep=None, scheme: str = "openai", **params: str
) -> OpenAIAdapter:
    return OpenAIAdapter(_cfg(scheme, base, key, **params), sleep=sleep or (lambda _s: None))


def _hi() -> llm.Request:
    return llm.Request(messages=[llm.Message(role="user", content="Hi")])


def _free_port() -> int:
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port


_ANTHROPIC_OK = {
    "id": "msg_1",
    "type": "message",
    "role": "assistant",
    "model": "m",
    "content": [{"type": "text", "text": "ok"}],
    "stop_reason": "end_turn",
    "usage": {"input_tokens": 1, "output_tokens": 1},
}


# ---------------------------------------------------------------------------
# Shared transport
# ---------------------------------------------------------------------------


class TestRetry:
    def test_server_wait_honored(self):
        waits: list[float] = []
        err = Canned.json(429, {"error": {"type": "rate_limit_error", "message": "x"}})
        err.headers["retry-after-ms"] = "250"
        with serve(err, Canned.json(200, _ANTHROPIC_OK)) as srv:
            _anthropic(srv.url, sleep=waits.append).complete(_hi())
            assert srv.attempts == 2
        assert waits == [0.25]

    @pytest.mark.parametrize(
        ("header", "value"),
        [
            ("retry-after", "999999"),
            ("retry-after", "0"),
            ("retry-after", "-1"),
            ("retry-after", "NaN"),
            ("retry-after", "inf"),
            ("retry-after-ms", "60001"),
            ("retry-after", "Wed, 21 Oct 2015 07:28:00 GMT"),
        ],
    )
    def test_out_of_range_wait_backs_off(self, header, value):
        waits: list[float] = []
        err = Canned.json(529, {"error": {"type": "overloaded_error", "message": "x"}})
        err.headers[header] = value
        with serve(err, Canned.json(200, _ANTHROPIC_OK)) as srv:
            _anthropic(srv.url, sleep=waits.append).complete(_hi())
        assert len(waits) == 1
        assert 0 <= waits[0] <= 0.5, waits

    def test_transport_failure_retried_and_fallbackable(self):
        waits: list[float] = []
        a = _anthropic(f"http://127.0.0.1:{_free_port()}", key="sekrit", sleep=waits.append)
        with pytest.raises(llm.TransportError) as ei:
            a.complete(_hi())
        assert len(waits) == 2
        assert llm.is_fallbackable(ei.value)
        assert "sekrit" not in str(ei.value)
        assert "POST http://127.0.0.1:" in str(ei.value)

    def test_offline_refusal_not_retried(self):
        waits: list[float] = []
        a = _anthropic("https://api.anthropic.com", sleep=waits.append)
        netpolicy.set_offline(True)
        with pytest.raises(netpolicy.OfflineError):
            a.complete(_hi())
        assert waits == []

    def test_redirect_not_followed(self):
        with serve(Canned(307, {"location": "http://127.0.0.1:1/x"})) as srv:
            with pytest.raises(llm.HTTPStatusError) as ei:
                _openai(srv.url).complete(_hi())
            assert ei.value.status_code == 307
            assert srv.attempts == 1
        assert not llm.is_fallbackable(ei.value)

    @pytest.mark.parametrize("status", [301, 302, 303, 307, 308])
    def test_no_redirect_status_followed(self, status):
        ok = Canned.json(200, _ANTHROPIC_OK)
        with serve(Canned(status, {"location": "/elsewhere"}), ok) as srv:
            with pytest.raises(llm.HTTPStatusError) as ei:
                _anthropic(srv.url).complete(_hi())
            assert ei.value.status_code == status
            assert srv.attempts == 1

    def test_error_text_scrubbed(self):
        body = {"error": {"type": "invalid_request_error", "message": "bad key sk-zzz"}}
        with serve(Canned.json(400, body)) as srv:
            with pytest.raises(llm.APIError) as ei:
                _openai(srv.url, key="sk-zzz").complete(_hi())
        assert "sk-zzz" not in str(ei.value)
        assert "REDACTED" in str(ei.value)


# ---------------------------------------------------------------------------
# Anthropic extras
# ---------------------------------------------------------------------------


class TestAnthropic:
    def test_key_required(self):
        with pytest.raises(llm.LLMError, match="API key required"):
            _anthropic("https://api.anthropic.com", key=" ")

    def test_key_over_plain_http_to_remote_refused(self):
        with pytest.raises(llm.KeyEndpointError) as ei:
            _anthropic("http://gw.example.test")
        assert ei.value.insecure

    def test_bad_limit_param(self):
        with pytest.raises(llm.LLMError, match="max_body_bytes must be a positive byte count"):
            _anthropic("https://api.anthropic.com", max_body_bytes="-1")

    def test_capabilities_via_client(self):
        c = llm.Client(_anthropic("https://api.anthropic.com"))
        assert c.capabilities() == ["complete", "stream", "call_with_tools", "stream_with_tools"]

    def test_resolve_by_uri(self, monkeypatch):
        with serve(Canned.json(200, _ANTHROPIC_OK)) as srv:
            p = llm.resolve(f"anthropic://m?api_key=fake&base_url={srv.url}")
            assert isinstance(p, AnthropicAdapter)
            assert p.complete(_hi()).content == "ok"
            assert srv.only().headers["x-api-key"] == "fake"

    def test_env_key_by_scheme(self, monkeypatch):
        monkeypatch.setenv("ANTHROPIC_API_KEY", "from-env")
        assert llm.load_config("anthropic://m").provider.api_key == "from-env"

    def test_stream_close_stops(self):
        sse = (
            'event: message_start\ndata: {"type":"message_start","message":{"usage":{}}}\n\n'
            'event: content_block_start\ndata: {"type":"content_block_start","index":0,'
            '"content_block":{"type":"text","text":""}}\n\n'
            'event: content_block_delta\ndata: {"type":"content_block_delta","index":0,'
            '"delta":{"type":"text_delta","text":"a"}}\n\n'
        )
        with serve(Canned.sse(sse)) as srv:
            s = _anthropic(srv.url).stream(_hi())
            assert next(s).content == "a"
            s.close()
            assert list(s) == []
            assert s.outcome() is None


# ---------------------------------------------------------------------------
# OpenAI
# ---------------------------------------------------------------------------


def _completion(message: dict[str, Any], finish: str = "stop") -> dict[str, Any]:
    return {
        "choices": [{"index": 0, "message": message, "finish_reason": finish}],
        "usage": {"prompt_tokens": 3, "completion_tokens": 2, "total_tokens": 5},
    }


def _sse(*chunks: Any, done: bool = True) -> str:
    out = "".join(f"data: {json.dumps(c)}\n\n" for c in chunks)
    return out + ("data: [DONE]\n\n" if done else "")


class TestOpenAI:
    def test_complete_body_and_response(self):
        body = _completion({"role": "assistant", "content": "hi"})
        with serve(Canned.json(200, body)) as srv:
            req = llm.Request(
                messages=[
                    llm.Message(role="system", content="s"),
                    llm.Message(role="user", content="u"),
                ],
                temperature=0.0,
                max_tokens=7,
                stop_sequences=["x"],
            )
            r = _openai(srv.url).complete(req)
            got = srv.only()
        assert got.path == "/chat/completions"
        assert got.headers["authorization"] == "Bearer k"
        assert got.json() == {
            "model": "m",
            "messages": [{"role": "system", "content": "s"}, {"role": "user", "content": "u"}],
            "temperature": 0.0,
            "max_tokens": 7,
            "stop": ["x"],
        }
        assert (r.content, r.role, r.finish_reason) == ("hi", "assistant", "stop")
        assert r.usage == llm.Usage(prompt_tokens=3, completion_tokens=2, total_tokens=5)

    def test_blank_key_sends_no_authorization(self):
        with serve(Canned.json(200, _completion({"content": "x"}))) as srv:
            _openai(srv.url, key="").complete(_hi())
            assert "authorization" not in srv.only().headers

    def test_tools_sanitized_and_restored(self):
        body = _completion(
            {
                "role": "assistant",
                "tool_calls": [
                    {
                        "id": "c1",
                        "type": "function",
                        "function": {"name": "pane_open", "arguments": '{"a":1}'},
                    }
                ],
            },
            finish="tool_calls",
        )
        history = [
            llm.Message(role="user", content="go"),
            llm.Message(role="assistant", tool_calls=[llm.ToolCall(id="c0", name="pane.open")]),
            llm.Message(role="tool", content="done", tool_call_id="c0"),
        ]
        tools = [
            llm.ToolDef(
                name="pane.open",
                description="opens",
                parameters='{"type":"object","properties":{"a":{"type":"integer"}}}',
            )
        ]
        with serve(Canned.json(200, body)) as srv:
            r = _openai(srv.url).call_with_tools(llm.Request(messages=history), tools)
            sent = srv.only().json()
        assert sent["tools"] == [
            {
                "type": "function",
                "function": {
                    "name": "pane_open",
                    "description": "opens",
                    "parameters": {"type": "object", "properties": {"a": {"type": "integer"}}},
                },
            }
        ]
        assert sent["messages"][1]["tool_calls"][0]["function"] == {
            "name": "pane_open",
            "arguments": "{}",
        }
        assert sent["messages"][2] == {"role": "tool", "content": "done", "tool_call_id": "c0"}
        assert [(c.id, c.name, c.arguments) for c in r.tool_calls] == [
            ("c1", "pane.open", '{"a":1}')
        ]
        assert r.finish_reason == "tool_calls"

    def test_strict_rejects_before_sending(self):
        with serve(Canned.json(200, _completion({"content": "x"}))) as srv:
            a = _openai(srv.url, tool_names="strict")
            with pytest.raises(llm.ToolNameError) as ei:
                a.call_with_tools(_hi(), [llm.ToolDef(name="pane.open", description="")])
            assert ei.value.reason == "invalid"
            assert srv.attempts == 0

    def test_stream_tokens_then_done(self):
        sse = _sse(
            {"choices": [{"delta": {"role": "assistant", "content": "He"}}]},
            {"choices": [{"delta": {"content": "llo"}, "finish_reason": "stop"}]},
        )
        with serve(Canned.sse(sse)) as srv:
            s = _openai(srv.url).stream(_hi())
            toks = list(s)
            assert srv.only().json()["stream"] is True
        assert [t.content for t in toks] == ["He", "llo", ""]
        assert toks[-1].done
        assert s.outcome() is not None
        assert s.outcome().content == "Hello"

    def test_stream_with_tools_accumulates_calls(self):
        sse = _sse(
            {
                "choices": [
                    {
                        "delta": {
                            "tool_calls": [
                                {
                                    "index": 0,
                                    "id": "c1",
                                    "type": "function",
                                    "function": {"name": "files_read", "arguments": '{"p'},
                                }
                            ]
                        }
                    }
                ]
            },
            {
                "choices": [
                    {"delta": {"tool_calls": [{"index": 0, "function": {"arguments": '":1}'}}]}}
                ]
            },
            {"choices": [{"delta": {}, "finish_reason": "tool_calls"}]},
            {
                "choices": [],
                "usage": {"prompt_tokens": 4, "completion_tokens": 6, "total_tokens": 10},
            },
        )
        with serve(Canned.sse(sse)) as srv:
            s = _openai(srv.url).stream_with_tools(
                _hi(), [llm.ToolDef(name="files.read", description="")]
            )
            assert [t.done for t in s] == [True]
            sent = srv.only().json()
        assert sent["tools"][0]["function"]["name"] == "files_read"
        out = s.outcome()
        assert out is not None
        assert [(c.id, c.name, c.arguments) for c in out.tool_calls] == [
            ("c1", "files.read", '{"p":1}')
        ]
        assert out.finish_reason == "tool_calls"
        assert out.usage.total_tokens == 10

    def test_stream_without_terminal_is_truncated(self):
        sse = _sse({"choices": [{"delta": {"content": "par"}}]}, done=False)
        with serve(Canned.sse(sse)) as srv:
            s = _openai(srv.url).stream(_hi())
            assert next(s).content == "par"
            with pytest.raises(llm.StreamTruncatedError):
                next(s)
        assert s.outcome() is None
        assert list(s) == []

    def test_stream_event_too_large(self):
        sse = _sse({"choices": [{"delta": {"content": "x" * 4096}}]})
        with serve(Canned.sse(sse)) as srv:
            s = _openai(srv.url, max_event_bytes="1024").stream(_hi())
            with pytest.raises(llm.ResponseTooLargeError) as ei:
                list(s)
        assert ei.value.what == "event"

    def test_body_too_large(self):
        body = _completion({"content": "x" * 4096})
        with serve(Canned.json(200, body)) as srv:
            with pytest.raises(llm.ResponseTooLargeError) as ei:
                _openai(srv.url, max_body_bytes="1024").complete(_hi())
        assert ei.value.what == "body"

    def test_stream_error_chunk(self):
        sse = "data: " + json.dumps({"error": {"type": "server_error", "message": "boom"}}) + "\n\n"
        with serve(Canned.sse(sse)) as srv:
            with pytest.raises(llm.LLMError, match="boom"):
                list(_openai(srv.url).stream(_hi()))

    @pytest.mark.parametrize(
        ("status", "kind"),
        [
            (401, llm.AuthError),
            (403, llm.AuthError),
            (404, llm.ModelError),
            (429, llm.RateLimitError),
            (503, llm.HTTPStatusError),
            (400, llm.APIError),
        ],
    )
    def test_error_mapping(self, status, kind):
        body = {"error": {"type": "x", "message": "y"}}
        with serve(Canned.json(status, body)) as srv:
            with pytest.raises(kind):
                _openai(srv.url).complete(_hi())
            retried = status in (429, 503)
            assert srv.attempts == (3 if retried else 1)

    def test_lmstudio_appends_v1(self):
        with serve(Canned.json(200, _completion({"content": "x"}))) as srv:
            _openai(srv.url, key="", scheme="lmstudio").complete(_hi())
            assert srv.only().path == "/v1/chat/completions"

    def test_openai_host_form_appends_v1(self):
        with serve(Canned.json(200, _completion({"content": "x"}))) as srv:
            host = srv.url.removeprefix("http://")
            p = llm.resolve(f"openai://{host}/m")
            p.complete(_hi())
            assert srv.only().path == "/v1/chat/completions"

    def test_base_url_used_as_given(self):
        with serve(Canned.json(200, _completion({"content": "x"}))) as srv:
            _openai(srv.url + "/gw").complete(_hi())
            assert srv.only().path == "/gw/chat/completions"

    def test_key_over_plain_http_to_remote_refused(self):
        with pytest.raises(llm.KeyEndpointError):
            _openai("http://gw.example.test/v1")

    @pytest.mark.parametrize(
        "scheme",
        [
            "openai",
            "openrouter",
            "xai",
            "lmstudio",
            "groq",
            "together",
            "fireworks",
            "deepseek",
            "mistral",
        ],
    )
    def test_schemes_registered(self, scheme):
        assert llm.declared_endpoints(scheme)
        assert scheme in llm._registry


# ---------------------------------------------------------------------------
# RouteLLM tool calling (OpenAI wire through the Controller)
# ---------------------------------------------------------------------------


def _routellm(controller: mock.MagicMock, **params: str):
    from hop_top_kit.routellm_adapter import RouteLLMAdapter

    cfg = llm.ResolvedConfig(
        uri=llm.URI(scheme="routellm", model="mf:0.7", params=dict(params)),
        provider=llm.ProviderConfig(params=dict(params), extras={}),
    )
    with mock.patch("hop_top_kit.routellm_adapter.Controller", return_value=controller):
        return RouteLLMAdapter(cfg)


class TestRouteLLMTools:
    def test_call_with_tools(self):
        controller = mock.MagicMock()
        controller.completion.return_value = _completion(
            {
                "role": "assistant",
                "content": None,
                "tool_calls": [
                    {
                        "id": "c1",
                        "type": "function",
                        "function": {"name": "pane_open", "arguments": "{}"},
                    }
                ],
            },
            finish="tool_calls",
        )
        a = _routellm(controller)
        r = a.call_with_tools(_hi(), [llm.ToolDef(name="pane.open", description="")])
        kwargs = controller.completion.call_args.kwargs
        assert kwargs["tools"][0]["function"]["name"] == "pane_open"
        assert (kwargs["router"], kwargs["threshold"]) == ("mf", 0.7)
        assert [(c.id, c.name) for c in r.tool_calls] == [("c1", "pane.open")]
        assert r.usage == llm.Usage(prompt_tokens=3, completion_tokens=2, total_tokens=5)
        assert "call_with_tools" in llm.Client(a).capabilities()

    def test_stream_with_tools(self):
        controller = mock.MagicMock()
        controller.completion.return_value = iter(
            [
                {"choices": [{"delta": {"content": "ok"}}]},
                {
                    "choices": [
                        {
                            "delta": {
                                "tool_calls": [
                                    {
                                        "index": 0,
                                        "id": "c1",
                                        "function": {"name": "pane_open", "arguments": "{}"},
                                    }
                                ]
                            },
                            "finish_reason": "tool_calls",
                        }
                    ]
                },
            ]
        )
        a = _routellm(controller)
        s = a.stream_with_tools(_hi(), [llm.ToolDef(name="pane.open", description="")])
        toks = list(s)
        assert [t.content for t in toks] == ["ok", ""]
        assert toks[-1].done
        assert controller.completion.call_args.kwargs["stream"] is True
        out = s.outcome()
        assert out is not None
        assert [(c.id, c.name) for c in out.tool_calls] == [("c1", "pane.open")]

    def test_stream_with_tools_truncated(self):
        controller = mock.MagicMock()
        controller.completion.return_value = iter([{"choices": [{"delta": {"content": "x"}}]}])
        s = _routellm(controller).stream_with_tools(_hi(), [])
        assert next(s).content == "x"
        with pytest.raises(llm.StreamTruncatedError):
            next(s)

    def test_strict_rejects_before_calling(self):
        controller = mock.MagicMock()
        a = _routellm(controller, tool_names="strict")
        with pytest.raises(llm.ToolNameError):
            a.call_with_tools(_hi(), [llm.ToolDef(name="pane.open", description="")])
        controller.completion.assert_not_called()


# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------


class TestConfig:
    def test_uri_key_and_base_url(self):
        cfg = llm.load_config("openai://m?api_key=inline&base_url=https://gw.example.test/v1")
        assert cfg.provider.api_key == "inline"
        assert cfg.provider.base_url == "https://gw.example.test/v1"

    def test_env_key_not_sent_to_uri_named_host(self, monkeypatch):
        monkeypatch.setenv("OPENAI_API_KEY", "env-key")
        with pytest.raises(llm.KeyEndpointError):
            llm.load_config("openai://m?base_url=https://evil.example.test")

    def test_env_key_to_declared_endpoint(self, monkeypatch):
        monkeypatch.setenv("OPENAI_API_KEY", "env-key")
        cfg = llm.load_config("openai://m?base_url=https://api.openai.com/v1")
        assert cfg.provider.api_key == "env-key"

    def test_usage_cost(self):
        u = llm.Usage(prompt_tokens=1_000_000, completion_tokens=2_000_000)
        assert u.cost(llm.Price(input=3.0, output=15.0)) == pytest.approx(33.0)
