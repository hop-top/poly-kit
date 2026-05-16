"""Tests for hop_top_kit.llm — LLM provider abstraction layer."""

from __future__ import annotations

import pytest

from hop_top_kit import llm

# ---------------------------------------------------------------------------
# parse_uri
# ---------------------------------------------------------------------------


class TestParseUri:
    def test_basic(self):
        uri = llm.parse_uri("openai://gpt-4")
        assert uri.scheme == "openai"
        assert uri.model == "gpt-4"
        assert uri.host == ""
        assert uri.params == {}

    def test_with_params(self):
        uri = llm.parse_uri("openai://gpt-4?temperature=0.5&max_tokens=100")
        assert uri.scheme == "openai"
        assert uri.model == "gpt-4"
        assert uri.params == {"temperature": "0.5", "max_tokens": "100"}

    def test_with_host_port(self):
        uri = llm.parse_uri("ollama://localhost:11434/llama3")
        assert uri.scheme == "ollama"
        assert uri.host == "localhost:11434"
        assert uri.model == "llama3"

    def test_slashes_in_model(self):
        uri = llm.parse_uri("openai://org/model-name")
        assert uri.scheme == "openai"
        assert uri.model == "org/model-name"

    def test_host_with_slashes_in_model(self):
        uri = llm.parse_uri("ollama://localhost:11434/org/model")
        assert uri.scheme == "ollama"
        assert uri.host == "localhost:11434"
        assert uri.model == "org/model"

    def test_invalid_no_scheme(self):
        with pytest.raises(llm.LLMError):
            llm.parse_uri("gpt-4")

    def test_invalid_empty(self):
        with pytest.raises(llm.LLMError):
            llm.parse_uri("")


# ---------------------------------------------------------------------------
# load_config — env vars
# ---------------------------------------------------------------------------


class TestLoadConfig:
    def test_env_provider_and_key(self, monkeypatch, tmp_path):
        monkeypatch.setenv("LLM_PROVIDER", "anthropic://claude-3")
        monkeypatch.setenv("LLM_API_KEY", "sk-test-123")
        monkeypatch.setenv("LLM_BASE_URL", "")
        monkeypatch.setenv("LLM_FALLBACK", "")
        # Prevent reading real config
        monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))

        cfg = llm.load_config()
        assert cfg.uri.scheme == "anthropic"
        assert cfg.uri.model == "claude-3"
        assert cfg.provider.api_key == "sk-test-123"

    def test_env_fallback(self, monkeypatch, tmp_path):
        monkeypatch.setenv("LLM_PROVIDER", "openai://gpt-4")
        monkeypatch.setenv("LLM_API_KEY", "")
        monkeypatch.setenv("LLM_BASE_URL", "")
        monkeypatch.setenv("LLM_FALLBACK", "anthropic://claude-3,ollama://llama3")
        monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))

        cfg = llm.load_config()
        assert cfg.fallbacks == [
            "anthropic://claude-3",
            "ollama://llama3",
        ]

    def test_env_base_url(self, monkeypatch, tmp_path):
        monkeypatch.setenv("LLM_PROVIDER", "openai://gpt-4")
        monkeypatch.setenv("LLM_API_KEY", "")
        monkeypatch.setenv("LLM_BASE_URL", "https://my-proxy.example.com")
        monkeypatch.setenv("LLM_FALLBACK", "")
        monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))

        cfg = llm.load_config()
        assert cfg.provider.base_url == "https://my-proxy.example.com"

    def test_explicit_uri_overrides_env(self, monkeypatch, tmp_path):
        monkeypatch.setenv("LLM_PROVIDER", "openai://gpt-4")
        monkeypatch.setenv("LLM_API_KEY", "")
        monkeypatch.setenv("LLM_BASE_URL", "")
        monkeypatch.setenv("LLM_FALLBACK", "")
        monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))

        cfg = llm.load_config("anthropic://claude-3")
        assert cfg.uri.scheme == "anthropic"
        assert cfg.uri.model == "claude-3"

    def test_yaml_config(self, monkeypatch, tmp_path):
        monkeypatch.delenv("LLM_PROVIDER", raising=False)
        monkeypatch.delenv("LLM_API_KEY", raising=False)
        monkeypatch.delenv("LLM_BASE_URL", raising=False)
        monkeypatch.delenv("LLM_FALLBACK", raising=False)
        monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))

        cfg_dir = tmp_path / "hop"
        cfg_dir.mkdir()
        (cfg_dir / "llm.yaml").write_text(
            "default: openai://gpt-4\n"
            "providers:\n"
            "  openai:\n"
            "    api_key: sk-yaml\n"
            "    base_url: https://yaml-proxy\n"
            "fallback:\n"
            "  - anthropic://claude-3\n"
        )

        cfg = llm.load_config()
        assert cfg.uri.scheme == "openai"
        assert cfg.uri.model == "gpt-4"
        assert cfg.provider.api_key == "sk-yaml"
        assert cfg.provider.base_url == "https://yaml-proxy"
        assert cfg.fallbacks == ["anthropic://claude-3"]


