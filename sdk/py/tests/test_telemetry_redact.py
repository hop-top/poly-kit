"""Tests for hop_top_kit.telemetry.redact."""

from __future__ import annotations

import os

import pytest

# Bind the SUBMODULE: `hop_top_kit.telemetry` also re-exports a `redact`
# function, so `from telemetry import redact as r` resolves to the function.
import hop_top_kit.telemetry.redact as r


class TestRedactString:
    def test_email(self):
        assert r.redact_string("contact user@example.com today") == (
            "contact <redacted:email> today"
        )

    def test_ipv4(self):
        assert r.redact_string("host 192.168.1.1 down") == "host <redacted:ipv4> down"

    def test_ipv6_compressed(self):
        # Cross-SDK pattern (matches PHP) covers `::` compressed form.
        assert r.redact_string("addr 2001:db8::1 here") == "addr <redacted:ipv6> here"

    def test_ipv6_link_local(self):
        assert (
            r.redact_string("peer fe80::1234:5678:abcd:ef01 up")
            == "peer <redacted:ipv6> up"
        )

    def test_ipv6_loopback_under_match_documented(self):
        # ::1 starts with "::" (zero leading groups). The PHP-parity
        # pattern requires AT LEAST one hex group before `::`, so the
        # bare `::1` loopback is NOT redacted. Documented under-match;
        # tracked under follow-up #21 trade-off (matching PHP > drift).
        assert r.redact_string("ping ::1 ok") == "ping ::1 ok"

    def test_ipv6_ipv4_mapped_documented(self):
        # IPv4-mapped form `::ffff:a.b.c.d` likewise starts with `::`
        # (no leading hex group), so the IPv6 pass leaves it alone. The
        # IPv4 pass then redacts the dotted-quad tail. Result is a
        # hybrid `::ffff:<redacted:ipv4>` — documented partial leak of
        # the well-known `::ffff` prefix; accepted for PHP parity.
        out = r.redact_string("hybrid ::ffff:192.168.1.1 here")
        assert "192.168.1.1" not in out
        assert "<redacted:ipv4>" in out

    def test_ipv6_full_form(self):
        # No regression on the explicit 8-group form.
        assert (
            r.redact_string("v6 2001:0db8:85a3:0000:0000:8a2e:0370:7334 end")
            == "v6 <redacted:ipv6> end"
        )

    def test_sk_token(self):
        assert r.redact_string("sk-ABCDEFGH key") == "<redacted:token> key"

    def test_sk_token_short_no_match(self):
        # ≥8 chars after sk- prefix required.
        s = "sk-abc"
        assert r.redact_string(s) == s

    @pytest.mark.parametrize("prefix", ["ghp", "gho", "ghu", "ghs", "ghr"])
    def test_gh_token(self, prefix):
        token = f"{prefix}_" + "x" * 20
        assert r.redact_string(f"gh {token} z") == "gh <redacted:token> z"

    def test_xoxb_token(self):
        s = "leak xoxb-12345-67890-" + "a" * 24
        assert r.redact_string(s) == "leak <redacted:token>"

    def test_home_prefix_rewrites(self, monkeypatch, tmp_path):
        # Force a stable HOME via reimport so the compiled pattern picks it up.
        monkeypatch.setenv("HOME", "/Users/testuser")
        from importlib import reload
        import hop_top_kit.telemetry.redact as r2

        reload(r2)
        assert r2.redact_string("path /Users/testuser/work/x.go") == "path $HOME/work/x.go"

    def test_non_redactable_passthrough(self):
        s = "nothing sensitive here"
        assert r.redact_string(s) == s


class TestRedact:
    def test_string_passthrough_non_redactable(self):
        assert r.redact("hello") == "hello"

    def test_nested_dict(self):
        out = r.redact({"u": "a@b.co", "n": {"ip": "1.2.3.4", "ok": True}})
        assert out == {
            "u": "<redacted:email>",
            "n": {"ip": "<redacted:ipv4>", "ok": True},
        }

    def test_list(self):
        out = r.redact(["a@b.co", 5, None, "plain"])
        assert out == ["<redacted:email>", 5, None, "plain"]

    def test_tuple(self):
        out = r.redact(("a@b.co", 5))
        assert isinstance(out, tuple)
        assert out == ("<redacted:email>", 5)

    def test_does_not_mutate_input(self):
        src = {"u": "a@b.co", "list": ["a@b.co"]}
        before = repr(src)
        r.redact(src)
        assert repr(src) == before

    @pytest.mark.parametrize("v", [42, 3.14, True, False, None])
    def test_scalar_passthrough(self, v):
        assert r.redact(v) == v
