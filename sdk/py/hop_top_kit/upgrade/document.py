"""In-memory JSON document migrator.

A document is an object with an integer version at ``version_field``
(dotted, default ``version``). Steps move ``from_version -> to_version``;
the chain picks the step whose ``from_version`` is the current version
wherever it was given. Every step must move forward and stay at or below
the latest version, checked before any runs; the chain runs while the
version is below the latest (``DocOptions.latest``, or the highest
``to_version`` when 0). On any error the caller's input is unchanged.
"""

from __future__ import annotations

import copy
import json
from collections.abc import Callable
from dataclasses import dataclass, field
from decimal import Decimal
from typing import Any

from hop_top_kit.upgrade.migration import VerifyError

Doc = dict[str, Any]


@dataclass
class DocStep:
    """Moves a document from ``from_version`` to ``to_version`` (greater).

    *apply* gets a private copy of the document, may mutate it in place and
    returns the migrated document; the version field is set to
    ``to_version`` afterwards. *verify*, when set, runs on *apply*'s result
    and raises to fail.
    """

    from_version: int
    to_version: int
    apply: Callable[[Doc], Doc]
    verify: Callable[[Doc], None] | None = None
    note: str = ""


@dataclass
class DocOptions:
    """``version_field``: dotted path of the integer version, ``"version"``
    when empty. ``latest``: the version this build writes, 0 for the
    highest ``to_version``. ``missing_is_zero``: read a missing or
    non-integer version as 0 instead of failing."""

    version_field: str = ""
    latest: int = 0
    missing_is_zero: bool = False


@dataclass
class DocApplied:
    """One step a migration applied."""

    from_version: int
    to_version: int
    note: str = ""


@dataclass
class DocReport:
    """What a migration did."""

    from_version: int
    to_version: int
    applied: list[DocApplied] = field(default_factory=list)


class DocumentError(Exception):
    """Base of every document migration error."""


class DocNewerError(DocumentError):
    """The document is newer than the latest known version."""


class DocVersionError(DocumentError):
    """The document has no integer version."""


class DocGapError(DocumentError):
    """The step chain has a gap."""


class DocStepError(DocumentError):
    """A step failed; the cause is chained."""


class DocInvalidError(DocumentError):
    """The document or a step is invalid."""


class DocVerifyError(DocumentError, VerifyError):
    """A step's verify failed; the cause is chained."""


def _reject_constant(name: str) -> Any:
    raise ValueError(f"invalid number {name}")


def _decode(doc: bytes | str) -> Doc:
    try:
        text = doc.decode("utf-8") if isinstance(doc, bytes | bytearray) else doc
        # First value only, trailing data ignored: as the reference does.
        value, _ = json.JSONDecoder(
            parse_float=Decimal, parse_constant=_reject_constant
        ).raw_decode(text.lstrip(" \t\r\n"))
    except ValueError as e:  # includes UnicodeDecodeError
        raise DocInvalidError(f"parse document: {e}") from e
    if not isinstance(value, dict):
        raise DocInvalidError("document is not a JSON object")
    return value


def _encode(v: Any) -> str:
    if isinstance(v, dict):
        items = ",".join(f"{_encode(str(k))}:{_encode(v[k])}" for k in sorted(v))
        return "{" + items + "}"
    if isinstance(v, list | tuple):
        return "[" + ",".join(_encode(x) for x in v) + "]"
    if isinstance(v, Decimal):
        return str(v)
    return json.dumps(v, ensure_ascii=False, allow_nan=False)


def _version(doc: Doc, path: str) -> int | None:
    cur: Any = doc
    for seg in path.split("."):
        if not isinstance(cur, dict) or seg not in cur:
            return None
        cur = cur[seg]
    if isinstance(cur, int) and not isinstance(cur, bool):
        return cur
    return None


def _set_version(doc: Doc, path: str, v: int) -> None:
    segs = path.split(".")
    cur = doc
    for seg in segs[:-1]:
        nxt = cur.get(seg)
        if not isinstance(nxt, dict):
            nxt = {}
            cur[seg] = nxt
        cur = nxt
    cur[segs[-1]] = v


def _find(steps: list[DocStep], frm: int) -> DocStep | None:
    # Follows versions, not the order the steps were given in.
    return next((s for s in steps if s.from_version == frm), None)


def _migrate(doc: Doc, steps: list[DocStep], opts: DocOptions) -> tuple[Doc, DocReport]:
    """Run the chain on *doc*, which it owns."""
    path = opts.version_field or "version"
    cur = _version(doc, path)
    if cur is None:
        if not opts.missing_is_zero:
            raise DocVersionError(f"document has no integer version at {json.dumps(path)}")
        cur = 0
    latest = opts.latest
    if latest == 0:
        latest = max([0, *(s.to_version for s in steps)])
    report = DocReport(cur, cur)
    for s in steps:
        if s.to_version <= s.from_version:
            raise DocInvalidError(
                f"migration {s.from_version} -> {s.to_version} does not move forward"
            )
        if s.to_version > latest:
            raise DocInvalidError(
                f"migration {s.from_version} -> {s.to_version} goes past the latest known {latest}"
            )
    if cur > latest:
        raise DocNewerError(f"document version {cur} is newer than the latest known {latest}")
    while cur < latest:
        step = _find(steps, cur)
        if step is None:
            raise DocGapError(
                f"no migration from version {cur} (highest target is {latest}); chain has a gap"
            )
        where = f"migration {step.from_version} -> {step.to_version}"
        try:
            out = step.apply(doc)
        except Exception as e:
            raise DocStepError(f"{where}: {e}") from e
        if out is None:
            raise DocStepError(f"{where} returned nil map")
        if step.verify is not None:
            try:
                step.verify(out)
            except Exception as e:
                raise DocVerifyError(f"{where}: verify: {e}") from e
        doc = out
        _set_version(doc, path, step.to_version)
        cur = step.to_version
        report.to_version = cur
        report.applied.append(DocApplied(step.from_version, step.to_version, step.note))
    return doc, report


def migrate_document(
    doc: bytes | str, steps: list[DocStep], opts: DocOptions | None = None
) -> tuple[bytes, DocReport]:
    """Migrate one JSON document in memory: no I/O.

    When the document is already at the latest version the input bytes are
    returned as they are; otherwise the result is re-encoded, keys sorted
    and numbers kept exact. Raises a :class:`DocumentError`; the input is
    never modified.
    """
    opts = opts or DocOptions()
    raw = doc.encode() if isinstance(doc, str) else bytes(doc)
    out, report = _migrate(_decode(raw), steps, opts)
    if not report.applied:
        return raw, report
    try:
        return _encode(out).encode(), report
    except (TypeError, ValueError) as e:
        raise DocInvalidError(f"encode document: {e}") from e


def migrate_document_map(
    doc: Doc, steps: list[DocStep], opts: DocOptions | None = None
) -> tuple[Doc, DocReport]:
    """:func:`migrate_document` on a decoded document. The caller's map is
    never modified; steps work on a deep copy."""
    if not isinstance(doc, dict):
        raise DocInvalidError("document is not a JSON object")
    return _migrate(copy.deepcopy(doc), steps, opts or DocOptions())
