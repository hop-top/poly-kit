"""tool_choice on the openai and routellm adapters, hermetic resolution,
and streams not opted in to events; the anthropic adapter is pinned by
contracts/llm-v1 (test_llm_conformance)."""

from __future__ import annotations

from typing import Any
from unittest import mock

import pytest

from hop_top_kit import llm
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
    "ROUTELLM_BASE_URL",
    "ROUTELLM_STRONG_MODEL",
    "ROUTELLM_WEAK_MODEL",
    "ROUTELLM_ROUTERS",
)

_TOOLS = [
    llm.ToolDef(name="get_weather", description=""),
    llm.ToolDef(name="pane.open", description=""),
]


@pytest.fixture(autouse=True)
def clean_env(monkeypatch, tmp_path):
    for k in _ENV_VARS:
        monkeypatch.delenv(k, raising=False)
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    return tmp_path


def _hi(choice: llm.ToolChoice | None = None) -> llm.Request:
    return llm.Request(messages=[llm.Message(role="user", content="Hi")], tool_choice=choice)


def _completion() -> dict[str, Any]:
    return {
        "id": "x",
        "object": "chat.completion",
        "model": "m",
        "choices": [
            {"index": 0, "message": {"role": "assistant", "content": "ok"}, "finish_reason": "stop"}
        ],
        "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
    }


def _openai(base: str) -> OpenAIAdapter:
    cfg = llm.ResolvedConfig(
        uri=llm.URI(scheme="openai", model="gpt-4o"),
        provider=llm.ProviderConfig(api_key="k", base_url=base, model="gpt-4o"),
    )
    return OpenAIAdapter(cfg, sleep=None)


# ---------------------------------------------------------------------------
# check_tool_choice
# ---------------------------------------------------------------------------


@pytest.mark.parametrize(
    ("choice", "tools", "want"),
    [
        (None, [], None),
        (llm.ToolChoice(llm.ToolChoiceMode.AUTO), _TOOLS, None),
        (llm.ToolChoice("tool", "pane.open"), _TOOLS, None),
        (llm.ToolChoice("auto"), [], 'tool_choice "auto" needs tools'),
        (llm.ToolChoice("required"), _TOOLS, 'unknown tool_choice mode "required"'),
        (llm.ToolChoice("tool"), _TOOLS, 'tool_choice "tool" needs a tool name'),
        (llm.ToolChoice("none", "a"), _TOOLS, 'tool_choice "none" takes no tool name'),
        (llm.ToolChoice("tool", "nope"), _TOOLS, 'tool_choice names unknown tool "nope"'),
    ],
)
def test_check_tool_choice(choice, tools, want) -> None:
    assert llm.check_tool_choice(choice, tools) == want


# ---------------------------------------------------------------------------
# openai
# ---------------------------------------------------------------------------


@pytest.mark.parametrize(
    ("choice", "want"),
    [
        (None, None),
        (llm.ToolChoice("auto"), "auto"),
        (llm.ToolChoice("any"), "required"),
        (llm.ToolChoice("none"), "none"),
        (
            llm.ToolChoice("tool", "pane.open"),
            {"type": "function", "function": {"name": "pane_open"}},
        ),
    ],
)
def test_openai_tool_choice_wire(choice, want) -> None:
    with serve(Canned.json(200, _completion())) as srv:
        _openai(srv.url).call_with_tools(_hi(choice), _TOOLS)
        body = srv.only().json()
    if want is None:
        assert "tool_choice" not in body
    else:
        assert body["tool_choice"] == want


def test_openai_stream_with_tools_sends_tool_choice() -> None:
    sse = (
        'data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\n'
    )
    with serve(Canned.sse(sse)) as srv:
        s = _openai(srv.url).stream_with_tools(_hi(llm.ToolChoice("tool", "get_weather")), _TOOLS)
        s.close()
        body = srv.only().json()
    assert body["tool_choice"] == {"type": "function", "function": {"name": "get_weather"}}


