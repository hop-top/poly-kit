"""Integration tests for RouteLLM adapter wiring and config flow.

These tests verify the adapter registration, config parsing, error types,
and hook wiring WITHOUT requiring a running routellm server.
"""

from __future__ import annotations

import pytest

from hop_top_kit import llm

# ------------------------------------------------------------------
# 1. test_scheme_registration
# ------------------------------------------------------------------


class TestSchemeRegistration:
    """Verify the routellm scheme is registered after import."""

    def test_routellm_scheme_in_registry(self):
        import hop_top_kit.routellm_adapter  # noqa: F401

        assert "routellm" in llm._registry

    def test_registry_factory_callable(self):
        import hop_top_kit.routellm_adapter  # noqa: F401

        factory = llm._registry["routellm"]
        assert callable(factory)

    def test_duplicate_register_is_idempotent(self):
        import hop_top_kit.routellm_adapter as mod

        # Should not raise on repeated calls.
        mod._register()
        assert "routellm" in llm._registry


# ------------------------------------------------------------------
# 2. test_config_round_trip
# ------------------------------------------------------------------


class TestConfigRoundTrip:
    """Create a RouterConfig, serialize to dict, parse back, verify."""

    def test_defaults(self):
        cfg = llm.default_router_config()
        assert cfg.base_url == "http://localhost:6060"
        assert cfg.grpc_port == 6061
        assert cfg.strong_model == ""
        assert cfg.weak_model == ""
        assert cfg.routers == []
        assert cfg.autostart is False
        assert cfg.pid_file == ""

    def test_parse_from_extras(self):
        extras = {
            "routellm": {
                "base_url": "http://custom:9090",
                "grpc_port": 7070,
                "strong_model": "gpt-4",
                "weak_model": "gpt-3.5-turbo",
                "routers": ["mf", "bert"],
                "autostart": True,
                "pid_file": "/tmp/routellm.pid",
            }
        }
        cfg = llm.parse_router_config(extras)
        assert cfg.base_url == "http://custom:9090"
        assert cfg.grpc_port == 7070
        assert cfg.strong_model == "gpt-4"
        assert cfg.weak_model == "gpt-3.5-turbo"
        assert cfg.routers == ["mf", "bert"]
        assert cfg.autostart is True
        assert cfg.pid_file == "/tmp/routellm.pid"

    def test_env_overrides(self, monkeypatch):
        monkeypatch.setenv("ROUTELLM_BASE_URL", "http://env:1111")
        monkeypatch.setenv("ROUTELLM_STRONG_MODEL", "claude-3")
        monkeypatch.setenv("ROUTELLM_WEAK_MODEL", "haiku")
        monkeypatch.setenv("ROUTELLM_ROUTERS", "mf, bert")

        cfg = llm.parse_router_config({})
        assert cfg.base_url == "http://env:1111"
        assert cfg.strong_model == "claude-3"
        assert cfg.weak_model == "haiku"
        assert cfg.routers == ["mf", "bert"]

    def test_env_overrides_config(self, monkeypatch):
        """Env vars take precedence over extras dict."""
        monkeypatch.setenv("ROUTELLM_BASE_URL", "http://env:2222")

        extras = {
            "routellm": {
                "base_url": "http://config:3333",
            }
        }
        cfg = llm.parse_router_config(extras)
        assert cfg.base_url == "http://env:2222"

    def test_invalid_extras_type_raises(self):
        with pytest.raises(llm.LLMError, match="expected dict"):
            llm.parse_router_config({"routellm": "not-a-dict"})

    def test_empty_extras_returns_defaults(self):
        cfg = llm.parse_router_config({})
        default = llm.default_router_config()
        assert cfg.base_url == default.base_url
        assert cfg.grpc_port == default.grpc_port


# ------------------------------------------------------------------
# 3. test_eva_config_parsing
# ------------------------------------------------------------------


class TestEvaConfigParsing:
    """Verify the eva section is parsed correctly."""

    def test_eva_defaults(self):
        cfg = llm.default_router_config()
        assert cfg.eva.contracts == []
        assert cfg.eva.enforce is False

    def test_eva_from_extras(self):
        extras = {
            "routellm": {
                "eva": {
                    "contracts": ["safety-v1", "quality-v2"],
                    "enforce": True,
                }
            }
        }
        cfg = llm.parse_router_config(extras)
        assert cfg.eva.contracts == ["safety-v1", "quality-v2"]
        assert cfg.eva.enforce is True

    def test_eva_partial(self):
        """Only contracts set, enforce defaults to False."""
        extras = {
            "routellm": {
                "eva": {
                    "contracts": ["safety-v1"],
                }
            }
        }
        cfg = llm.parse_router_config(extras)
        assert cfg.eva.contracts == ["safety-v1"]
        assert cfg.eva.enforce is False

    def test_eva_missing(self):
        """No eva section at all — defaults apply."""
        extras = {"routellm": {"base_url": "http://x:1"}}
        cfg = llm.parse_router_config(extras)
        assert cfg.eva.contracts == []
        assert cfg.eva.enforce is False


