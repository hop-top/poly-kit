"""The mount-level audit hook: one record per refusal the surface makes.

Go audits every refusal its MCP surface decides before adopter code runs
through the bridge's sinks (``cmdsurface.Bridge.Audit``). This port
carries no sink fan-out, so a mount takes one callable instead,
``mount_mcp(bridge, audit=fn)``, and calls it synchronously with an
:class:`AuditRecord` after each refusal is decided and before the
response is returned.

Best-effort by contract: the hook never affects the wire response. An
exception it raises is swallowed, and nothing is retried.

``error`` carries the message of the Go sentinel the same event is
audited with, verbatim, so one sink can match records from every port.
"""

from __future__ import annotations

import contextlib
from collections.abc import Callable, Sequence
from dataclasses import dataclass, field
from datetime import UTC, datetime

#: ``cmdsurface.ErrAuthRefused``: a ``kit/auth-required`` leaf called by
#: a caller the mount's verifier did not establish.
ERR_AUTH_REFUSED = "cmdsurface: authentication refused"
#: ``mcpsdk.ErrConfirmationRequired``: nothing satisfied the confirmation
#: gate (no ``X-Confirm-Token``, and no elicitation the client can
#: answer).
ERR_CONFIRMATION_REQUIRED = "mcpsdk: confirmation required"
#: ``mcpsdk.ErrConfirmationDeclined``: the client's user declined or
#: dismissed the confirmation question.
ERR_CONFIRMATION_DECLINED = "mcpsdk: confirmation declined"
#: ``mcpsdk.ErrConfirmationStateRejected``: a presented ``requestState``
#: failed verification. Never honoured; the question is asked again.
ERR_CONFIRMATION_STATE_REJECTED = "mcpsdk: confirmation requestState failed verification"
#: ``cmdsurface.ErrDestructiveBlocked``: the leaf's destructiveness
#: exceeds the mount policy's ceiling for the MCP surface.
ERR_DESTRUCTIVE_BLOCKED = "cmdsurface: destructive command blocked on this surface"

#: The ``extra`` key every record carries, as Go's MCP surfaces stamp it.
EXTRA_SPEC_VERSION = "mcp_spec_version"
#: The ``extra`` key and value of a rejected ``requestState``.
EXTRA_CONFIRM_REJECTION = "mcp_confirm_rejection"
CONFIRM_REJECTION_STATE_VERIFICATION_FAILED = "request_state_verification_failed"


@dataclass(frozen=True, kw_only=True)
class AuditRecord:
    """One refusal, as the hook receives it.

    The subset of Go's ``cmdsurface.Invocation`` a pre-flight refusal
    carries, plus the error: no caller, arguments or flags, so there is
    nothing to redact.
    """

    #: The resolved leaf's path segments. Only resolved leaves are audited.
    path: tuple[str, ...]
    #: Always ``"mcp"``.
    surface: str = "mcp"
    #: When the refusal was decided; timezone-aware UTC.
    requested_at: datetime
    #: The protocol revision serving the request.
    spec_version: str
    #: The Go sentinel's message for the event, verbatim.
    error: str
    #: ``mcp_spec_version`` always; ``mcp_confirm_rejection`` for a
    #: rejected ``requestState``.
    extra: dict[str, str] = field(default_factory=dict)


#: What a mount's ``audit`` option is: called once per refusal.
AuditHook = Callable[[AuditRecord], None]


def emit_audit(
    hook: AuditHook | None,
    *,
    path: Sequence[str],
    spec_version: str,
    error: str,
    extra: dict[str, str] | None = None,
) -> None:
    """Build one record and hand it to ``hook``, swallowing any exception.

    A ``None`` hook is the unmounted default and costs nothing. ``extra``
    is laid over the spec-version entry every record carries.
    """
    if hook is None:
        return
    bag = {EXTRA_SPEC_VERSION: spec_version}
    if extra:
        bag.update(extra)
    record = AuditRecord(
        path=tuple(path),
        requested_at=datetime.now(UTC),
        spec_version=spec_version,
        error=error,
        extra=bag,
    )
    # Best-effort by contract: a sink that raises never reaches the wire.
    with contextlib.suppress(Exception):
        hook(record)