# ---------------------------------------------------------------------------
# Registry
# ---------------------------------------------------------------------------


class _MockProvider:
    """Minimal provider for testing."""

    def __init__(self, name: str = "mock"):
        self.name = name
        self.closed = False

    def complete(self, req: llm.Request) -> llm.Response:
        return llm.Response(content=f"mock:{req.model}")

    def close(self) -> None:
        self.closed = True


def _mock_factory(cfg: llm.ResolvedConfig) -> _MockProvider:
    return _MockProvider(cfg.uri.scheme)


class TestRegistry:
    def setup_method(self):
        llm._registry.clear()

    def test_register_and_resolve(self, monkeypatch, tmp_path):
        monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
        monkeypatch.delenv("LLM_PROVIDER", raising=False)
        monkeypatch.delenv("LLM_API_KEY", raising=False)
        monkeypatch.delenv("LLM_BASE_URL", raising=False)
        monkeypatch.delenv("LLM_FALLBACK", raising=False)

        llm.register("mock", _mock_factory)
        provider = llm.resolve("mock://test-model")
        assert isinstance(provider, _MockProvider)
        assert provider.name == "mock"

    def test_unknown_scheme(self, monkeypatch, tmp_path):
        monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
        monkeypatch.delenv("LLM_PROVIDER", raising=False)
        monkeypatch.delenv("LLM_API_KEY", raising=False)
        monkeypatch.delenv("LLM_BASE_URL", raising=False)
        monkeypatch.delenv("LLM_FALLBACK", raising=False)

        with pytest.raises(llm.ProviderNotFoundError):
            llm.resolve("unknown://model")

    def test_duplicate_raises(self):
        llm.register("dup", _mock_factory)
        with pytest.raises(llm.LLMError):
            llm.register("dup", _mock_factory)


# ---------------------------------------------------------------------------
# Client — complete delegates, stream unsupported, capabilities
# ---------------------------------------------------------------------------


class TestClient:
    def setup_method(self):
        llm._registry.clear()
        llm.register("mock", _mock_factory)

    def test_complete_delegates(self, monkeypatch, tmp_path):
        monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
        monkeypatch.delenv("LLM_PROVIDER", raising=False)
        monkeypatch.delenv("LLM_API_KEY", raising=False)
        monkeypatch.delenv("LLM_BASE_URL", raising=False)
        monkeypatch.delenv("LLM_FALLBACK", raising=False)

        client = llm.create_llm("mock://gpt-test")
        req = llm.Request(
            messages=[llm.Message(role="user", content="hi")],
            model="gpt-test",
        )
        resp = client.complete(req)
        assert resp.content == "mock:gpt-test"

    def test_stream_unsupported(self, monkeypatch, tmp_path):
        monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
        monkeypatch.delenv("LLM_PROVIDER", raising=False)
        monkeypatch.delenv("LLM_API_KEY", raising=False)
        monkeypatch.delenv("LLM_BASE_URL", raising=False)
        monkeypatch.delenv("LLM_FALLBACK", raising=False)

        client = llm.create_llm("mock://gpt-test")
        req = llm.Request(
            messages=[llm.Message(role="user", content="hi")],
            model="gpt-test",
        )
        with pytest.raises(llm.CapabilityNotSupportedError):
            list(client.stream(req))

    def test_capabilities(self, monkeypatch, tmp_path):
        monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
        monkeypatch.delenv("LLM_PROVIDER", raising=False)
        monkeypatch.delenv("LLM_API_KEY", raising=False)
        monkeypatch.delenv("LLM_BASE_URL", raising=False)
        monkeypatch.delenv("LLM_FALLBACK", raising=False)

        client = llm.create_llm("mock://gpt-test")
        caps = client.capabilities()
        assert "complete" in caps
        # _MockProvider has no stream method matching Streamer protocol
        assert "stream" not in caps
        assert "call_with_tools" not in caps

    def test_provider_accessor(self, monkeypatch, tmp_path):
        monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
        monkeypatch.delenv("LLM_PROVIDER", raising=False)
        monkeypatch.delenv("LLM_API_KEY", raising=False)
        monkeypatch.delenv("LLM_BASE_URL", raising=False)
        monkeypatch.delenv("LLM_FALLBACK", raising=False)

        client = llm.create_llm("mock://gpt-test")
        assert isinstance(client.provider, _MockProvider)