def test_openai_tool_choice_rejected_before_sending() -> None:
    with serve(Canned.json(200, _completion())) as srv:
        a = _openai(srv.url)
        any_ = _hi(llm.ToolChoice("any"))
        with pytest.raises(llm.LLMError, match=r'^openai: tool_choice "any" needs tools$'):
            a.complete(any_)
        with pytest.raises(llm.LLMError, match=r'^openai: tool_choice "any" needs tools$'):
            a.stream(any_)
        with pytest.raises(llm.LLMError, match=r'^openai: tool_choice names unknown tool "nope"$'):
            a.call_with_tools(_hi(llm.ToolChoice("tool", "nope")), _TOOLS)
        with pytest.raises(llm.LLMError, match=r'^openai: unknown tool_choice mode "required"$'):
            a.stream_with_tools(_hi(llm.ToolChoice("required")), _TOOLS)
        assert srv.attempts == 0


# ---------------------------------------------------------------------------
# routellm
# ---------------------------------------------------------------------------


def _routellm(controller: mock.MagicMock):
    from hop_top_kit.routellm_adapter import RouteLLMAdapter

    cfg = llm.ResolvedConfig(
        uri=llm.URI(scheme="routellm", model="mf:0.7"),
        provider=llm.ProviderConfig(extras={}),
    )
    with mock.patch("hop_top_kit.routellm_adapter.Controller", return_value=controller):
        return RouteLLMAdapter(cfg)


def test_routellm_tool_choice_passed_to_controller() -> None:
    controller = mock.MagicMock()
    controller.completion.return_value = _completion()
    a = _routellm(controller)
    a.call_with_tools(_hi(llm.ToolChoice("tool", "pane.open")), _TOOLS)
    kwargs = controller.completion.call_args.kwargs
    assert kwargs["tool_choice"] == {"type": "function", "function": {"name": "pane_open"}}
    a.call_with_tools(_hi(llm.ToolChoice("any")), _TOOLS)
    assert controller.completion.call_args.kwargs["tool_choice"] == "required"
    a.call_with_tools(_hi(), _TOOLS)
    assert "tool_choice" not in controller.completion.call_args.kwargs


def test_routellm_tool_choice_rejected_before_routing() -> None:
    controller = mock.MagicMock()
    a = _routellm(controller)
    with pytest.raises(llm.LLMError, match=r'^routellm: tool_choice "auto" needs tools$'):
        a.complete(_hi(llm.ToolChoice("auto")))
    with pytest.raises(llm.LLMError, match=r'^routellm: tool_choice "auto" needs tools$'):
        list(a.stream(_hi(llm.ToolChoice("auto"))))
    with pytest.raises(llm.LLMError, match=r'^routellm: tool_choice names unknown tool "x"$'):
        a.stream_with_tools(_hi(llm.ToolChoice("tool", "x")), _TOOLS)
    controller.completion.assert_not_called()


# ---------------------------------------------------------------------------
# hermetic
# ---------------------------------------------------------------------------


def _write_yaml(home, text: str) -> None:
    (home / "hop").mkdir(exist_ok=True)
    (home / "hop" / "llm.yaml").write_text(text)


def test_load_config_hermetic_reads_only_the_uri(clean_env, monkeypatch) -> None:
    _write_yaml(
        clean_env,
        "default: openai://gpt-4o\nfallback: [openai://b]\n"
        "providers:\n  anthropic:\n    api_key: file-key\n    base_url: https://gw.corp.test\n",
    )
    monkeypatch.setenv("LLM_API_KEY", "env-key")
    monkeypatch.setenv("ANTHROPIC_API_KEY", "scheme-key")
    monkeypatch.setenv("LLM_BASE_URL", "https://env.test")
    monkeypatch.setenv("LLM_FALLBACK", "openai://c")

    ambient = llm.load_config("anthropic://m")
    assert (ambient.provider.api_key, ambient.provider.base_url) == ("env-key", "https://env.test")
    assert ambient.fallbacks == ["openai://c"]
    assert not ambient.hermetic

    cfg = llm.load_config("anthropic://m", hermetic=True)
    assert cfg.hermetic
    assert (cfg.provider.api_key, cfg.provider.base_url, cfg.fallbacks) == ("", "", [])

    cfg = llm.load_config("anthropic://m?api_key=k&base_url=https://proxy.test", hermetic=True)
    assert (cfg.provider.api_key, cfg.provider.base_url) == ("k", "https://proxy.test")
    cfg = llm.load_config("openai://localhost:8080/m", hermetic=True)
    assert cfg.provider.base_url == "http://localhost:8080"


