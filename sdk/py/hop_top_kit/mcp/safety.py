"""Surface-exposure policy — the port of Go's ``Policy.Allowed``.

This is **not** ``hop_top_kit.safety``. That module implements Factor 10
delegation safety: a CLI-time ``--force`` check keyed on TTY-ness, which
decides whether a human at a terminal may run a dangerous command. It has
nothing to say about which *transports* may reach a command at all.

This module is the transport-side gate, ported from
``go/transport/cmdsurface/safety.go``. It answers one question: may
surface *S* invoke a leaf whose annotations classify it as *C*? The
default answer for destructive leaves on every remote surface is no.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from enum import StrEnum

#: Cobra annotation keys the bridge reads, matching the canonical ``kit/``
#: vocabulary registered by the Go console package.
ANN_SIDE_EFFECT = "kit/side-effect"
ANN_AUTH_REQUIRED = "kit/auth-required"
ANN_EXIT_CODES = "kit/exit-codes"
ANN_PERMISSIONS = "kit/permissions"
ANN_REQUIRES_CONFIRM = "kit/requires-confirmation"

#: ``kit/side-effect`` values that classify a leaf as destructive.
DESTRUCTIVE_TIERS = frozenset({"destructive", "destructive-local", "destructive-shared"})


class Surface(StrEnum):
    """The transports a leaf can be exposed on."""

    CLI = "cli"
    LIB = "lib"
    MCP = "mcp"
    REST = "rest"
    RPC = "rpc"
    WS = "ws"
    SSE = "sse"
    BUS = "bus"
    CRON = "cron"
    WEBHOOK = "webhook"


#: Surfaces that run in the local process and are therefore never gated.
LOCAL_SURFACES = frozenset({Surface.CLI, Surface.LIB})


@dataclass(frozen=True)
class SafetyClass:
    """A leaf's safety annotations, as read by the bridge.

    The input the policy gate consults. A leaf with no annotations at all
    yields the zero value: read-only, no auth, no confirmation.
    """

    destructive: bool = False
    auth_required: bool = False
    requires_confirmation: bool = False
    permissions: tuple[str, ...] = ()
    exit_codes: tuple[str, ...] = ()


def _split_csv(value: str | None) -> tuple[str, ...]:
    """Parse a comma-separated annotation, dropping blanks.

    Returns the empty tuple for a missing or all-blank value so an absent
    annotation is indistinguishable from an explicitly empty list, which
    is what Go's ``splitCSV`` does by returning nil for both.
    """
    if not value:
        return ()
    return tuple(part.strip() for part in value.split(",") if part.strip())


def classify(annotations: dict[str, str] | None) -> SafetyClass:
    """Read ``annotations`` into a :class:`SafetyClass`.

    ``None`` or an empty mapping yields the zero value, matching Go's
    treatment of a nil ``cmd.Annotations``.
    """
    if not annotations:
        return SafetyClass()
    return SafetyClass(
        destructive=annotations.get(ANN_SIDE_EFFECT) in DESTRUCTIVE_TIERS,
        auth_required=annotations.get(ANN_AUTH_REQUIRED) == "true",
        requires_confirmation=annotations.get(ANN_REQUIRES_CONFIRM) == "true",
        permissions=_split_csv(annotations.get(ANN_PERMISSIONS)),
        exit_codes=_split_csv(annotations.get(ANN_EXIT_CODES)),
    )


@dataclass
class Policy:
    """Gates which surface may invoke a leaf of a given safety class.

    ``allow_destructive_on`` is the destructive ceiling: the surfaces on
    which destructive leaves may be invoked. It is **empty by default**,
    and empty means block-all — not allow-all. That inversion is the
    whole point of the default, so it is spelled out rather than left to
    a falsy-check reader's intuition.
    """

    #: Surfaces on which destructive leaves may be invoked. CLI and LIB
    #: are always allowed regardless. Empty = block every remote
    #: destructive invocation.
    allow_destructive_on: tuple[Surface, ...] = ()
    #: Surfaces a leaf is exposed on when its per-command config omits
    #: the enabled field.
    default_enabled: tuple[Surface, ...] = ()

    def allowed(self, cls: SafetyClass, surface: Surface) -> bool:
        """Report whether ``surface`` may invoke a leaf classified ``cls``.

        1. ``CLI`` and ``LIB`` are always allowed (local runtime).
        2. Non-destructive leaves are allowed on every other surface.
        3. Destructive leaves are allowed only where
           :attr:`allow_destructive_on` names the surface.

        Surface *enablement* (the per-leaf opt-in) is a separate gate on
        the bridge; this only enforces the destructive ceiling.
        """
        if surface in LOCAL_SURFACES:
            return True
        if not cls.destructive:
            return True
        return surface in self.allow_destructive_on

    def resolved_defaults(self) -> tuple[Surface, ...]:
        """Return :attr:`default_enabled`, or the package-wide fallback."""
        if self.default_enabled:
            return self.default_enabled
        return (Surface.CLI, Surface.LIB, Surface.MCP)


def default_policy() -> Policy:
    """The conservative default: no remote surface may invoke destructive.

    Mirrors Go's ``DefaultPolicy()`` exactly — default enablement is
    CLI + LIB + MCP, and ``allow_destructive_on`` stays empty.
    """
    return Policy(default_enabled=(Surface.CLI, Surface.LIB, Surface.MCP))


@dataclass
class SafetyError(Exception):
    """Base for the bridge's gate refusals."""

    message: str = field(default="")

    def __str__(self) -> str:
        return self.message


class UnknownCommandError(SafetyError):
    """The invocation path does not resolve to a leaf."""


class SurfaceNotEnabledError(SafetyError):
    """The leaf is not exposed on the invoking surface."""


class DestructiveBlockedError(SafetyError):
    """The policy gate refused a destructive leaf on this surface."""
