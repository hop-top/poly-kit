"""Shared wire primitives for both MCP eras.

Everything in here is vocabulary the two era modules agree on: the
JSON-RPC envelope shape, the error codes, the header and ``_meta`` key
names, and the request/response value objects the ASGI layer hands
around. Keeping it separate is what lets :mod:`~hop_top_kit.mcp.legacy`
stay provably untouched by modern-era changes — it imports names from
here and nothing from :mod:`~hop_top_kit.mcp.modern`.

Protocol constants come from the official ``mcp-types`` package rather
than being re-declared. That package already models the exact era split
kit needs (``HANDSHAKE_PROTOCOL_VERSIONS`` vs
``MODERN_PROTOCOL_VERSIONS``) and owns the reserved
``io.modelcontextprotocol/*`` key spellings, so re-typing them here
would only create a second source of truth to drift from.
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import Any

from mcp_types import (
    CLIENT_CAPABILITIES_META_KEY,
    CLIENT_INFO_META_KEY,
    HEADER_MISMATCH,
    INTERNAL_ERROR,
    INVALID_PARAMS,
    INVALID_REQUEST,
    METHOD_NOT_FOUND,
    MISSING_REQUIRED_CLIENT_CAPABILITY,
    PARSE_ERROR,
    PROTOCOL_VERSION_META_KEY,
    SERVER_INFO_META_KEY,
    UNSUPPORTED_PROTOCOL_VERSION,
)
from mcp_types.version import HANDSHAKE_PROTOCOL_VERSIONS, MODERN_PROTOCOL_VERSIONS

from ._json import Ordered, Raw, encode_envelope

# --- protocol revisions -------------------------------------------------

#: The handshake-era revision kit serves. ``mcp-types`` lists four
#: handshake versions; kit's legacy handler pins the oldest, which is the
#: one its ``initialize`` response has always advertised.
LEGACY_PROTOCOL_VERSION = HANDSHAKE_PROTOCOL_VERSIONS[0]

#: The stateless-era revision kit serves. Exactly one today.
MODERN_PROTOCOL_VERSION = MODERN_PROTOCOL_VERSIONS[0]

#: Both revisions, in the spelling ``spec_versions`` accepts.
SPEC_VERSIONS = (LEGACY_PROTOCOL_VERSION, MODERN_PROTOCOL_VERSION)

# --- JSON-RPC error codes ----------------------------------------------

ERR_PARSE = PARSE_ERROR
ERR_INVALID_REQUEST = INVALID_REQUEST
ERR_METHOD_NOT_FOUND = METHOD_NOT_FOUND
ERR_INVALID_PARAMS = INVALID_PARAMS
ERR_INTERNAL = INTERNAL_ERROR
ERR_HEADER_MISMATCH = HEADER_MISMATCH
ERR_MISSING_CLIENT_CAPABILITY = MISSING_REQUIRED_CLIENT_CAPABILITY
ERR_UNSUPPORTED_VERSION = UNSUPPORTED_PROTOCOL_VERSION

# --- header + _meta key names ------------------------------------------

HEADER_PROTOCOL_VERSION = "mcp-protocol-version"
HEADER_METHOD = "mcp-method"
HEADER_NAME = "mcp-name"
HEADER_AUTHORIZATION = "authorization"
HEADER_CONFIRM_TOKEN = "x-confirm-token"
HEADER_ORIGIN = "origin"

#: Canonical (wire-cased) spellings, used in error messages so the text
#: matches Go's byte-for-byte. Lookup is case-insensitive; only the
#: rendering needs the canonical form.
HEADER_PROTOCOL_VERSION_WIRE = "MCP-Protocol-Version"
HEADER_METHOD_WIRE = "Mcp-Method"
HEADER_NAME_WIRE = "Mcp-Name"

META_PROTOCOL_VERSION = PROTOCOL_VERSION_META_KEY
META_CLIENT_INFO = CLIENT_INFO_META_KEY
META_CLIENT_CAPABILITIES = CLIENT_CAPABILITIES_META_KEY
META_SERVER_INFO = SERVER_INFO_META_KEY

# --- result types -------------------------------------------------------

RESULT_TYPE_COMPLETE = "complete"
RESULT_TYPE_INPUT_REQUIRED = "input_required"

# --- defaults -----------------------------------------------------------

DEFAULT_PATH = "/mcp"
DEFAULT_SERVER_NAME = "cmdsurface"
DEFAULT_SERVER_VERSION = "0.0.0"

CACHE_SCOPE_PUBLIC = "public"
CACHE_SCOPE_PRIVATE = "private"
CACHE_SCOPES = frozenset({CACHE_SCOPE_PUBLIC, CACHE_SCOPE_PRIVATE})


# --- request / response value objects -----------------------------------


class Headers:
    """Case-insensitive multi-valued HTTP headers.

    Multi-valued because header/body agreement validation has to be able
    to tell "sent twice with identical values" (benign proxy duplication,
    tolerated) from "sent twice with conflicting values" (a validation
    failure in its own right). A flattened mapping loses exactly the
    distinction the check exists to make.
    """

    __slots__ = ("_items",)

    def __init__(self, items: list[tuple[str, str]] | None = None) -> None:
        self._items: list[tuple[str, str]] = [
            (name.lower(), value) for name, value in (items or [])
        ]

    @classmethod
    def from_mapping(cls, mapping: dict[str, str] | None) -> Headers:
        return cls(list((mapping or {}).items()))

    def values(self, name: str) -> list[str]:
        """Every value sent for ``name``, in order."""
        key = name.lower()
        return [value for header, value in self._items if header == key]

    def get(self, name: str, default: str = "") -> str:
        """The first value for ``name``, or ``default``."""
        values = self.values(name)
        return values[0] if values else default

    def single(self, name: str) -> tuple[str, bool]:
        """Reduce ``name`` to one value for header/body comparison.

        Returns ``(value, ok)``. Sent once, or several times with
        byte-identical values, resolves to that value with ``ok=True``.
        Sent several times with differing values yields ``ok=False``: the
        routing headers exist precisely so a gateway and the server agree
        on one signal, and conflicting duplicates are the
        multiple-sources-of-truth hazard the agreement check closes — so
        the caller rejects without ever comparing a value that was never
        singular.
        """
        values = self.values(name)
        if not values:
            return "", True
        if all(value == values[0] for value in values[1:]):
            return values[0], True
        return "", False


@dataclass
class Request:
    """A normalised inbound request: method, path, headers, body bytes."""

    method: str = "POST"
    path: str = DEFAULT_PATH
    headers: Headers = field(default_factory=Headers)
    body: bytes = b""


@dataclass
class Response:
    """A normalised outbound response: status, headers, body bytes."""

    status: int = 200
    headers: dict[str, str] = field(default_factory=dict)
    body: bytes = b""


JSON_CONTENT_TYPE = {"content-type": "application/json"}


@dataclass
class RPCRequest:
    """A decoded JSON-RPC request envelope.

    ``id_raw`` keeps the caller's exact ``id`` bytes so the response can
    echo them verbatim, matching Go's ``json.RawMessage`` field: a
    decode/re-encode round trip would normalise ``1.0`` to ``1`` and lose
    the ``null`` the legacy handler is contractually required to return
    unchanged.
    """

    jsonrpc: str = ""
    id_raw: Raw | None = None
    method: str = ""
    params: Any = None
    #: True when the body carried an ``id`` member at all, distinguishing
    #: a request from a notification.
    has_id: bool = False


def parse_rpc(body: bytes) -> RPCRequest:
    """Decode one JSON-RPC request envelope.

    Raises :class:`ValueError` when the body is not valid JSON; the
    dispatcher turns that into the ``-32700`` parse error. A body that
    parses as valid JSON but is not an object decodes to an all-default
    envelope, mirroring Go's tolerance for unmarshalling into a struct.
    """
    decoded = json.loads(body.decode("utf-8"))
    if not isinstance(decoded, dict):
        return RPCRequest()
    rpc = RPCRequest()
    jsonrpc = decoded.get("jsonrpc")
    rpc.jsonrpc = jsonrpc if isinstance(jsonrpc, str) else ""
    method = decoded.get("method")
    rpc.method = method if isinstance(method, str) else ""
    rpc.params = decoded.get("params")
    if "id" in decoded:
        rpc.has_id = True
        rpc.id_raw = Raw(_raw_id_text(body, decoded["id"]))
    return rpc


def _raw_id_text(body: bytes, value: Any) -> str:
    """Recover the ``id`` member's source text.

    ``json.loads`` has already normalised the value, so re-serialising it
    is the closest recovery available. It round-trips every id shape this
    surface actually meets (string, integer, ``null``) byte-identically;
    only exotic number spellings such as ``1e2`` would differ, and those
    are rejected as malformed ids on the modern path and are not part of
    any pinned legacy case.
    """
    return json.dumps(value, separators=(",", ":"), ensure_ascii=False)


def write_result(id_raw: Raw | None, result: Any, status: int) -> Response:
    """Encode a successful JSON-RPC response.

    Members are emitted in Go's struct declaration order — ``jsonrpc``,
    ``id``, ``result`` — not sorted, because Go marshals this envelope
    from a struct while the ``result`` body itself is a map and does get
    sorted. ``id`` is omitted entirely when absent, matching the
    ``omitempty`` tag.
    """
    members: list[tuple[str, Any]] = [("jsonrpc", "2.0")]
    if id_raw is not None:
        members.append(("id", id_raw))
    members.append(("result", result))
    return Response(status=status, headers=dict(JSON_CONTENT_TYPE), body=encode_envelope(members))


def write_error(
    id_raw: Raw | None, code: int, message: str, status: int, data: Any = None
) -> Response:
    """Encode a JSON-RPC error response.

    The error object's members follow Go's struct order — ``code``,
    ``message``, ``data`` — with ``data`` omitted when absent.
    """
    err: list[tuple[str, Any]] = [("code", code), ("message", message)]
    if data is not None:
        err.append(("data", data))
    members: list[tuple[str, Any]] = [("jsonrpc", "2.0")]
    if id_raw is not None:
        members.append(("id", id_raw))
    members.append(("error", Ordered(err)))
    return Response(status=status, headers=dict(JSON_CONTENT_TYPE), body=encode_envelope(members))