# ---------------------------------------------------------------------------
# Fallback
# ---------------------------------------------------------------------------


class _FailProvider:
    """Provider that always raises RateLimitError."""

    def complete(self, req: llm.Request) -> llm.Response:
        raise llm.RateLimitError("mock", retry_after=1.0)

    def close(self) -> None:
        pass


def _fail_factory(cfg: llm.ResolvedConfig) -> _FailProvider:
    return _FailProvider()


class _OkProvider:
    """Provider that always succeeds."""

    def complete(self, req: llm.Request) -> llm.Response:
        return llm.Response(content="ok-fallback")

    def close(self) -> None:
        pass


def _ok_factory(cfg: llm.ResolvedConfig) -> _OkProvider:
    return _OkProvider()


class TestFallback:
    def setup_method(self):
        llm._registry.clear()
        llm.register("fail", _fail_factory)
        llm.register("ok", _ok_factory)

    def _env(self, monkeypatch, tmp_path):
        monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
        monkeypatch.delenv("LLM_PROVIDER", raising=False)
        monkeypatch.delenv("LLM_API_KEY", raising=False)
        monkeypatch.delenv("LLM_BASE_URL", raising=False)
        monkeypatch.delenv("LLM_FALLBACK", raising=False)

    def test_fallbackable_triggers_next(self, monkeypatch, tmp_path):
        self._env(monkeypatch, tmp_path)
        client = llm.create_llm(
            "fail://model",
            fallback=["ok://backup"],
        )
        req = llm.Request(
            messages=[llm.Message(role="user", content="hi")],
        )
        resp = client.complete(req)
        assert resp.content == "ok-fallback"

    def test_non_fallbackable_does_not(self, monkeypatch, tmp_path):
        self._env(monkeypatch, tmp_path)

        class _AuthFailProvider:
            def complete(self, req: llm.Request) -> llm.Response:
                raise llm.AuthError("fail")

            def close(self) -> None:
                pass

        def _auth_fail_factory(cfg: llm.ResolvedConfig) -> _AuthFailProvider:
            return _AuthFailProvider()

        llm.register("authfail", _auth_fail_factory)

        client = llm.create_llm(
            "authfail://model",
            fallback=["ok://backup"],
        )
        req = llm.Request(
            messages=[llm.Message(role="user", content="hi")],
        )
        with pytest.raises(llm.AuthError):
            client.complete(req)

    def test_all_exhausted(self, monkeypatch, tmp_path):
        self._env(monkeypatch, tmp_path)
        client = llm.create_llm(
            "fail://m1",
            fallback=["fail://m2"],
        )
        req = llm.Request(
            messages=[llm.Message(role="user", content="hi")],
        )
        with pytest.raises(llm.FallbackExhaustedError):
            client.complete(req)


# ---------------------------------------------------------------------------
# Event hooks
# ---------------------------------------------------------------------------


