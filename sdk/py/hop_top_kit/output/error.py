"""Structured-error envelope — mirrors ``go/console/output/error.go``.

When a command fails under ``--format json|yaml``, the error is
materialized as a structured :class:`CLIError` and rendered to stderr by
:func:`render_error`. Plaintext mode (``--format table`` or unset) prints
``"Code: Message\\nFix: ...\\n"`` so the human-readable behavior matches
the Go kit output.

Wire keys are snake_case (``code``, ``message``, ``cause``,
``suggested_fix``, ``alternatives``, ``exit_code``, ``transience``);
empty optional fields are omitted, mirroring Go's ``omitempty``.
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field, replace
from typing import IO, Any

import yaml

# ---------------------------------------------------------------------------
# Transience classes
# ---------------------------------------------------------------------------

#: Marks a failure a retry may clear (rate limit, timeout, upstream blip).
TRANSIENCE_TRANSIENT = "transient"
#: Marks a failure retrying cannot clear without changing the input or
#: the environment.
TRANSIENCE_PERMANENT = "permanent"
#: Marks a failure kit cannot classify. Agents should treat retries as
#: best-effort and bounded.
TRANSIENCE_UNKNOWN = "unknown"

# ---------------------------------------------------------------------------
# Standard codes mapping the cross-tool exit codes (conventions §8.1)
# ---------------------------------------------------------------------------

CODE_OK = "OK"  # exit 0
CODE_GENERIC = "GENERIC"  # exit 1
CODE_USAGE = "USAGE"  # exit 2
CODE_NOT_FOUND = "NOT_FOUND"  # exit 3
CODE_CONFLICT = "CONFLICT"  # exit 4
CODE_UNAUTHORIZED = "UNAUTHORIZED"  # exit 5
CODE_TRANSIENT = "TRANSIENT"  # exit 6 — Factor-11 transient/retryable failure
CODE_PROVENANCE_MISSING = "PROVENANCE_MISSING"  # exit 65 — Factor-12 strict-mode refusal
CODE_RATE_LIMITED = "RATE_LIMITED"  # exit 64 — Factor-10 max-ops budget exceeded

#: Spec-assigned exit code for the generic failure class: the command
#: failed and no narrower code applies. Pair it with :func:`generic_error`
#: rather than hand-rolling exit 1, so the envelope carries a transience
#: class.
EXIT_GENERIC = 1
#: Spec-assigned exit code for transient/retryable failures (Factor 11).
#: Agents branch on it before parsing stderr: exit 6 means a retry may
#: clear the failure.
EXIT_TRANSIENT = 6
#: Conventional exit code for Factor-10 rate-limit refusals.
EXIT_RATE_LIMITED = 64
#: Conventional exit code for Factor-12 strict-mode provenance refusals.
#: Lives at 65 in kit's extension band (alongside RATE_LIMITED at 64):
#: the spec reserves 0-6 for its core taxonomy and leaves >6 to per-tool
#: codes, and kit as a library stays out of the low per-tool range.
EXIT_PROVENANCE_MISSING = 65


def transience_for_code(code: str) -> str:
    """Return the default transience class for a standard code.

    Unrecognized (adopter-defined) codes map to
    :data:`TRANSIENCE_UNKNOWN`; adopters set ``CLIError.transience`` (or
    use :meth:`CLIError.with_transience`) to classify their own codes.
    """
    if code in (
        CODE_USAGE,
        CODE_NOT_FOUND,
        CODE_CONFLICT,
        CODE_UNAUTHORIZED,
        CODE_PROVENANCE_MISSING,
    ):
        return TRANSIENCE_PERMANENT
    if code in (CODE_RATE_LIMITED, CODE_TRANSIENT):
        return TRANSIENCE_TRANSIENT
    return TRANSIENCE_UNKNOWN


@dataclass
class CLIError(Exception):
    """Structured-error envelope rendered to stderr for ``json``/``yaml``.

    ``transience`` classifies the failure for retry decisions (Factor 4):
    :data:`TRANSIENCE_TRANSIENT` (retry-worthy),
    :data:`TRANSIENCE_PERMANENT` (do not retry), or
    :data:`TRANSIENCE_UNKNOWN`. Constructors and :func:`wrap_error`
    populate it; :func:`render_error` normalizes an unset value to
    :data:`TRANSIENCE_UNKNOWN` so every structured error carries a valid
    class on the wire.
    """

    code: str = ""
    message: str = ""
    cause: str = ""
    suggested_fix: str = ""
    alternatives: list[str] = field(default_factory=list)
    exit_code: int = 0
    transience: str = ""

    def __post_init__(self) -> None:
        # Populate Exception.args so pickling / repr behave.
        super().__init__(str(self))

    def __str__(self) -> str:
        if not self.code:
            return self.message
        return f"{self.code}: {self.message}"

    def with_transience(self, transience: str) -> CLIError:
        """Return a copy with ``transience`` set, other fields untouched.

        Copies rather than mutating: adopters commonly share module-level
        envelopes, and writing to one would leak across call sites.
        """
        return replace(self, transience=transience)

    def to_dict(self) -> dict[str, Any]:
        """Wire form: snake_case keys, empty optional fields omitted."""
        d: dict[str, Any] = {"code": self.code, "message": self.message}
        if self.cause:
            d["cause"] = self.cause
        if self.suggested_fix:
            d["suggested_fix"] = self.suggested_fix
        if self.alternatives:
            d["alternatives"] = list(self.alternatives)
        d["exit_code"] = self.exit_code
        if self.transience:
            d["transience"] = self.transience
        return d


def wrap_error(err: BaseException | None, code: str, exit_code: int) -> CLIError | None:
    """Build an envelope from *err*, retaining it as ``__cause__``.

    Transience defaults from the code via :func:`transience_for_code`;
    use :meth:`CLIError.with_transience` to override. Returns ``None``
    for ``None`` input, mirroring Go's ``WrapError(nil, ...)``.
    """
    if err is None:
        return None
    e = CLIError(
        code=code,
        message=str(err),
        exit_code=exit_code,
        transience=transience_for_code(code),
    )
    e.__cause__ = err
    return e


def generic_error(message: str) -> CLIError:
    """CODE_GENERIC envelope with exit code 1.

    The catch-all for failures no narrower code describes; permanent
    because retrying the same input in the same environment is not
    expected to help. Wrapping an arbitrary error as CODE_GENERIC via
    :func:`wrap_error` still defaults to :data:`TRANSIENCE_UNKNOWN`.
    """
    return CLIError(
        code=CODE_GENERIC,
        message=message,
        exit_code=EXIT_GENERIC,
        transience=TRANSIENCE_PERMANENT,
    )


def not_found_error(message: str) -> CLIError:
    """CODE_NOT_FOUND envelope with exit code 3."""
    return CLIError(
        code=CODE_NOT_FOUND, message=message, exit_code=3, transience=TRANSIENCE_PERMANENT
    )


def conflict_error(message: str) -> CLIError:
    """CODE_CONFLICT envelope with exit code 4."""
    return CLIError(
        code=CODE_CONFLICT, message=message, exit_code=4, transience=TRANSIENCE_PERMANENT
    )


def unauthorized_error(message: str) -> CLIError:
    """CODE_UNAUTHORIZED envelope with exit code 5."""
    return CLIError(
        code=CODE_UNAUTHORIZED, message=message, exit_code=5, transience=TRANSIENCE_PERMANENT
    )


def usage_error(message: str) -> CLIError:
    """CODE_USAGE envelope with exit code 2."""
    return CLIError(code=CODE_USAGE, message=message, exit_code=2, transience=TRANSIENCE_PERMANENT)


def transient_error(message: str) -> CLIError:
    """CODE_TRANSIENT envelope with exit code 6 (Factor 11).

    Use it for failures a retry may clear: upstream timeouts, connection
    resets, service-unavailable responses.
    """
    return CLIError(
        code=CODE_TRANSIENT,
        message=message,
        exit_code=EXIT_TRANSIENT,
        transience=TRANSIENCE_TRANSIENT,
    )


def rate_limited_error(message: str) -> CLIError:
    """CODE_RATE_LIMITED envelope with exit code 64 (Factor 10)."""
    return CLIError(
        code=CODE_RATE_LIMITED,
        message=message,
        exit_code=EXIT_RATE_LIMITED,
        transience=TRANSIENCE_TRANSIENT,
    )


def provenance_missing_error(detail: str) -> CLIError:
    """CODE_PROVENANCE_MISSING envelope with exit code 65 (Factor 12).

    *detail* is a free-form string suitable for the ``cause`` slot
    (typically the JSON-pointer list of offending fields).
    """
    return CLIError(
        code=CODE_PROVENANCE_MISSING,
        message="provenance not recorded for one or more output fields",
        cause=detail,
        suggested_fix="record provenance for synthesized/cached fields "
        "before rendering (see hop_top_kit.provenance)",
        exit_code=EXIT_PROVENANCE_MISSING,
        transience=TRANSIENCE_PERMANENT,
    )


def render_error(w: IO[str], format: str, err: CLIError | None) -> None:
    """Write *err* to *w* in the requested format.

    ``format == ""`` or ``"table"`` renders human-readable plain text
    (``"Code: Message\\nFix: ..."``); ``json``/``yaml`` render the
    envelope structurally. An unset transience is normalized to
    :data:`TRANSIENCE_UNKNOWN` on the wire (Factor 4) without mutating
    *err*. Always returns; the caller decides the exit code from
    ``err.exit_code`` after rendering.
    """
    if err is None:
        return
    if not err.transience:
        err = err.with_transience(TRANSIENCE_UNKNOWN)
    if format == "json":
        json.dump(err.to_dict(), w, indent=2)
        w.write("\n")
        return
    if format == "yaml":
        yaml.safe_dump(err.to_dict(), w, default_flow_style=False, sort_keys=False)
        return
    _render_plain(w, err)


def _render_plain(w: IO[str], err: CLIError) -> None:
    """Human-readable form used by ``--format table`` / empty format.

    Each populated field appears on its own line so the output is
    grep-friendly.
    """
    if err.code:
        w.write(f"{err.code}: {err.message}\n")
    else:
        w.write(f"{err.message}\n")
    if err.cause:
        w.write(f"Cause: {err.cause}\n")
    if err.suggested_fix:
        w.write(f"Fix: {err.suggested_fix}\n")
    for alt in err.alternatives:
        w.write(f"Alternative: {alt}\n")
