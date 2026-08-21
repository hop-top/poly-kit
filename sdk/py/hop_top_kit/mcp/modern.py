"""Modern MCP handler — protocol revision 2026-07-28.

The stateless request core behind the era dispatcher. Statelessness is
the revision's whole point: every request carries its protocol version,
client identity, and capabilities in ``params._meta`` under reserved
``io.modelcontextprotocol/*`` keys, there is no handshake and no session
id, so any instance can serve any request.

Implements the normative validation chain V1–V9. The first failure
responds and stops. HTTP status is 400 or 404 only where the spec
mandates it; application-level JSON-RPC errors ride HTTP 200, matching
the legacy convention.

======  =====================================================  ==============
Check   Condition                                              Failure
======  =====================================================  ==============
V1      ``jsonrpc`` absent or ``"2.0"``                         -32600 @ 400
V2      ``id`` absent → notification (202); present id must     -32600 @ 400
        be a string or a fractionless number (``null`` is
        forbidden by this revision)
V3      ``params._meta`` carries ``protocolVersion`` and        -32602 @ 400
        ``clientCapabilities``
V4      ``MCP-Protocol-Version`` header equals the ``_meta``    -32020 @ 400
        value
V5      requested version is supported                          -32022 @ 400
V6      ``Mcp-Method`` header equals the body method            -32020 @ 400
V7      ``tools/call``: ``Mcp-Name`` present, non-empty after   -32020 @ 400
        sentinel decoding, equal to ``params.name``
V8      method is discover / list / call                        -32601 @ 404
V9      per-method params                                       -32602 @ 200
======  =====================================================  ==============
"""

from __future__ import annotations

import base64
from collections.abc import Callable
from typing import Any

from ._json import Raw
from .bridge import Bridge, Invocation, Leaf, Meta
from .legacy import (
    STATUS_OK,
    STATUS_PRECONDITION_REQUIRED,
    STATUS_UNAUTHORIZED,
    error_result_block,
    exposed_tools,
    render_call_result,
    resolve_exposed_leaf,
)
from .protocol import (
    CACHE_SCOPE_PRIVATE,
    ERR_HEADER_MISMATCH,
    ERR_INVALID_PARAMS,
    ERR_INVALID_REQUEST,
    ERR_METHOD_NOT_FOUND,
    ERR_UNSUPPORTED_VERSION,
    HEADER_METHOD,
    HEADER_METHOD_WIRE,
    HEADER_NAME,
    HEADER_NAME_WIRE,
    HEADER_PROTOCOL_VERSION,
    HEADER_PROTOCOL_VERSION_WIRE,
    META_CLIENT_CAPABILITIES,
    META_CLIENT_INFO,
    META_PROTOCOL_VERSION,
    META_SERVER_INFO,
    MODERN_PROTOCOL_VERSION,
    RESULT_TYPE_COMPLETE,
    Request,
    Response,
    RPCRequest,
    write_error,
    write_result,
)
from .safety import (
    DestructiveBlockedError,
    Surface,
    SurfaceNotEnabledError,
    UnknownCommandError,
)

STATUS_BAD_REQUEST = 400
STATUS_FORBIDDEN = 403
STATUS_NOT_FOUND = 404
STATUS_ACCEPTED = 202

#: Delimiters of the Base64 sentinel a header value may carry when it
#: cannot be represented as a safe plain-ASCII header. Case-sensitive,
#: and must appear exactly as spelled (spec: Value Encoding).
SENTINEL_PREFIX = "=?base64?"
SENTINEL_SUFFIX = "?="


class CheckError(Exception):
    """One validation-chain failure: code, message, status, optional data."""

    def __init__(self, code: int, message: str, status: int, data: Any = None) -> None:
        super().__init__(message)
        self.code = code
        self.message = message
        self.status = status
        self.data = data


class RequestMeta:
    """The decoded reserved ``params._meta`` keys of a modern request."""

    __slots__ = (
        "client_name",
        "client_version",
        "has_client_info",
        "version",
        "version_is_text",
        "version_raw",
    )

    def __init__(self) -> None:
        self.version: str = ""
        self.version_is_text: bool = False
        self.version_raw: Any = None
        self.client_name: str = ""
        self.client_version: str = ""
        self.has_client_info: bool = False


