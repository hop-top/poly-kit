"""The one side-effect resolution: tiers, destructive-by-name inference,
the dry-run table. Every reader of ``kit/side-effect`` in the SDK goes
through ``hop_top_kit.sideeffect``; the MCP gate's own coverage lives in
``test_mcp_safety.py``."""

from __future__ import annotations

import pytest

from hop_top_kit import sideeffect as se

SE = "kit/side-effect"


def test_destructive_name_table_is_gos() -> None:
    assert frozenset({"delete", "remove", "rm", "destroy", "purge", "drop"}) == se.DESTRUCTIVE_NAMES
    assert se.INFERRED_TIER == "destructive-shared"


@pytest.mark.parametrize(
    ("name", "declared", "want"),
    [
        ("delete", None, True),
        ("rm", "", True),
        ("drop", None, True),
        ("delete-all", None, False),  # only near the table
        ("list", None, False),
        ("Delete", None, False),  # case-sensitive, like Go
        ("drop", "read", False),  # explicit annotation wins
        ("purge", "bogus", False),  # malformed is never inferred over
        ("remove", "destructive", False),  # already declared: not "inferred"
    ],
)
def test_inferred_destructive(name: str, declared: str | None, want: bool) -> None:
    assert se.inferred_destructive(name, declared) is want


def test_effective_side_effect_resolution() -> None:
    assert se.effective_side_effect("delete", {}) == "destructive-shared"
    assert se.effective_side_effect("delete", None) == "destructive-shared"
    assert se.effective_side_effect("delete", {SE: ""}) == "destructive-shared"
    assert se.effective_side_effect("delete", {SE: "read"}) == "read"
    assert se.effective_side_effect("purge", {SE: "bogus"}) == "bogus"
    assert se.effective_side_effect("list", {}) is None
    assert se.effective_side_effect("list", {SE: "write"}) == "write"


def test_tier_predicates() -> None:
    for t in ("write", "write-local", "write-shared"):
        assert se.is_write_like(t) and not se.is_destructive_like(t)
    for t in ("destructive", "destructive-local", "destructive-shared"):
        assert se.is_destructive_like(t) and not se.is_write_like(t)
    assert not se.is_write_like("read") and not se.is_destructive_like("interactive")
    assert se.class_of("write-shared") == "write"
    assert se.class_of("destructive-local") == "destructive"
    assert se.class_of("bogus") is None


def _p(leaf: str, **ann: str) -> se.DryRunPolicy:
    return se.resolve_dry_run_policy(leaf, ann)


def test_dry_run_table() -> None:
    P = se.DryRunPolicy
    assert _p("x", **{"kit/side-effect": "write"}) is P.ALLOW
    assert _p("x", **{"kit/side-effect": "destructive-shared"}) is P.ALLOW
    assert _p("x", **{"kit/side-effect": "read"}) is P.NOOP
    assert _p("x", **{"kit/side-effect": "interactive"}) is P.REJECT_INTERACTIVE
    assert _p("x") is P.REJECT_UNTAGGED
    assert _p("x", **{"kit/side-effect": ""}) is P.REJECT_UNTAGGED
    assert _p("x", **{"kit/side-effect": "bogus"}) is P.REJECT_MALFORMED
    assert _p("x", **{"kit/side-effect": "write", "kit/dry-run": "opted-out"}) is P.REJECT_OPT_OUT
    assert _p("x", **{"kit/dry-run": "supported"}) is P.ALLOW


def test_dry_run_previews_an_inferred_destructive_leaf() -> None:
    """An unannotated destructive-named leaf previews like a declared
    destructive-shared one; it is not refused as untagged."""
    P = se.DryRunPolicy
    assert _p("delete") is P.ALLOW
    assert _p("rm", **{"kit/side-effect": ""}) is P.ALLOW
    # explicit wins: a malformed tag is still refused as malformed
    assert _p("purge", **{"kit/side-effect": "bogus"}) is P.REJECT_MALFORMED
    # opt-out still wins over inference
    assert _p("delete", **{"kit/dry-run": "opted-out"}) is P.REJECT_OPT_OUT


def test_dry_run_refusals_are_usage_errors() -> None:
    err = se.dry_run_refusal("kitapp shell", {}, se.DryRunPolicy.REJECT_INTERACTIVE)
    assert err is not None
    assert err.code == "USAGE" and err.exit_code == 2
    assert err.message.startswith('"kitapp shell" does not accept --dry-run: interactive')
    err = se.dry_run_refusal(
        "kitapp sync",
        {"kit/dry-run-rationale": "talks to a remote"},
        se.DryRunPolicy.REJECT_OPT_OUT,
    )
    assert err is not None
    assert "explicitly opted out" in err.message
    assert err.message.endswith("(talks to a remote)")
    assert se.dry_run_refusal("t ls", {}, se.DryRunPolicy.NOOP) is None
    assert se.dry_run_refusal("t ls", {}, se.DryRunPolicy.ALLOW) is None
    err = se.dry_run_refusal("t x", {}, se.DryRunPolicy.REJECT_UNTAGGED)
    assert err is not None and "missing the required kit/side-effect tag" in err.message
    err = se.dry_run_refusal("t x", {SE: "bogus"}, se.DryRunPolicy.REJECT_MALFORMED)
    assert err is not None and '"bogus"' in err.message


def test_annotate_decorator_round_trips() -> None:
    @se.annotate(side_effect="destructive", destructive_token=True, dry_run="opted-out")
    def f() -> None: ...

    assert se.annotations_of(f) == {
        "kit/side-effect": "destructive",
        "kit/destructive-token": "required",
        "kit/dry-run": "opted-out",
    }
    assert se.annotations_of(lambda: None) == {}

    @se.annotate({"kit/permissions": "fs"}, side_effect="write")
    def g() -> None: ...

    assert se.annotations_of(g) == {"kit/permissions": "fs", "kit/side-effect": "write"}


def test_is_dry_run_defaults_false_and_scopes() -> None:
    assert se.is_dry_run() is False
    with se.dry_run_scope(True):
        assert se.is_dry_run() is True
    assert se.is_dry_run() is False