def test_load_config_hermetic_needs_a_uri(monkeypatch) -> None:
    monkeypatch.setenv("LLM_PROVIDER", "openai://gpt-4o")
    with pytest.raises(llm.LLMError, match="no LLM provider URI"):
        llm.load_config("", hermetic=True)


def test_load_config_hermetic_refuses_a_clear_key(monkeypatch) -> None:
    with pytest.raises(llm.KeyEndpointError):
        llm.load_config("openai://m?api_key=k&base_url=http://remote.test", hermetic=True)


def test_resolve_hermetic_reaches_the_adapter(clean_env, monkeypatch) -> None:
    got: list[llm.ResolvedConfig] = []
    monkeypatch.setitem(llm._registry, "fake", lambda cfg: got.append(cfg) or object())
    llm.resolve("fake://m?api_key=k")
    llm.resolve("fake://m?api_key=k", hermetic=True)
    assert [c.hermetic for c in got] == [False, True]
    assert got[1].provider.api_key == "k", "the URI still counts"


def test_create_llm_hermetic(monkeypatch) -> None:
    got: list[llm.ResolvedConfig] = []
    monkeypatch.setitem(llm._registry, "fake", lambda cfg: got.append(cfg) or object())
    monkeypatch.setenv("LLM_FALLBACK", "fake://env-fallback")
    llm.create_llm("fake://m", hermetic=True)
    assert [(c.uri.model, c.hermetic) for c in got] == [("m", True)], "no ambient fallback"
    got.clear()
    llm.create_llm("fake://m", fallback=["fake://b"], hermetic=True)
    assert [(c.uri.model, c.hermetic) for c in got] == [("m", True), ("b", True)]


def test_anthropic_hermetic_reads_no_config_file(clean_env, monkeypatch) -> None:
    _write_yaml(clean_env, "providers:\n  anthropic:\n    base_url: https://gw.corp.test\n")
    monkeypatch.setenv("ANTHROPIC_AUTH_TOKEN", "env-token")
    monkeypatch.setenv("ANTHROPIC_WORKSPACE_ID", "ws-1")
    uri = "anthropic://m?api_key=k&base_url=https://gw.corp.test"
    from hop_top_kit import llm_config

    reads = mock.Mock(wraps=llm_config._load_yaml_config)
    monkeypatch.setattr(llm_config, "_load_yaml_config", reads)
    ambient = llm.resolve(uri)
    assert reads.call_count > 0, "control: an ambient adapter consults llm.yaml"
    assert "authorization" in ambient.credential_headers()
    reads.reset_mock()
    a = llm.resolve(uri, hermetic=True)
    assert isinstance(a, AnthropicAdapter)
    assert reads.call_count == 0, "hermetic reads no llm.yaml"
    assert a.credential_headers() == ["x-api-key"]


def test_parse_router_config_hermetic_ignores_env(monkeypatch) -> None:
    monkeypatch.setenv("ROUTELLM_BASE_URL", "http://env:1")
    monkeypatch.setenv("ROUTELLM_STRONG_MODEL", "env-strong")
    extras = {"routellm": {"base_url": "http://cfg:1", "strong_model": "cfg-strong"}}
    assert llm.parse_router_config(extras).base_url == "http://env:1", "ambient: env wins"
    cfg = llm.parse_router_config(extras, hermetic=True)
    assert (cfg.base_url, cfg.strong_model) == ("http://cfg:1", "cfg-strong")


# ---------------------------------------------------------------------------
# stream events: only what the provider reports
# ---------------------------------------------------------------------------


def test_openai_stream_events_without_calls_or_usage() -> None:
    """Opted in, a body with no tool calls and no usage chunk yields no
    event token: none is made up."""
    sse = (
        'data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\n'
    )
    with serve(Canned.sse(sse)) as srv:
        req = _hi()
        req.stream_events = True
        toks = list(_openai(srv.url).stream(req))
        sent = srv.only().json()
    assert sent["stream_options"] == {"include_usage": True}
    assert [(t.content, t.done) for t in toks] == [("ok", False), ("", True)]
    assert all(t.tool_call is None and t.usage is None for t in toks)
