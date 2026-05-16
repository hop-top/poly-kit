"""Tests for hop_top_kit.scope — Python port parity with the Go scope package.

Parity strategy:

- Load the canonical ``contracts/parity/scope-defaults.json`` directly and
  compare it byte-for-byte (post JSON parse) with the local copy at
  ``sdk/py/hop_top_kit/scope-defaults.json``. Drift fails the test —
  keeping ports honest about syncing the contract.
- ``secret_paths()`` is verified to be (common ∪ platform-specific) on the
  current host, with ``~`` left unexpanded (the Go port also leaves it for
  late expansion at match time).
"""

from __future__ import annotations

import json
import sys
from pathlib import Path

import pytest

from hop_top_kit.scope import (
    Decision,
    ErrDenied,
    Mode,
    Op,
    Policy,
    default,
    new,
    secret_paths,
    set_default,
)

_HERE = Path(__file__).parent
_REPO_ROOT = _HERE.parent.parent.parent
_CANONICAL_DEFAULTS = _REPO_ROOT / "contracts" / "parity" / "scope-defaults.json"
_LOCAL_DEFAULTS = _HERE.parent / "hop_top_kit" / "scope-defaults.json"


# ─── Contract sync ───────────────────────────────────────────────────────────


class TestContractParity:
    def test_local_matches_canonical(self) -> None:
        """sdk/py local copy must equal contracts/parity/scope-defaults.json."""
        local = json.loads(_LOCAL_DEFAULTS.read_text(encoding="utf-8"))
        canonical = json.loads(_CANONICAL_DEFAULTS.read_text(encoding="utf-8"))
        assert local == canonical


# ─── secret_paths ────────────────────────────────────────────────────────────


class TestSecretPaths:
    def test_non_empty(self) -> None:
        assert len(secret_paths()) > 0

    def test_contains_common_ssh(self) -> None:
        assert "~/.ssh/**" in secret_paths()

    def test_includes_platform_specific(self) -> None:
        sp = secret_paths()
        if sys.platform == "darwin":
            assert "~/Library/Keychains/**" in sp
        elif sys.platform.startswith("linux"):
            assert "~/.mozilla/firefox/**/cookies.sqlite" in sp


# ─── Policy: allow / deny / check ────────────────────────────────────────────


class TestCheck:
    def test_empty_policy_unknown(self) -> None:
        p = new()
        assert p.check("/tmp/anything", Op.READ) is Decision.UNKNOWN

    def test_allow_matches(self) -> None:
        p = new().allow("/tmp/**")
        assert p.check("/tmp/foo.txt", Op.READ) is Decision.ALLOWED

    def test_deny_wins_over_allow(self) -> None:
        p = new().allow("/tmp/**").deny("/tmp/sensitive/**")
        assert p.check("/tmp/sensitive/x", Op.READ) is Decision.DENIED

    def test_op_bitset_filters(self) -> None:
        p = new().allow_op(Op.READ, "/tmp/**")
        assert p.check("/tmp/x", Op.READ) is Decision.ALLOWED
        assert p.check("/tmp/x", Op.WRITE) is Decision.UNKNOWN


# ─── Policy.enforce ──────────────────────────────────────────────────────────


class TestEnforce:
    def test_strict_unknown_raises(self) -> None:
        p = new()
        with pytest.raises(ErrDenied):
            p.enforce("/tmp/x", Op.READ)

    def test_strict_allowed_ok(self) -> None:
        p = new().allow("/tmp/**")
        p.enforce("/tmp/x", Op.READ)  # no raise

    def test_warn_denied_ok(self) -> None:
        p = new().set_mode(Mode.WARN).deny("/tmp/x")
        p.enforce("/tmp/x", Op.READ)  # no raise; logs

    def test_prompt_callback_true_ok(self) -> None:
        p = new().set_mode(Mode.PROMPT).deny("/tmp/x").set_prompt_func(lambda _p, _o: True)
        p.enforce("/tmp/x", Op.READ)

    def test_prompt_callback_false_raises(self) -> None:
        p = new().set_mode(Mode.PROMPT).deny("/tmp/x").set_prompt_func(lambda _p, _o: False)
        with pytest.raises(ErrDenied):
            p.enforce("/tmp/x", Op.READ)


# ─── Snapshot + set_default ──────────────────────────────────────────────────


class TestSnapshot:
    def test_snapshot_independent(self) -> None:
        orig = new().allow("/tmp/**")
        cp: Policy = orig.snapshot()
        cp.deny("/tmp/x")
        assert orig.check("/tmp/x", Op.READ) is Decision.ALLOWED
        assert cp.check("/tmp/x", Op.READ) is Decision.DENIED


class TestSetDefault:
    def test_swap_and_restore(self) -> None:
        before = default()
        swap = new().allow("/tmp/swap/**")
        restore = set_default(swap)
        try:
            assert default() is swap
        finally:
            restore()
        assert default() is before


# ─── Default singleton seeded with secret_paths ──────────────────────────────


class TestDefaultSingleton:
    def test_denies_known_dotenv(self) -> None:
        # **/.env is in the common deny set on every platform.
        policy = default()
        assert policy.check("/tmp/whatever/.env", Op.READ) is Decision.DENIED
