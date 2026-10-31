"""The surface-exposure policy gate.

Distinct from ``tests/test_safety.py``, which covers
``hop_top_kit.safety`` — the Factor 10 CLI ``--force`` guard. That module
decides whether a human at a terminal may run a dangerous command; this
one decides whether a *transport* may reach a command at all. Confusing
the two is the mistake this suite exists to make expensive.
"""

from __future__ import annotations

import pytest

from hop_top_kit.mcp import Bridge, Command, Result, Surface, classify, default_policy
from hop_top_kit.mcp.safety import (
    DESTRUCTIVE_TIERS,
    Policy,
    SafetyClass,
    SurfaceNotEnabledError,
)


@pytest.mark.parametrize("tier", sorted(DESTRUCTIVE_TIERS))
def test_every_destructive_tier_classifies_destructive(tier: str) -> None:
    """All three ``kit/side-effect`` destructive tiers count."""
    assert classify({"kit/side-effect": tier}).destructive


@pytest.mark.parametrize("tier", ["read", "write", "", "destructive-ish"])
def test_non_destructive_tiers_do_not(tier: str) -> None:
    """Anything outside the three named tiers is not destructive.

    Notably ``write``: a mutating command is not automatically a
    destructive one, and widening this predicate would quietly lock
    adopters out of ordinary write operations over MCP.
    """
    assert not classify({"kit/side-effect": tier}).destructive


def test_no_annotations_is_the_zero_class() -> None:
    """An unannotated command is read-only, no auth, no confirmation."""
    assert classify(None) == SafetyClass()
    assert classify({}) == SafetyClass()


def test_default_policy_blocks_destructive_on_every_remote_surface() -> None:
    """The default is block-all, and empty means block — not allow.

    ``allow_destructive_on`` being empty is the *most restrictive*
    setting, which inverts the usual reading of an empty allowlist. That
    inversion is the whole point of the default, so it is asserted across
    every remote surface rather than spot-checked on MCP.
    """
    policy = default_policy()
    destructive = classify({"kit/side-effect": "destructive"})
    assert policy.allow_destructive_on == ()
    for surface in Surface:
        if surface in (Surface.CLI, Surface.LIB):
            continue
        assert not policy.allowed(destructive, surface), surface


def test_local_surfaces_are_always_allowed() -> None:
    """CLI and LIB run in-process and are never gated."""
    policy = default_policy()
    destructive = classify({"kit/side-effect": "destructive"})
    assert policy.allowed(destructive, Surface.CLI)
    assert policy.allowed(destructive, Surface.LIB)


def test_non_destructive_is_allowed_everywhere() -> None:
    """Rule 2: the gate only ever constrains destructive leaves."""
    policy = default_policy()
    for surface in Surface:
        assert policy.allowed(classify({"kit/side-effect": "read"}), surface)


def test_opting_a_surface_in_allows_only_that_surface() -> None:
    """Naming a surface lifts the ceiling for it alone."""
    policy = Policy(allow_destructive_on=(Surface.MCP,))
    destructive = classify({"kit/side-effect": "destructive"})
    assert policy.allowed(destructive, Surface.MCP)
    assert not policy.allowed(destructive, Surface.REST)


def test_default_enabled_matches_go() -> None:
    """Default enablement is CLI + LIB + MCP."""
    assert default_policy().resolved_defaults() == (Surface.CLI, Surface.LIB, Surface.MCP)


def test_annotations_parse_into_the_class() -> None:
    """Auth, confirmation, permissions, and exit codes all round-trip."""
    cls = classify(
        {
            "kit/auth-required": "true",
            "kit/requires-confirmation": "true",
            "kit/permissions": "read:widgets, write:widgets ,",
            "kit/exit-codes": "OK,CONFLICT",
        }
    )
    assert cls.auth_required
    assert cls.requires_confirmation
    assert cls.permissions == ("read:widgets", "write:widgets")
    assert cls.exit_codes == ("OK", "CONFLICT")


def test_annotation_values_other_than_true_are_false() -> None:
    """Only the exact string ``"true"`` enables a boolean annotation."""
    cls = classify({"kit/auth-required": "yes", "kit/requires-confirmation": "1"})
    assert not cls.auth_required
    assert not cls.requires_confirmation


def test_hide_removes_a_leaf_from_the_surface() -> None:
    """Enablement is a separate gate from the destructive ceiling."""
    root = Command(
        name="root",
        children=[Command(name="ping", short="Ping", run=lambda flags: Result())],
    )
    bridge = Bridge(root)
    bridge.hide("ping", Surface.MCP)
    leaf = bridge.resolve_leaf(("ping",))
    assert not leaf.enabled[Surface.MCP]

    from hop_top_kit.mcp import Invocation, Meta

    with pytest.raises(SurfaceNotEnabledError):
        bridge.invoke(Invocation(path=("ping",), meta=Meta(surface=Surface.MCP)))


# -- destructive-by-name inference (shared with the CLI gate) ----------------


@pytest.mark.parametrize("name", ["delete", "remove", "rm", "destroy", "purge", "drop"])
def test_unannotated_destructive_name_classifies_destructive(name: str) -> None:
    """The one inference table, as Go's cmdreflect applies it."""
    assert classify(None, leaf_name=name).destructive
    assert classify({}, leaf_name=name).destructive
    assert classify({"kit/side-effect": ""}, leaf_name=name).destructive


def test_inference_never_overrides_an_explicit_annotation() -> None:
    assert not classify({"kit/side-effect": "read"}, leaf_name="drop").destructive
    assert not classify({"kit/side-effect": "bogus"}, leaf_name="purge").destructive
    assert not classify(None, leaf_name="delete-all").destructive
    assert not classify(None, leaf_name="list").destructive


def test_bridge_blocks_an_unannotated_destructive_named_leaf_remotely() -> None:
    from hop_top_kit.mcp import Invocation, Meta
    from hop_top_kit.mcp.safety import DestructiveBlockedError

    ran: list[str] = []

    def run(flags: dict) -> Result:
        ran.append("ran")
        return Result()

    root = Command(
        name="root",
        children=[
            Command(name="widget", children=[Command(name="remove", short="r", run=run)]),
            Command(name="list", short="l", run=lambda flags: Result()),
        ],
    )
    bridge = Bridge(root)
    assert bridge.resolve_leaf(("widget", "remove")).cls.destructive
    assert not bridge.resolve_leaf(("list",)).cls.destructive
    with pytest.raises(DestructiveBlockedError):
        bridge.invoke(Invocation(path=("widget", "remove"), meta=Meta(surface=Surface.MCP)))
    assert ran == []
