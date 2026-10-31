"""sideeffect — the one reader of ``kit/side-effect``.

Ports Go ``console/cli/sideeffect.go``, ``cmdmeta/destructive.go`` and the
dry-run table of ``global_dryrun.go``. Every place the SDK reads the
annotation (the delegation gate in :mod:`hop_top_kit.gate`, the MCP
surface gate in :mod:`hop_top_kit.mcp.safety`) resolves through
:func:`effective_side_effect`; none keeps its own name table.

Tiers
-----
Two generations coexist: the legacy ``read|write|destructive|interactive``
and the expanded ``read|write-local|write-shared|destructive-local|
destructive-shared|interactive``. The ``*-like`` predicates treat the
local and shared variants as their legacy class.

Inference
---------
A leaf whose ``kit/side-effect`` is absent or empty and whose name is in
:data:`DESTRUCTIVE_NAMES` is ``destructive-shared`` (:data:`INFERRED_TIER`).
An explicit annotation, valid or malformed, is never inferred over.
"""

from __future__ import annotations

import contextvars
from collections.abc import Callable, Iterator, Mapping
from contextlib import contextmanager
from enum import Enum
from typing import TypeVar

from hop_top_kit.output.error import CLIError, usage_error

SIDE_EFFECT = "kit/side-effect"
DRY_RUN = "kit/dry-run"
DRY_RUN_RATIONALE = "kit/dry-run-rationale"
DESTRUCTIVE_TOKEN = "kit/destructive-token"
PERMISSIONS = "kit/permissions"

#: Leaf names kit treats as destructive when the command carries no
#: ``kit/side-effect``. The only copy in the SDK.
DESTRUCTIVE_NAMES = frozenset({"delete", "remove", "rm", "destroy", "purge", "drop"})

#: The tier an inferred-destructive leaf is gated as.
INFERRED_TIER = "destructive-shared"

_WRITE = frozenset({"write", "write-local", "write-shared"})
_DESTRUCTIVE = frozenset({"destructive", "destructive-local", "destructive-shared"})
_CLASS = {
    "read": "read",
    "interactive": "interactive",
    "write": "write",
    "write-local": "write",
    "write-shared": "write",
    "destructive": "destructive",
    "destructive-local": "destructive",
    "destructive-shared": "destructive",
}


def is_write_like(tier: str | None) -> bool:
    return tier in _WRITE


def is_destructive_like(tier: str | None) -> bool:
    return tier in _DESTRUCTIVE


def class_of(tier: str) -> str | None:
    """Legacy class of ``tier``; ``None`` for a value outside the vocabulary."""
    return _CLASS.get(tier)


def is_destructive_name(name: str) -> bool:
    return name in DESTRUCTIVE_NAMES


def inferred_destructive(leaf_name: str, declared: str | None) -> bool:
    """Whether a leaf is destructive by name alone (declared absent or empty)."""
    return not declared and leaf_name in DESTRUCTIVE_NAMES


def effective_side_effect(leaf_name: str, annotations: Mapping[str, str] | None) -> str | None:
    """The tier the gates act on: declared, else inferred, else ``None``."""
    declared = (annotations or {}).get(SIDE_EFFECT)
    if inferred_destructive(leaf_name, declared):
        return INFERRED_TIER
    return declared


# -- dry-run -----------------------------------------------------------------


class DryRunPolicy(Enum):
    ALLOW = "allow"
    NOOP = "noop"
    REJECT_INTERACTIVE = "reject-interactive"
    REJECT_OPT_OUT = "reject-opt-out"
    REJECT_UNTAGGED = "reject-untagged"
    REJECT_MALFORMED = "reject-malformed"