#: A confirmation gate returns ``(refusal_body, status)`` to refuse, or
#: ``None`` to let the call proceed.
ConfirmationGate = Callable[
    [Request, Leaf, RPCRequest], "tuple[dict[str, Any], int] | None"
]


def header_confirmation_gate(
    request: Request, leaf: Leaf, _rpc: RPCRequest
) -> tuple[dict[str, Any], int] | None:
    """The default gate: an ``X-Confirm-Token`` header, exactly as legacy."""
    if leaf.cls.requires_confirmation and not request.headers.get("x-confirm-token"):
        return error_result_block("confirmation required"), STATUS_PRECONDITION_REQUIRED
    return None


class ModernHandler:
    """Serves one mount's 2026-07-28 traffic."""

    def __init__(
        self,
        bridge: Bridge,
        *,
        server_name: str,
        server_version: str,
        cache_ttl_ms: int = 0,
        cache_scope: str = CACHE_SCOPE_PRIVATE,
        origin_allowlist: tuple[str, ...] = (),
        confirmation_gate: ConfirmationGate | None = None,
        extensions: dict[str, Any] | None = None,
        method_handlers: dict[str, Callable[..., Response]] | None = None,
    ) -> None:
        self._bridge = bridge
        self._server_name = server_name
        self._server_version = server_version
        self._cache_ttl_ms = cache_ttl_ms
        self._cache_scope = cache_scope or CACHE_SCOPE_PRIVATE
        self._origin_allowlist = origin_allowlist
        self._confirm: ConfirmationGate = confirmation_gate or header_confirmation_gate
        #: Negotiated extensions advertised by ``server/discover``. Empty
        #: means the map is omitted entirely, which is how the spec
        #: spells "no extensions supported".
        self._extensions = dict(extensions or {})
        #: Extra methods contributed by extensions, keyed by JSON-RPC
        #: method name. Consulted at V8 after the core three.
        self._method_handlers = dict(method_handlers or {})

    @property
    def bridge(self) -> Bridge:
        return self._bridge

    # --- entry point ----------------------------------------------------

    def serve(self, request: Request, rpc: RPCRequest) -> Response:
        """Run the validation chain, then dispatch to the method handler."""
        if not self._origin_allowed(request):
            return self._error(
                rpc, CheckError(ERR_INVALID_REQUEST, "origin not allowed", STATUS_FORBIDDEN)
            )

        # V1 — jsonrpc member absent or "2.0" (same tolerance as legacy).
        if rpc.jsonrpc not in ("", "2.0"):
            return self._error(
                rpc,
                CheckError(ERR_INVALID_REQUEST, "invalid jsonrpc version", STATUS_BAD_REQUEST),
            )

        # V2 — no id is a notification: accepted, discarded, not processed.
        if not rpc.has_id:
            return Response(status=STATUS_ACCEPTED, headers={}, body=b"")
        if not _valid_request_id(rpc.id_raw):
            raw = rpc.id_raw.text if rpc.id_raw else ""
            return self._error(
                rpc,
                CheckError(
                    ERR_INVALID_REQUEST,
                    f"invalid request id: must be a string or integer, got {raw}",
                    STATUS_BAD_REQUEST,
                ),
            )

        # V3 — required reserved _meta keys.
        try:
            meta = parse_request_meta(rpc.params)
        except CheckError as exc:
            return self._error(rpc, exc)

        # V4 — MCP-Protocol-Version header agreement.
        try:
            self._validate_version_header(request, meta)
            # V5 — requested version supported.
            self._validate_version_supported(meta)
            # V6 — Mcp-Method header agreement.
            self._validate_method_header(request, rpc)
        except CheckError as exc:
            return self._error(rpc, exc)

        # V8 — method routing. V7 and V9 run inside the method handlers.
        if rpc.method == "server/discover":
            return self._discover(rpc)
        if rpc.method == "tools/list":
            return self._tools_list(rpc)
        if rpc.method == "tools/call":
            return self._tools_call(request, rpc, meta)
        extension = self._method_handlers.get(rpc.method)
        if extension is not None:
            return extension(request, rpc, meta)
        return self._error(
            rpc,
            CheckError(
                ERR_METHOD_NOT_FOUND,
                f"method not found: {rpc.method}",
                STATUS_NOT_FOUND,
            ),
        )

    # --- validation steps -----------------------------------------------

    def _validate_version_header(self, request: Request, meta: RequestMeta) -> None:
        """V4: the header must be present and equal the ``_meta`` value."""
        value, ok = request.headers.single(HEADER_PROTOCOL_VERSION)
        if not ok:
            raise CheckError(
                ERR_HEADER_MISMATCH,
                f"{HEADER_PROTOCOL_VERSION_WIRE} header sent with conflicting duplicate values",
                STATUS_BAD_REQUEST,
            )
        if not value:
            raise CheckError(
                ERR_HEADER_MISMATCH,
                f"missing {HEADER_PROTOCOL_VERSION_WIRE} header",
                STATUS_BAD_REQUEST,
            )
        if not meta.version_is_text or value != meta.version:
            # A non-string _meta value can never equal the header string.
            raise CheckError(
                ERR_HEADER_MISMATCH,
                f"{HEADER_PROTOCOL_VERSION_WIRE} header {_quote(value)} "
                f"does not match _meta protocolVersion {_go_value(meta.version_raw)}",
                STATUS_BAD_REQUEST,
            )

    def _validate_version_supported(self, meta: RequestMeta) -> None:
        """V5: this handler serves exactly one revision.

        The ``supported`` list deliberately excludes ``2024-11-05``: that
        list names versions a client may select *per request*, and the
        legacy revision is reachable only through its handshake. Naming
        it here would send a retrying modern client into a dead end.
        """
        if meta.version != MODERN_PROTOCOL_VERSION:
            raise CheckError(
                ERR_UNSUPPORTED_VERSION,
                f"unsupported protocol version: {meta.version}",
                STATUS_BAD_REQUEST,
                data={
                    "supported": [MODERN_PROTOCOL_VERSION],
                    "requested": meta.version_raw,
                },
            )

    def _validate_method_header(self, request: Request, rpc: RPCRequest) -> None:
        """V6: the ``Mcp-Method`` header must equal the body method."""
        value, ok = request.headers.single(HEADER_METHOD)
        if not ok:
            raise CheckError(
                ERR_HEADER_MISMATCH,
                f"{HEADER_METHOD_WIRE} header sent with conflicting duplicate values",
                STATUS_BAD_REQUEST,
            )
        if not value:
            raise CheckError(
                ERR_HEADER_MISMATCH,
                f"missing {HEADER_METHOD_WIRE} header",
                STATUS_BAD_REQUEST,
            )
        if value != rpc.method:
            raise CheckError(
                ERR_HEADER_MISMATCH,
                f"{HEADER_METHOD_WIRE} header {_quote(value)} "
                f"does not match body method {_quote(rpc.method)}",
                STATUS_BAD_REQUEST,
            )

    def _validate_name_header(
        self, request: Request, raw_name: str, present: bool, is_string: bool
    ) -> None:
        """V7: ``Mcp-Name`` presence, sentinel decoding, and body agreement.

        Runs *before* the full params decode, against a raw peek at
        ``params.name``. Headers are the routing signal a gateway trusts
        without parsing the body, so an absent or contradicted signal is
        malformed at the header layer and is decided ahead of any
        body-shape check — a ``tools/call`` with no ``Mcp-Name`` is
        ``-32020`` even when the rest of ``params`` is unparseable.
        """
        value, ok = request.headers.single(HEADER_NAME)
        if not ok:
            raise CheckError(
                ERR_HEADER_MISMATCH,
                f"{HEADER_NAME_WIRE} header sent with conflicting duplicate values",
                STATUS_BAD_REQUEST,
            )
        if not value:
            raise CheckError(
                ERR_HEADER_MISMATCH,
                f"missing {HEADER_NAME_WIRE} header",
                STATUS_BAD_REQUEST,
            )
        decoded = decode_sentinel(value)
        if decoded is None:
            raise CheckError(
                ERR_HEADER_MISMATCH,
                f"{HEADER_NAME_WIRE} header value is not valid base64-sentinel encoded",
                STATUS_BAD_REQUEST,
            )
        if decoded == "":
            raise CheckError(
                ERR_HEADER_MISMATCH,
                f"{HEADER_NAME_WIRE} header decodes to an empty value",
                STATUS_BAD_REQUEST,
            )
        if not present:
            raise CheckError(
                ERR_HEADER_MISMATCH,
                f"{HEADER_NAME_WIRE} header present but body params.name is absent",
                STATUS_BAD_REQUEST,
            )
        if not is_string:
            # params.name exists but is not a string: V7 has nothing to
            # compare, so it defers to V9's params decode rather than
            # guessing at an equivalence.
            return
        if decoded != raw_name:
            raise CheckError(
                ERR_HEADER_MISMATCH,
                f"{HEADER_NAME_WIRE} header {_quote(decoded)} "
                f"does not match body params.name {_quote(raw_name)}",
                STATUS_BAD_REQUEST,
            )

    # --- methods ---------------------------------------------------------

    def _discover(self, rpc: RPCRequest) -> Response:
        """``server/discover`` — the mandatory modern discovery method.

        Carries no ``listChanged`` flag (notifications are not
        implemented) and no ``instructions``. The ``extensions`` map is
        omitted unless a extension registered one, which is how the spec
        spells "supports none".
        """
        result: dict[str, Any] = {
            "supportedVersions": [MODERN_PROTOCOL_VERSION],
            "capabilities": {"tools": {}},
        }
        if self._extensions:
            result["capabilities"] = {"tools": {}, "extensions": dict(self._extensions)}
        self.apply_cache_hints(result)
        self.stamp_envelope(result)
        return write_result(rpc.id_raw, result, STATUS_OK)

    def _tools_list(self, rpc: RPCRequest) -> Response:
        """``tools/list`` — the same descriptors legacy emits, plus hints.

        Optional 2026-07-28 descriptor fields (``title``, ``icons``,
        ``outputSchema``, ``annotations``, ``x-mcp-header``) are not
        emitted. Pagination is not implemented: a ``cursor`` param is
        ignored and no ``nextCursor`` comes back.
        """
        result: dict[str, Any] = {"tools": exposed_tools(self._bridge)}
        self.apply_cache_hints(result)
        self.stamp_envelope(result)
        return write_result(rpc.id_raw, result, STATUS_OK)

    def _tools_call(
        self, request: Request, rpc: RPCRequest, meta: RequestMeta
    ) -> Response:
        """``tools/call`` — V7, V9, the pre-flight gates, invoke, render."""
        raw_name, present, is_string = raw_tool_name(rpc.params)
        try:
            self._validate_name_header(request, raw_name, present, is_string)
        except CheckError as exc:
            return self._error(rpc, exc)

        params = rpc.params if isinstance(rpc.params, dict) else {}
        name = params.get("name")
        if not isinstance(name, str) or not name:
            # V9. Unreachable through a conforming request — V7 already
            # requires a present, non-empty, header-matching name — but
            # kept as the correct answer for any caller that reaches this
            # method without passing the V7 gate above.
            return write_error(
                rpc.id_raw, ERR_INVALID_PARAMS, "missing tool name", STATUS_OK
            )

        leaf = resolve_exposed_leaf(self._bridge, name)
        if leaf is None:
            return write_error(
                rpc.id_raw, ERR_INVALID_PARAMS, f"unknown tool: {name}", STATUS_OK
            )

        if leaf.cls.auth_required and not request.headers.get("authorization"):
            return self._call_error(rpc, "authentication required", STATUS_UNAUTHORIZED)

        refusal = self._confirm(request, leaf, rpc)
        if refusal is not None:
            body, status = refusal
            return write_result(rpc.id_raw, self.stamp_envelope(body), status)

        arguments = params.get("arguments")
        inv = Invocation(
            path=leaf.path,
            flags=arguments if isinstance(arguments, dict) else {},
            meta=Meta(surface=Surface.MCP, extra=invocation_extra(meta)),
        )
        try:
            result = self._bridge.invoke(inv)
        except (UnknownCommandError, SurfaceNotEnabledError):
            return write_error(
                rpc.id_raw, ERR_INVALID_PARAMS, f"unknown tool: {name}", STATUS_OK
            )
        except DestructiveBlockedError as exc:
            # The destructive ceiling renders as a tool error at HTTP 200,
            # never as a transport error — the same rendering as legacy.
            return self._call_error(rpc, str(exc), STATUS_OK)
        except Exception as exc:
            return self._call_error(rpc, str(exc), STATUS_OK)

        out = render_call_result(result)
        if result.data is not None:
            out["structuredContent"] = result.data
        return write_result(rpc.id_raw, self.stamp_envelope(out), STATUS_OK)

    # --- envelope helpers -------------------------------------------------

    def stamp_envelope(self, body: dict[str, Any]) -> dict[str, Any]:
        """Add the members every modern result envelope carries.

        ``resultType`` defaults to ``"complete"``; a producer that has
        already chosen one keeps it, which is how the MRTR confirmation
        flow stamps ``"input_required"`` on its interim results. The
        result-level ``_meta`` always carries ``serverInfo``, built from
        the same identity the legacy handshake reports.
        """
        body.setdefault("resultType", RESULT_TYPE_COMPLETE)
        body["_meta"] = {
            META_SERVER_INFO: {
                "name": self._server_name,
                "version": self._server_version,
            }
        }
        return body

    def apply_cache_hints(self, body: dict[str, Any]) -> dict[str, Any]:
        """Add ``ttlMs`` and ``cacheScope`` to a cacheable complete-result.

        Applies to ``server/discover`` and ``tools/list`` only —
        ``tools/call`` is not a cacheable operation and interim
        ``input_required`` results are never cached. The default
        ``ttlMs: 0`` is honest rather than pessimistic: ``expose`` and
        ``hide`` can mutate the leaf set at runtime and there is no
        ``list_changed`` notification to invalidate a cache with.
        """
        body["ttlMs"] = self._cache_ttl_ms
        body["cacheScope"] = self._cache_scope
        return body

    def _origin_allowed(self, request: Request) -> bool:
        """Apply the opt-in Origin allowlist.

        No allowlist configured means no check; a request without an
        ``Origin`` header is never refused; otherwise the value must
        match an entry exactly.
        """
        if not self._origin_allowlist:
            return True
        origin = request.headers.get("origin")
        if not origin:
            return True
        return origin in self._origin_allowlist

    def _error(self, rpc: RPCRequest, exc: CheckError) -> Response:
        """Write a modern JSON-RPC error envelope.

        When the rejected request's method is ``initialize`` the message
        additionally names the supported versions: a legacy client has no
        fall-forward mechanism, so the version list in the error text is
        its only recovery hint.
        """
        message = exc.message
        if rpc.method == "initialize":
            message += f"; supported protocol versions: {MODERN_PROTOCOL_VERSION}"
        return write_error(rpc.id_raw, exc.code, message, exc.status, exc.data)

    def _call_error(self, rpc: RPCRequest, message: str, status: int) -> Response:
        """Write an ``isError`` tools/call result with the envelope stamped."""
        return write_result(
            rpc.id_raw, self.stamp_envelope(error_result_block(message)), status
        )