class TestEventHooks:
    def setup_method(self):
        llm._registry.clear()
        llm.register("mock", _mock_factory)

    def test_on_request_fires(self, monkeypatch, tmp_path):
        monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
        monkeypatch.delenv("LLM_PROVIDER", raising=False)
        monkeypatch.delenv("LLM_API_KEY", raising=False)
        monkeypatch.delenv("LLM_BASE_URL", raising=False)
        monkeypatch.delenv("LLM_FALLBACK", raising=False)

        captured: list[llm.Request] = []
        client = llm.create_llm("mock://m", on_request=captured.append)
        req = llm.Request(
            messages=[llm.Message(role="user", content="hi")],
            model="m",
        )
        client.complete(req)
        assert len(captured) == 1
        assert captured[0] is req

    def test_on_response_fires(self, monkeypatch, tmp_path):
        monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
        monkeypatch.delenv("LLM_PROVIDER", raising=False)
        monkeypatch.delenv("LLM_API_KEY", raising=False)
        monkeypatch.delenv("LLM_BASE_URL", raising=False)
        monkeypatch.delenv("LLM_FALLBACK", raising=False)

        captured: list[tuple[llm.Response, float]] = []
        client = llm.create_llm(
            "mock://m",
            on_response=lambda r, t: captured.append((r, t)),
        )
        req = llm.Request(
            messages=[llm.Message(role="user", content="hi")],
            model="m",
        )
        client.complete(req)
        assert len(captured) == 1
        assert captured[0][0].content == "mock:m"
        assert captured[0][1] >= 0.0

    def test_on_error_fires(self, monkeypatch, tmp_path):
        monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
        monkeypatch.delenv("LLM_PROVIDER", raising=False)
        monkeypatch.delenv("LLM_API_KEY", raising=False)
        monkeypatch.delenv("LLM_BASE_URL", raising=False)
        monkeypatch.delenv("LLM_FALLBACK", raising=False)

        llm.register("fail", _fail_factory)
        captured: list[Exception] = []
        client = llm.create_llm(
            "fail://m",
            fallback=["mock://m"],
            on_error=captured.append,
        )
        req = llm.Request(
            messages=[llm.Message(role="user", content="hi")],
            model="m",
        )
        client.complete(req)
        assert len(captured) == 1
        assert isinstance(captured[0], llm.RateLimitError)

    def test_on_fallback_fires(self, monkeypatch, tmp_path):
        monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
        monkeypatch.delenv("LLM_PROVIDER", raising=False)
        monkeypatch.delenv("LLM_API_KEY", raising=False)
        monkeypatch.delenv("LLM_BASE_URL", raising=False)
        monkeypatch.delenv("LLM_FALLBACK", raising=False)

        llm.register("fail", _fail_factory)
        captured: list[tuple[int, int, Exception]] = []
        client = llm.create_llm(
            "fail://m",
            fallback=["mock://m"],
            on_fallback=lambda i, n, e: captured.append((i, n, e)),
        )
        req = llm.Request(
            messages=[llm.Message(role="user", content="hi")],
            model="m",
        )
        client.complete(req)
        assert len(captured) == 1
        idx, total, err = captured[0]
        assert idx == 0
        assert total == 1
        assert isinstance(err, llm.RateLimitError)


# ---------------------------------------------------------------------------
# Error types and is_fallbackable
# ---------------------------------------------------------------------------


class TestErrors:
    def test_llm_error(self):
        e = llm.LLMError("boom")
        assert str(e) == "boom"
        assert isinstance(e, Exception)

    def test_provider_not_found(self):
        e = llm.ProviderNotFoundError("openai")
        assert e.scheme == "openai"
        assert "openai" in str(e)

    def test_capability_not_supported(self):
        e = llm.CapabilityNotSupportedError("stream", "mock")
        assert e.capability == "stream"
        assert e.provider == "mock"

    def test_auth_error(self):
        e = llm.AuthError("openai")
        assert e.provider == "openai"

    def test_rate_limit_error(self):
        e = llm.RateLimitError("openai", retry_after=1.5)
        assert e.provider == "openai"
        assert e.retry_after == 1.5

    def test_model_error(self):
        e = llm.ModelError("gpt-5", "openai")
        assert e.model == "gpt-5"
        assert e.provider == "openai"

    def test_fallback_exhausted(self):
        errs = [ValueError("a"), ValueError("b")]
        e = llm.FallbackExhaustedError(errs)
        assert e.errors == errs

    def test_is_fallbackable_rate_limit(self):
        assert llm.is_fallbackable(llm.RateLimitError("x", 1.0))

    def test_is_fallbackable_model_error(self):
        assert not llm.is_fallbackable(llm.ModelError("m", "p"))

    def test_not_fallbackable_auth(self):
        assert not llm.is_fallbackable(llm.AuthError("x"))

    def test_not_fallbackable_generic(self):
        assert not llm.is_fallbackable(ValueError("nope"))
