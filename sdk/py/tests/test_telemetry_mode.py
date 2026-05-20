"""Tests for hop_top_kit.telemetry.mode."""

from __future__ import annotations

import pytest

from hop_top_kit.telemetry.mode import Mode, parse_mode, resolve_mode


class TestParseMode:
    @pytest.mark.parametrize(
        "raw,expected",
        [
            ("off", Mode.OFF),
            ("anon", Mode.ANON),
            ("full", Mode.FULL),
            ("OFF", Mode.OFF),
            ("Anon", Mode.ANON),
            ("FULL", Mode.FULL),
            ("  full  ", Mode.FULL),
        ],
    )
    def test_known_values_case_insensitive(self, raw, expected):
        m, ok = parse_mode(raw)
        assert ok is True
        assert m is expected

    def test_empty_string_maps_off_true(self):
        m, ok = parse_mode("")
        assert m is Mode.OFF
        assert ok is True

    def test_none_maps_off_true(self):
        m, ok = parse_mode(None)
        assert m is Mode.OFF
        assert ok is True

    @pytest.mark.parametrize("raw", ["garbage", "on", "true", "yes", "anonymous"])
    def test_unknown_maps_off_false(self, raw):
        m, ok = parse_mode(raw)
        assert m is Mode.OFF
        assert ok is False


class TestResolveMode:
    def test_empty_env_defaults_off(self):
        assert resolve_mode({}) is Mode.OFF

    def test_kit_telemetry_mode_anon(self):
        assert resolve_mode({"KIT_TELEMETRY_MODE": "anon"}) is Mode.ANON

    def test_kit_telemetry_mode_full(self):
        assert resolve_mode({"KIT_TELEMETRY_MODE": "full"}) is Mode.FULL

    def test_kit_telemetry_mode_off(self):
        assert resolve_mode({"KIT_TELEMETRY_MODE": "off"}) is Mode.OFF

    def test_app_prefix_overrides_kit(self):
        env = {
            "KIT_APP_PREFIX": "spaced",
            "SPACED_TELEMETRY_MODE": "full",
            "KIT_TELEMETRY_MODE": "anon",
        }
        assert resolve_mode(env) is Mode.FULL

    def test_app_prefix_set_but_app_var_missing_falls_through(self):
        env = {
            "KIT_APP_PREFIX": "spaced",
            "KIT_TELEMETRY_MODE": "anon",
        }
        assert resolve_mode(env) is Mode.ANON

    def test_app_prefix_set_but_app_var_empty_falls_through(self):
        env = {
            "KIT_APP_PREFIX": "spaced",
            "SPACED_TELEMETRY_MODE": "   ",
            "KIT_TELEMETRY_MODE": "anon",
        }
        assert resolve_mode(env) is Mode.ANON

    def test_app_prefix_malformed_value_falls_through(self):
        env = {
            "KIT_APP_PREFIX": "spaced",
            "SPACED_TELEMETRY_MODE": "garbage",
            "KIT_TELEMETRY_MODE": "full",
        }
        assert resolve_mode(env) is Mode.FULL

    def test_malformed_kit_only_returns_off(self):
        assert resolve_mode({"KIT_TELEMETRY_MODE": "garbage"}) is Mode.OFF

    def test_does_not_raise_on_weird_env(self):
        # Strings only — but make sure odd whitespace / empty values are tolerated.
        env = {
            "KIT_APP_PREFIX": "",
            "KIT_TELEMETRY_MODE": "",
        }
        assert resolve_mode(env) is Mode.OFF

    def test_uses_os_environ_when_none(self, monkeypatch):
        monkeypatch.delenv("KIT_APP_PREFIX", raising=False)
        monkeypatch.setenv("KIT_TELEMETRY_MODE", "anon")
        assert resolve_mode() is Mode.ANON

    def test_app_prefix_lowercase_in_env_uppercased(self):
        # KIT_APP_PREFIX value is upper-cased before lookup; SDK consumer can
        # set "spaced" but the env var read is SPACED_TELEMETRY_MODE.
        env = {
            "KIT_APP_PREFIX": "spaced",
            "SPACED_TELEMETRY_MODE": "anon",
        }
        assert resolve_mode(env) is Mode.ANON