# --- free helpers --------------------------------------------------------


def parse_request_meta(params: Any) -> RequestMeta:
    """V3: decode the reserved ``params._meta`` keys.

    ``protocolVersion`` and ``clientCapabilities`` are required;
    ``clientInfo`` is optional and only feeds audit metadata, so a value
    that is not an object is treated as absent rather than rejected.
    Missing or non-object ``params`` fails exactly like missing keys.
    """
    meta_obj: Any = None
    if isinstance(params, dict):
        meta_obj = params.get("_meta")
    if not isinstance(meta_obj, dict):
        raise CheckError(
            ERR_INVALID_PARAMS, "missing required params._meta", STATUS_BAD_REQUEST
        )
    if META_PROTOCOL_VERSION not in meta_obj:
        raise CheckError(
            ERR_INVALID_PARAMS,
            f"missing required _meta key: {META_PROTOCOL_VERSION}",
            STATUS_BAD_REQUEST,
        )
    if META_CLIENT_CAPABILITIES not in meta_obj:
        raise CheckError(
            ERR_INVALID_PARAMS,
            f"missing required _meta key: {META_CLIENT_CAPABILITIES}",
            STATUS_BAD_REQUEST,
        )

    meta = RequestMeta()
    version = meta_obj[META_PROTOCOL_VERSION]
    meta.version_raw = version
    if isinstance(version, str):
        meta.version = version
        meta.version_is_text = True

    client_info = meta_obj.get(META_CLIENT_INFO)
    if isinstance(client_info, dict):
        meta.has_client_info = True
        name = client_info.get("name")
        ver = client_info.get("version")
        meta.client_name = name if isinstance(name, str) else ""
        meta.client_version = ver if isinstance(ver, str) else ""
    return meta


