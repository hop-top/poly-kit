"""Tests for hop_top_kit.auth — credential lifecycle."""

from __future__ import annotations

from hop_top_kit.auth import AuthIntrospector, Credential, NoAuth


class TestCredential:
    """Credential dataclass fields."""

    def test_required_fields(self) -> None:
        c = Credential(
            source="env",
            identity="user@example.com",
            scopes=["read", "write"],
        )
        assert c.source == "env"
        assert c.identity == "user@example.com"
        assert c.scopes == ["read", "write"]
        assert c.expires_at is None
        assert c.renewable is False

    def test_optional_fields(self) -> None:
        c = Credential(
            source="oauth",
            identity="bot",
            scopes=["admin"],
            expires_at="2026-12-31T00:00:00Z",
            renewable=True,
        )
        assert c.expires_at == "2026-12-31T00:00:00Z"
        assert c.renewable is True


class TestNoAuth:
    """NoAuth returns a sentinel credential and refresh is a no-op."""

    def test_inspect_returns_credential(self) -> None:
        na = NoAuth()
        cred = na.inspect()
        assert isinstance(cred, Credential)

    def test_inspect_source(self) -> None:
        cred = NoAuth().inspect()
        assert cred.source == "none"

    def test_inspect_identity_empty(self) -> None:
        cred = NoAuth().inspect()
        assert cred.identity == ""

    def test_inspect_scopes_empty(self) -> None:
        cred = NoAuth().inspect()
        assert cred.scopes == []

    def test_inspect_not_renewable(self) -> None:
        cred = NoAuth().inspect()
        assert cred.renewable is False

    def test_inspect_no_expiry(self) -> None:
        cred = NoAuth().inspect()
        assert cred.expires_at is None

    def test_refresh_noop(self) -> None:
        na = NoAuth()
        na.refresh()  # should not raise

    def test_implements_protocol(self) -> None:
        na = NoAuth()
        assert isinstance(na, AuthIntrospector)
