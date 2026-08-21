"""Go-compatible JSON serialisation for the MCP wire surface.

The parity contract (``sdk/tests/cross-lang/fixtures/mcp-wire.json``) is
byte-exact: a port's response bytes are compared to Go's without any
decode/re-encode step. Go's ``encoding/json`` and Python's ``json`` agree
on almost nothing by default, so this module pins the four differences
that actually show up on this surface.

Empirically verified against ``encoding/json`` (Go 1.25), not assumed:

1. **Key order.** Go sorts ``map[string]any`` keys lexicographically but
   emits *struct* fields in declaration order. The JSON-RPC envelope and
   the error object are structs in Go; every result body is a map. So
   the envelope is assembled positionally by :func:`encode_envelope`
   while bodies go through :func:`dumps` with ``sort_keys=True``.
2. **Separators.** Go emits no whitespace: ``(",", ":")``.
3. **Escaping.** Go's ``Encoder`` HTML-escapes ``<``, ``>``, ``&`` to
   ``\\u003c``/``\\u003e``/``\\u0026`` and escapes U+2028/U+2029, while
   leaving every other non-ASCII rune raw. Python does the opposite on
   both counts, hence ``ensure_ascii=False`` plus the fix-up below.
4. **Trailing newline.** Go's ``json.Encoder.Encode`` appends ``\\n``.

Whole floats are also normalised to integer syntax (Go prints ``1.0`` as
``1``); every number this surface emits is conceptually an integer.
"""

from __future__ import annotations

import json
import re
from typing import Any

#: Runes Go's encoder escapes that Python's does not.
_GO_ESCAPES = {
    "<": "\\u003c",
    ">": "\\u003e",
    "&": "\\u0026",
    "\u2028": "\\u2028",
    "\u2029": "\\u2029",
}

_GO_ESCAPE_RE = re.compile("[<>&\u2028\u2029]")

#: Trailing newline ``json.Encoder.Encode`` appends to every value.
NEWLINE = "\n"


def _escape(match: re.Match[str]) -> str:
    return _GO_ESCAPES[match.group(0)]


def _normalise(value: Any) -> Any:
    """Coerce whole floats to ints so Go's number syntax is reproduced.

    Go prints ``float64(1)`` as ``1``; Python prints ``1.0``. Every number
    on this surface (error codes, ``ttlMs``, exit codes) is an integer in
    Go, so folding whole floats down is lossless here and keeps a caller
    that hands us ``0.0`` from silently breaking byte-parity.
    """
    if isinstance(value, (bool, Raw)):
        return value
    if isinstance(value, Ordered):
        return Ordered([(k, _normalise(v)) for k, v in value.members])
    if isinstance(value, float) and value.is_integer():
        return int(value)
    if isinstance(value, dict):
        return {k: _normalise(v) for k, v in value.items()}
    if isinstance(value, (list, tuple)):
        return [_normalise(v) for v in value]
    return value


class Raw:
    """Pre-encoded JSON text emitted verbatim.

    Go models the JSON-RPC ``id`` as ``json.RawMessage``: whatever bytes
    the client sent come back unaltered, including ``null`` and number
    formatting a decode/re-encode round trip would normalise away. This
    wrapper is the Python equivalent — the raw source slice, carried
    through serialisation untouched.
    """

    __slots__ = ("text",)

    def __init__(self, text: str) -> None:
        self.text = text

    def __repr__(self) -> str:  # pragma: no cover - debugging aid
        return f"Raw({self.text!r})"

    def __eq__(self, other: object) -> bool:
        return isinstance(other, Raw) and other.text == self.text

    def __hash__(self) -> int:
        return hash(self.text)


class Ordered:
    """An object whose members keep declaration order instead of sorting.

    Go marshals *structs* in field-declaration order and *maps* in sorted
    key order. Most of this surface's bodies are maps, but a few — the
    JSON-RPC envelope and its error object — are structs in Go, and their
    member order is part of the byte contract. This wrapper carries that
    distinction across, so the serialiser does not have to guess which
    shape a given dict was on the Go side.
    """

    __slots__ = ("members",)

    def __init__(self, members: list[tuple[str, Any]]) -> None:
        self.members = members

    def __repr__(self) -> str:  # pragma: no cover - debugging aid
        return f"Ordered({self.members!r})"


def dumps(value: Any) -> str:
    """Serialise ``value`` the way Go's ``encoding/json`` would.

    Object keys are sorted, matching Go's ``map[string]any`` marshalling,
    except inside an :class:`Ordered` wrapper. No trailing newline:
    callers that need one add it, mirroring the ``Marshal`` / ``Encode``
    split in Go.
    """
    if isinstance(value, Raw):
        return value.text
    if isinstance(value, Ordered):
        body = ",".join(f"{dumps(k)}:{dumps(v)}" for k, v in value.members)
        return "{" + body + "}"
    text = json.dumps(
        _normalise(value),
        sort_keys=True,
        separators=(",", ":"),
        ensure_ascii=False,
        allow_nan=False,
    )
    return _GO_ESCAPE_RE.sub(_escape, text)


def encode_envelope(members: list[tuple[str, Any]]) -> bytes:
    """Encode a struct-shaped top-level object with the trailing newline.

    ``members`` is the ordered ``(key, value)`` list, already filtered for
    Go's ``omitempty`` semantics by the caller. Nested maps still sort;
    the newline is the one ``json.Encoder.Encode`` appends.
    """
    return (dumps(Ordered(members)) + NEWLINE).encode("utf-8")