def raw_tool_name(params: Any) -> tuple[str, bool, bool]:
    """Peek at ``params.name`` without requiring the rest to decode.

    Returns ``(name, present, is_string)``. ``present`` reports whether
    ``params`` is an object carrying a ``name`` key at all, which is what
    V7 needs to distinguish "absent" (a header-validation failure) from
    "wrong type" (a params shape error V9 rejects).
    """
    if not isinstance(params, dict) or "name" not in params:
        return "", False, False
    value = params["name"]
    if isinstance(value, str):
        return value, True, True
    return "", True, False


def decode_sentinel(value: str) -> str | None:
    """Decode a possibly sentinel-wrapped header value.

    A plain value comes back unchanged — conforming kit tool names are
    header-safe ASCII and are sent literally. A value that merely *looks*
    sentinel-wrapped is always treated as encoded, so a malformed
    encoding fails closed (``None``) rather than silently falling back to
    a literal comparison a tampered header could win.
    """
    if not (value.startswith(SENTINEL_PREFIX) and value.endswith(SENTINEL_SUFFIX)):
        return value
    inner = value[len(SENTINEL_PREFIX) : -len(SENTINEL_SUFFIX)]
    try:
        return base64.b64decode(inner, validate=True).decode("utf-8")
    except Exception:
        return None