def resolve_dry_run_policy(leaf_name: str, annotations: Mapping[str, str] | None) -> DryRunPolicy:
    """What ``--dry-run`` does on a leaf.

    Opted-out refuses; legacy ``supported`` allows; write and destructive
    tiers allow; read is a silent no-op; interactive refuses; untagged and
    malformed refuse. An inferred-destructive leaf resolves as its inferred
    tier (allow) rather than untagged; the gate previews it itself and does
    not invoke its handler (see :mod:`hop_top_kit.gate`).
    """
    ann = annotations or {}
    flag = ann.get(DRY_RUN)
    if flag == "opted-out":
        return DryRunPolicy.REJECT_OPT_OUT
    if flag == "supported":
        return DryRunPolicy.ALLOW
    tier = effective_side_effect(leaf_name, ann)
    if not tier:
        return DryRunPolicy.REJECT_UNTAGGED
    if is_write_like(tier) or is_destructive_like(tier):
        return DryRunPolicy.ALLOW
    if tier == "read":
        return DryRunPolicy.NOOP
    if tier == "interactive":
        return DryRunPolicy.REJECT_INTERACTIVE
    return DryRunPolicy.REJECT_MALFORMED


def dry_run_refusal(
    path: str, annotations: Mapping[str, str] | None, policy: DryRunPolicy
) -> CLIError | None:
    """The USAGE envelope refusing ``--dry-run`` under ``policy``, or ``None``."""
    ann = annotations or {}
    if policy is DryRunPolicy.REJECT_INTERACTIVE:
        msg = (
            f'"{path}" does not accept --dry-run: interactive sessions have no batch '
            "boundary to scope the preview"
        )
        fix = "run without --dry-run"
    elif policy is DryRunPolicy.REJECT_OPT_OUT:
        msg = (
            f'"{path}" does not support --dry-run: the command explicitly opted out '
            "via kit/dry-run: opted-out"
        )
        if why := ann.get(DRY_RUN_RATIONALE):
            msg += f" ({why})"
        fix = "run without --dry-run; see the command's documentation for the rationale"
    elif policy is DryRunPolicy.REJECT_UNTAGGED:
        msg = (
            f'"{path}" cannot apply --dry-run: the command is missing the required '
            "kit/side-effect tag"
        )
        fix = "adopter must declare the tier with hop_top_kit.sideeffect.annotate(side_effect=...)"
    elif policy is DryRunPolicy.REJECT_MALFORMED:
        msg = (
            f'"{path}" cannot apply --dry-run: the command has a malformed '
            f'kit/side-effect tag "{ann.get(SIDE_EFFECT, "")}" that names no tier'
        )
        fix = "adopter must declare one of the kit side-effect tiers"
    else:
        return None
    err = usage_error(msg)
    err.suggested_fix = fix
    return err


_dry_run: contextvars.ContextVar[bool] = contextvars.ContextVar("kit_dry_run", default=False)


def is_dry_run() -> bool:
    """Whether kit applied ``--dry-run`` to the running leaf (Go ``sideeffect.IsDryRun``)."""
    return _dry_run.get()


@contextmanager
def dry_run_scope(on: bool) -> Iterator[None]:
    tok = _dry_run.set(on)
    try:
        yield
    finally:
        _dry_run.reset(tok)


# -- annotating Typer / plain callbacks --------------------------------------

_ATTR = "__kit_annotations__"
F = TypeVar("F", bound=Callable[..., object])


def annotate(
    annotations: Mapping[str, str] | None = None,
    *,
    side_effect: str | None = None,
    dry_run: str | None = None,
    dry_run_rationale: str | None = None,
    destructive_token: bool = False,
    permissions: str | None = None,
) -> Callable[[F], F]:
    """Attach ``kit/*`` annotations to a command callback.

    Apply beneath ``@app.command()``. ``destructive_token=True`` makes the
    leaf require ``--confirm-token``.
    """
    ann: dict[str, str] = dict(annotations or {})
    if side_effect is not None:
        ann[SIDE_EFFECT] = side_effect
    if dry_run is not None:
        ann[DRY_RUN] = dry_run
    if dry_run_rationale is not None:
        ann[DRY_RUN_RATIONALE] = dry_run_rationale
    if destructive_token:
        ann[DESTRUCTIVE_TOKEN] = "required"
    if permissions is not None:
        ann[PERMISSIONS] = permissions

    def deco(fn: F) -> F:
        setattr(fn, _ATTR, {**getattr(fn, _ATTR, {}), **ann})
        return fn

    return deco


def annotations_of(fn: object) -> dict[str, str]:
    """The annotations :func:`annotate` attached to ``fn`` (empty if none)."""
    return dict(getattr(fn, _ATTR, {}) or {})
