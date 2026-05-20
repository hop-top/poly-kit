"""hop_top_kit.id._core — implementation guts.

Thin wrapper around the upstream ``typeid-python`` package, exposing the
cross-language kit API SHAPE defined in ADR 0001:

    new(prefix) -> str
    parse(s)    -> Parsed(prefix: str, uuid: uuid.UUID)
    Typed[P]    — generic phantom-typed string alias
    TypeId(...) — Pydantic v2 annotation (re-exported)

URI composition is intentionally **not** part of this module. Callers
that need a poly-uri form should call ``hop_top_kit.uri`` (which in turn
delegates to the ``hop-top-uri`` package) with the canonical TypeID
string returned by :func:`new` / :func:`parse`.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Generic, TypeVar
from uuid import UUID

from typeid import TypeID
from typeid.core.errors import (
    InvalidTypeIDStringException,
    PrefixValidationException,
    SuffixValidationException,
    TypeIDException,
)

__all__ = (
    "IdError",
    "InvalidPrefixError",
    "InvalidSuffixError",
    "Parsed",
    "PrefixMismatchError",
    "Typed",
    "new",
    "parse",
)


# ---------------------------------------------------------------------------
# Errors
# ---------------------------------------------------------------------------


class IdError(Exception):
    """Base exception for any TypeID-related failure in kit.

    All concrete failures raised by this module are subclasses of
    :class:`IdError`. Callers wanting to catch every typeid problem in
    one shot should ``except IdError`` and let subclass-specific
    handling happen further out.
    """


class InvalidPrefixError(IdError):
    """The prefix segment failed grammar / length validation.

    Spec: ``^[a-z]([a-z0-9_]*[a-z0-9])?$`` and max 63 characters
    (see ADR 0001 § Spec pin).
    """


class InvalidSuffixError(IdError):
    """The suffix segment is not a valid 26-char Crockford base32 UUIDv7.

    Either the string itself is malformed or it does not split into a
    parseable ``<prefix>_<suffix>`` form.
    """


class PrefixMismatchError(IdError):
    """The parsed prefix does not match an expected / declared prefix.

    Raised by Pydantic-validated fields (via the upstream
    ``TypeIDField`` integration) and by any kit caller that re-checks
    prefix expectations after :func:`parse`.
    """


# ---------------------------------------------------------------------------
# Public dataclass returned by parse()
# ---------------------------------------------------------------------------


@dataclass(frozen=True, slots=True)
class Parsed:
    """Round-trip view of a parsed TypeID.

    Attributes:
        prefix: The prefix portion, or ``""`` for a prefix-less TypeID.
        uuid:   The decoded UUIDv7 as a standard-library
                :class:`uuid.UUID`. We deliberately surface the stdlib
                type (not ``uuid_utils.UUID``) so callers don't need
                ``uuid-utils`` in their own type signatures.
    """

    prefix: str
    uuid: UUID


# ---------------------------------------------------------------------------
# Typed[P] — phantom-typed canonical-string alias.
# ---------------------------------------------------------------------------


P = TypeVar("P", bound=str)


class Typed(Generic[P]):
    """Generic phantom-typed alias for a TypeID canonical string.

    ``Typed[P]`` is a *typing-time* annotation. At runtime it returns
    plain :class:`str` — values flowing through it are the canonical
    ``<prefix>_<suffix>`` form, which is what kit puts on the wire
    (per ADR 0001 § Wire form). This mirrors the Go ``TypeID[Prefix]``
    and Rust newtype patterns at the type-checker level without
    forcing adopters into a class allocation per ID.

    Usage::

        from typing import NewType
        from hop_top_kit.id import Typed, new

        TaskId = Typed[str]                 # bare phantom alias
        # or, for stronger checker-level distinction:
        # TaskId = NewType("TaskId", Typed[str])

        tid: TaskId = new("task")           # type: ignore[assignment]

    For Pydantic-validated typed fields (with runtime prefix check),
    use :class:`hop_top_kit.id.TypeId` instead.
    """

    # __class_getitem__ is inherited from Generic and already returns
    # a parameterised alias at runtime. We override only to make the
    # alias evaluate to ``str`` so isinstance-style checks work, while
    # keeping the static type informative.
    def __class_getitem__(cls, item: Any) -> Any:
        return super().__class_getitem__(item)  # type: ignore[misc]


# ---------------------------------------------------------------------------
# Internal: translate upstream exceptions to kit IdError subclasses.
# ---------------------------------------------------------------------------


def _translate(exc: Exception) -> IdError:
    """Map a ``typeid-python`` exception to the kit hierarchy."""

    msg = str(exc) or exc.__class__.__name__
    if isinstance(exc, PrefixValidationException):
        return InvalidPrefixError(msg)
    if isinstance(exc, SuffixValidationException):
        return InvalidSuffixError(msg)
    if isinstance(exc, InvalidTypeIDStringException):
        return InvalidSuffixError(msg)
    if isinstance(exc, TypeIDException):
        return IdError(msg)
    return IdError(msg)


# ---------------------------------------------------------------------------
# Public functions
# ---------------------------------------------------------------------------


def new(prefix: str) -> str:
    """Generate a new TypeID with the given prefix.

    Returns the canonical ``<prefix>_<suffix>`` string. The suffix is a
    fresh UUIDv7 encoded as 26-character Crockford base32, as defined
    by the TypeID v0.3 spec.

    Args:
        prefix: A valid TypeID prefix, matching
            ``^[a-z]([a-z0-9_]*[a-z0-9])?$`` and at most 63 chars.
            Pass ``""`` for a prefix-less TypeID (rare; bus payloads
            should always carry a prefix per ADR 0001).

    Raises:
        InvalidPrefixError: if ``prefix`` violates the grammar.
    """

    try:
        return str(TypeID(prefix=prefix or None))
    except (PrefixValidationException, SuffixValidationException) as exc:
        raise _translate(exc) from exc
    except TypeIDException as exc:
        raise _translate(exc) from exc


def parse(s: str) -> Parsed:
    """Parse a canonical TypeID string into its components.

    Args:
        s: A canonical TypeID string (``<prefix>_<suffix>`` or bare
            ``<suffix>``).

    Returns:
        A :class:`Parsed` with the prefix as a plain ``str`` (empty
        string when the input has no prefix) and the underlying UUIDv7
        as a stdlib :class:`uuid.UUID`.

    Raises:
        InvalidSuffixError: if the suffix is malformed or the string
            cannot be split into a valid (prefix, suffix) pair.
        InvalidPrefixError: if the prefix is structurally invalid.
    """

    try:
        tid = TypeID.from_string(s)
    except (
        InvalidTypeIDStringException,
        PrefixValidationException,
        SuffixValidationException,
    ) as exc:
        raise _translate(exc) from exc
    except TypeIDException as exc:
        raise _translate(exc) from exc

    # ``tid.uuid`` is a ``uuid_utils.UUID``; surface a stdlib UUID so
    # callers don't need ``uuid-utils`` in their own signatures. Use
    # ``.bytes`` (well-defined on both UUID flavours) for the convert.
    return Parsed(prefix=tid.prefix, uuid=UUID(bytes=tid.uuid_bytes))