def invocation_extra(meta: RequestMeta) -> dict[str, str]:
    """The audit bag for a modern invocation.

    The spec version always; client identity when the request carried
    ``io.modelcontextprotocol/clientInfo``. Both eras share one
    ``Surface`` value, so this is the only thing distinguishing them for
    an audit sink.
    """
    extra = {"mcp_spec_version": MODERN_PROTOCOL_VERSION}
    if meta.has_client_info:
        extra["mcp_client_name"] = meta.client_name
        extra["mcp_client_version"] = meta.client_version
    return extra


def _valid_request_id(raw: Raw | None) -> bool:
    """V2: an id must be a JSON string or a number with no fractional part.

    Base JSON-RPC also permits ``null``, but this revision forbids it, so
    ``null`` is rejected alongside booleans, floats, objects, and arrays.
    """
    if raw is None:
        return False
    import json as _json

    if raw.text == "null":
        return False
    try:
        value = _json.loads(raw.text)
    except ValueError:
        return False
    if isinstance(value, bool):
        return False
    if isinstance(value, str):
        return True
    if isinstance(value, int):
        return True
    if isinstance(value, float):
        return value.is_integer()
    return False


def _quote(value: str) -> str:
    """Render a string the way Go's ``%q`` verb would, for error text."""
    from ._json import dumps

    return dumps(value)


def _go_value(value: Any) -> str:
    """Render a decoded JSON value the way Go's ``%v`` verb would.

    Only reached in the V4 mismatch message, where the ``_meta``
    ``protocolVersion`` was not a string. Go prints ``map``/slice/number
    values through ``%v``; the shapes that actually occur here are
    scalars, and ``true``/``false``/``<nil>`` are the spellings Go uses.
    """
    if value is None:
        return "<nil>"
    if value is True:
        return "true"
    if value is False:
        return "false"
    if isinstance(value, str):
        return value
    return str(value)
