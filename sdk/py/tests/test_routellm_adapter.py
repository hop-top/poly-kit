"""Tests for hop_top_kit.routellm_adapter."""

from __future__ import annotations

from unittest import mock

import pytest

from hop_top_kit import llm

# ------------------------------------------------------------------
# URI parsing
# ------------------------------------------------------------------


class TestParseRouterThreshold:
    def test_basic(self):
        from hop_top_kit.routellm_adapter import parse_router_threshold

        router, thresh = parse_router_threshold("mf:0.7")
        assert router == "mf"
        assert thresh == pytest.approx(0.7)

    def test_zero_threshold(self):
        from hop_top_kit.routellm_adapter import parse_router_threshold

        router, thresh = parse_router_threshold("bert:0.0")
        assert router == "bert"
        assert thresh == 0.0

    def test_one_threshold(self):
        from hop_top_kit.routellm_adapter import parse_router_threshold

        _, thresh = parse_router_threshold("sw_ranking:1.0")
        assert thresh == 1.0

    def test_missing_colon_raises(self):
        from hop_top_kit.routellm_adapter import parse_router_threshold

        with pytest.raises(llm.LLMError, match="router:threshold"):
            parse_router_threshold("mf0.7")

    def test_invalid_float_raises(self):
        from hop_top_kit.routellm_adapter import parse_router_threshold

        with pytest.raises(llm.LLMError, match="not a valid float"):
            parse_router_threshold("mf:abc")

    def test_out_of_range_raises(self):
        from hop_top_kit.routellm_adapter import parse_router_threshold

        with pytest.raises(llm.ThresholdInvalidError):
            parse_router_threshold("mf:1.5")

    def test_negative_raises(self):
        from hop_top_kit.routellm_adapter import parse_router_threshold

        with pytest.raises(llm.ThresholdInvalidError):
            parse_router_threshold("mf:-0.1")


# ------------------------------------------------------------------
# Factory error when routellm not installed
# ------------------------------------------------------------------


class TestFactoryMissingDep:
    def test_raises_import_error(self):
        """Factory raises ImportError when routellm is absent."""
        from hop_top_kit.routellm_adapter import RouteLLMAdapter

        cfg = llm.ResolvedConfig(
            uri=llm.URI(scheme="routellm", model="mf:0.5"),
            provider=llm.ProviderConfig(extras={}),
        )

        # Patch Controller to None to simulate missing dep
        with (
            mock.patch("hop_top_kit.routellm_adapter.Controller", None),
            pytest.raises(ImportError, match="routellm is not installed"),
        ):
            RouteLLMAdapter(cfg)


# ------------------------------------------------------------------
# Scheme registration
# ------------------------------------------------------------------


class TestRegistration:
    def test_routellm_scheme_registered(self):
        """Importing the adapter registers the routellm scheme."""
        import hop_top_kit.routellm_adapter as mod

        # Re-register in case other tests cleared the registry.
        mod._register()
        assert "routellm" in llm._registry

    def test_double_import_no_error(self):
        """Re-importing does not raise on duplicate registration."""
        import hop_top_kit.routellm_adapter as mod

        # Call _register again explicitly — should be idempotent.
        mod._register()
        assert "routellm" in llm._registry