# ------------------------------------------------------------------
# 4. test_error_types
# ------------------------------------------------------------------


class TestErrorTypes:
    """Verify new error types and is_fallbackable classification."""

    def test_router_unavailable_is_fallbackable(self):
        err = llm.RouterUnavailableError("mf")
        assert llm.is_fallbackable(err) is True

    def test_router_timeout_is_fallbackable(self):
        err = llm.RouterTimeoutError("mf", 5.0)
        assert llm.is_fallbackable(err) is True

    def test_rate_limit_is_fallbackable(self):
        err = llm.RateLimitError("openai", 1.0)
        assert llm.is_fallbackable(err) is True

    def test_contract_violation_not_fallbackable(self):
        err = llm.ContractViolationError("safety", ["bad output"])
        assert llm.is_fallbackable(err) is False

    def test_threshold_invalid_not_fallbackable(self):
        err = llm.ThresholdInvalidError(1.5)
        assert llm.is_fallbackable(err) is False

    def test_auth_error_not_fallbackable(self):
        err = llm.AuthError("openai")
        assert llm.is_fallbackable(err) is False

    def test_provider_not_found_not_fallbackable(self):
        err = llm.ProviderNotFoundError("unknown")
        assert llm.is_fallbackable(err) is False

    def test_llm_error_not_fallbackable(self):
        err = llm.LLMError("generic")
        assert llm.is_fallbackable(err) is False

    def test_error_hierarchy(self):
        """All custom errors inherit from LLMError."""
        assert issubclass(llm.RouterUnavailableError, llm.LLMError)
        assert issubclass(llm.RouterTimeoutError, llm.LLMError)
        assert issubclass(llm.ContractViolationError, llm.LLMError)
        assert issubclass(llm.ThresholdInvalidError, llm.LLMError)
        assert issubclass(llm.FallbackExhaustedError, llm.LLMError)

    def test_router_unavailable_attrs(self):
        cause = RuntimeError("conn refused")
        err = llm.RouterUnavailableError("mf", cause)
        assert err.router == "mf"
        assert err.err is cause
        assert "mf" in str(err)

    def test_contract_violation_attrs(self):
        err = llm.ContractViolationError("safety-v1", ["profanity", "pii"])
        assert err.contract == "safety-v1"
        assert err.violations == ["profanity", "pii"]


# ------------------------------------------------------------------
# 5. test_hooks_wiring
# ------------------------------------------------------------------


class TestHooksWiring:
    """Verify on_route and on_eva_result are accepted by Client."""

    def _make_stub_provider(self):
        """Return a minimal provider stub for Client construction."""

        class Stub:
            def close(self):
                pass

            def complete(self, req):
                return llm.Response(content="ok")

        return Stub()

    def test_client_accepts_on_route(self):
        calls = []

        def on_route(router, score, model):
            calls.append((router, score, model))

        provider = self._make_stub_provider()
        client = llm.Client(provider, on_route=on_route)
        assert client._on_route is on_route

    def test_client_accepts_on_eva_result(self):
        calls = []

        def on_eva_result(contract, passed, violations):
            calls.append((contract, passed, violations))

        provider = self._make_stub_provider()
        client = llm.Client(provider, on_eva_result=on_eva_result)
        assert client._on_eva_result is on_eva_result

    def test_client_all_hooks(self):
        """Client accepts all hook kwargs without error."""
        provider = self._make_stub_provider()
        client = llm.Client(
            provider,
            on_request=lambda req: None,
            on_response=lambda resp, t: None,
            on_error=lambda err: None,
            on_fallback=lambda f, t, e: None,
            on_route=lambda r, s, m: None,
            on_eva_result=lambda c, p, v: None,
        )
        assert client._provider is provider

    def test_create_llm_accepts_hooks(self, monkeypatch):
        """create_llm passes on_route and on_eva_result through."""
        monkeypatch.setenv("LLM_PROVIDER", "stub://test-model")

        stub = self._make_stub_provider()

        def fake_factory(cfg):
            return stub

        # Register a stub scheme.
        if "stub" not in llm._registry:
            llm.register("stub", fake_factory)

        try:
            client = llm.create_llm(
                "stub://test-model",
                on_route=lambda r, s, m: None,
                on_eva_result=lambda c, p, v: None,
            )
            assert client._on_route is not None
            assert client._on_eva_result is not None
        finally:
            # Clean up stub registration.
            llm._registry.pop("stub", None)
