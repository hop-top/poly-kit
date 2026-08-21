"""Era detection and dispatch for the dual-spec MCP surface.

One mount serves both protocol revisions. This module decides, per
request, which of the two handlers serves it — and exports the
ASGI-compatible callable an adopter binds to their own HTTP server.

Detection rules are normative and are ported literally rather than
re-derived. The marker set ``M(R)`` for a request is:

===  ===========================================================
M1   HTTP header ``Mcp-Method`` present
M2   HTTP header ``Mcp-Name`` present
M3   body ``params._meta`` contains the reserved key
     ``io.modelcontextprotocol/protocolVersion`` — key presence
     only, the value is not inspected at detection time
M4   body ``method == "server/discover"``
===  ===========================================================

Two **deliberate non-markers**, which are the subtle part and the most
likely source of silent divergence:

- **Bare ``params._meta`` is not a marker.** 2024-11-05 clients
  legitimately send ``_meta.progressToken`` and OTel ``traceparent``.
  Only the reserved ``protocolVersion`` key signals the modern era.
- **The ``MCP-Protocol-Version`` header is not a marker.** It predates
  2026-07-28 — it arrived with the 2025-06-18 transport — so a client
  that negotiated *down* to legacy through the handshake sends it on
  every subsequent request. Treating it as a modern signal would serve
  such a client's handshake and then brick its session. Nothing is lost:
  a conforming modern request always carries M1 and M3.

Precedence, first rule that applies winning:

- **D1 — parse.** Read and decode the body once, here. An unreadable
  body is ``-32603`` at 400; unparseable JSON is ``-32700`` at 400 —
  both byte-identical to the legacy responses, whatever headers are
  present.
- **D2 — ``initialize`` is legacy, unconditionally**, even with modern
  markers present. A confused client gets a working handshake, the most
  recoverable outcome; modern clients never send ``initialize``.
- **D3 — any marker routes modern.** Incomplete or contradictory modern
  requests are *not* demoted to legacy: the modern handler rejects them
  with modern errors, which is exactly what a dual-era client keys on to
  avoid falling back incorrectly.
- **D4 — otherwise legacy.** This is the byte-for-byte preservation
  path.
"""

from __future__ import annotations

import json
from typing import Any

from .bridge import Bridge
from .legacy import LegacyHandler
from .modern import ModernHandler, header_confirmation_gate
from .protocol import (
    CACHE_SCOPE_PRIVATE,
    CACHE_SCOPES,
    DEFAULT_PATH,
    DEFAULT_SERVER_NAME,
    DEFAULT_SERVER_VERSION,
    ERR_INVALID_REQUEST,
    ERR_PARSE,
    HEADER_METHOD,
    HEADER_NAME,
    LEGACY_PROTOCOL_VERSION,
    META_PROTOCOL_VERSION,
    MODERN_PROTOCOL_VERSION,
    Headers,
    Request,
    Response,
    RPCRequest,
    parse_rpc,
    write_error,
)

#: The two eras, as :func:`detect_era` reports them.
ERA_LEGACY = "legacy"
ERA_MODERN = "modern"


def detect_era(request: Request, rpc: RPCRequest) -> str:
    """Classify one already-parsed request as legacy or modern.

    Never fails: it only classifies. D1 (parse) is the caller's job, so
    by the time this runs there is always a well-formed envelope to look
    at.
    """
    # D2 — initialize is legacy, unconditionally.
    if rpc.method == "initialize":
        return ERA_LEGACY
    # M4 — server/discover is a marker on its own.
    if rpc.method == "server/discover":
        return ERA_MODERN
    # M1 / M2 — header presence.
    if request.headers.get(HEADER_METHOD):
        return ERA_MODERN
    if request.headers.get(HEADER_NAME):
        return ERA_MODERN
    # M3 — the reserved _meta key. Presence only; the value is not read.
    if has_modern_meta_marker(rpc.params):
        return ERA_MODERN
    # D4 — no markers.
    return ERA_LEGACY


def has_modern_meta_marker(params: Any) -> bool:
    """Report whether ``params._meta`` carries the reserved version key.

    Malformed or non-object ``params``/``_meta`` count as "no marker":
    detection never fails, and surfacing shape errors is the job of
    whichever handler ends up serving the request.
    """
    if not isinstance(params, dict):
        return False
    meta = params.get("_meta")
    if not isinstance(meta, dict):
        return False
    return META_PROTOCOL_VERSION in meta


class MountError(ValueError):
    """A mount-time configuration refusal.

    Misconfiguration fails fast at mount rather than being absorbed into
    surprising per-request behaviour, matching the surface's
    all-or-nothing mount convention.
    """


class McpSurface:
    """The dual-spec MCP surface: a transport-agnostic request handler.

    :meth:`handle` is the whole contract — a pure function from a
    normalised :class:`~hop_top_kit.mcp.protocol.Request` to a
    :class:`~hop_top_kit.mcp.protocol.Response`, with no socket in sight,
    which is what lets the conformance suite drive every fixture case
    without a server. :meth:`__call__` wraps it as an ASGI application
    for adopters who want to mount it under uvicorn, hypercorn, or a
    Starlette route.

    ASGI rather than WSGI is deliberate: the modern era's streaming
    affordances are not expressible in WSGI, and the official Python MCP
    SDK's modern transport is async.
    """

    def __init__(
        self,
        bridge: Bridge,
        *,
        path: str = DEFAULT_PATH,
        server_name: str = DEFAULT_SERVER_NAME,
        server_version: str = DEFAULT_SERVER_VERSION,
        spec_versions: tuple[str, ...] | None = None,
        cache_ttl_ms: int | None = None,
        cache_scope: str | None = None,
        origin_allowlist: tuple[str, ...] = (),
        confirmation_key: bytes | None = None,
        extensions: tuple[Any, ...] = (),
    ) -> None:
        self.path = path
        self._legacy_enabled, self._modern_enabled = _resolve_spec_versions(spec_versions)

        ttl_ms, scope = _resolve_cache_hints(cache_ttl_ms, cache_scope)
        if confirmation_key is not None and not confirmation_key:
            raise MountError("mcp: confirmation_key: empty key")

        self.legacy = LegacyHandler(
            bridge, server_name=server_name, server_version=server_version
        )

        gate = header_confirmation_gate
        if confirmation_key:
            from .modern_confirm import ElicitationConfirmationGate

            gate = ElicitationConfirmationGate(confirmation_key, bridge)

        advertised: dict[str, Any] = {}
        method_handlers: dict[str, Any] = {}
        for extension in extensions:
            advertised[extension.name] = extension.capability()
            method_handlers.update(extension.methods())

        self.modern = ModernHandler(
            bridge,
            server_name=server_name,
            server_version=server_version,
            cache_ttl_ms=ttl_ms,
            cache_scope=scope,
            origin_allowlist=tuple(origin_allowlist),
            confirmation_gate=gate,
            extensions=advertised,
            method_handlers=method_handlers,
        )
        for extension in extensions:
            extension.bind(self.modern)

    # --- request handling -------------------------------------------------

    def handle(self, request: Request) -> Response:
        """Serve one request. The entire public surface of this class."""
        if request.method != "POST":
            if self._modern_enabled:
                # Post-session servers answer the session-era verbs with
                # 405; the POST route is unaffected.
                return _method_not_allowed()
            return _method_not_allowed()

        # D1 — parse once, here.
        try:
            rpc = parse_rpc(request.body)
        except UnicodeDecodeError as exc:
            return write_error(None, ERR_PARSE, f"parse error: {exc}", 400)
        except ValueError as exc:
            return write_error(None, ERR_PARSE, f"parse error: {_go_json_error(request.body, exc)}", 400)

        if self._modern_enabled and not self._legacy_enabled:
            # Modern only: every request runs the normal V1-V9 order with
            # no special-casing of initialize, so a bare legacy handshake
            # fails modern validation rather than being demoted.
            return self.modern.serve(request, rpc)
        if self._legacy_enabled and not self._modern_enabled:
            # Legacy only: markers are ignored exactly as they were
            # before the modern era existed.
            return self.legacy.serve(request, rpc)

        if detect_era(request, rpc) == ERA_MODERN:
            return self.modern.serve(request, rpc)
        return self.legacy.serve(request, rpc)

    # --- ASGI binding ------------------------------------------------------

    async def __call__(self, scope: dict, receive: Any, send: Any) -> None:
        """ASGI application entry point.

        Deliberately does not bind to any particular server: kit does not
        own the adopter's HTTP stack, and a handler that hard-bound to one
        would force that dependency on every consumer.
        """
        if scope.get("type") != "http":
            raise RuntimeError("mcp: McpSurface only handles ASGI http scopes")

        body = b""
        more = True
        while more:
            message = await receive()
            if message["type"] == "http.disconnect":
                return
            body += message.get("body", b"")
            more = message.get("more_body", False)

        headers = Headers(
            [
                (name.decode("latin-1"), value.decode("latin-1"))
                for name, value in scope.get("headers", [])
            ]
        )
        request = Request(
            method=scope.get("method", "POST"),
            path=scope.get("path", self.path),
            headers=headers,
            body=body,
        )
        response = self.handle(request)
        await send(
            {
                "type": "http.response.start",
                "status": response.status,
                "headers": [
                    (name.encode("latin-1"), value.encode("latin-1"))
                    for name, value in response.headers.items()
                ],
            }
        )
        await send({"type": "http.response.body", "body": response.body})


def mount_mcp(bridge: Bridge, **options: Any) -> McpSurface:
    """Build the MCP surface for ``bridge``.

    The option set mirrors Go's ``MountMCP`` exactly — path, server info,
    spec versions, cache hints, origin allowlist, confirmation key — with
    the same defaults: both eras enabled, path ``/mcp``, no origin check,
    and cache hints ``ttlMs = 0`` / ``cacheScope = "private"``.
    """
    return McpSurface(bridge, **options)


# --- mount-time validation ------------------------------------------------


def _resolve_spec_versions(versions: tuple[str, ...] | None) -> tuple[bool, bool]:
    """Resolve the enabled-era set, refusing bad input at mount time.

    ``None`` means the option was not supplied: both eras. An explicitly
    empty tuple is a refusal, not a synonym for the default — the two are
    different intents and conflating them would silently mount a surface
    that serves nothing.
    """
    if versions is None:
        return True, True
    if len(versions) == 0:
        raise MountError("mcp: spec_versions: at least one spec version required")
    legacy = modern = False
    for version in versions:
        if version == LEGACY_PROTOCOL_VERSION:
            legacy = True
        elif version == MODERN_PROTOCOL_VERSION:
            modern = True
        else:
            raise MountError(f"mcp: spec_versions: unrecognized version {version!r}")
    return legacy, modern


def _resolve_cache_hints(ttl_ms: int | None, scope: str | None) -> tuple[int, str]:
    """Validate and default the cache hints."""
    resolved_ttl = 0 if ttl_ms is None else int(ttl_ms)
    if resolved_ttl < 0:
        raise MountError("mcp: cache_ttl_ms: negative ttl")
    resolved_scope = CACHE_SCOPE_PRIVATE if scope is None else scope
    if resolved_scope not in CACHE_SCOPES:
        raise MountError(f"mcp: cache_scope: unknown cache scope {resolved_scope}")
    return resolved_ttl, resolved_scope


def _method_not_allowed() -> Response:
    """The 405 answer to the session-era verbs (GET, DELETE)."""
    return write_error(None, ERR_INVALID_REQUEST, "method not allowed", 405)


def _go_json_error(body: bytes, exc: ValueError) -> str:
    """Render a decode failure the way Go's ``encoding/json`` words it.

    The parse-error *message text* is part of the wire contract: the
    fixtures pin Go's phrasing byte-for-byte, and Python's own
    ``JSONDecodeError`` text shares none of it. Only the shapes the
    fixtures and the spec's own examples exercise are reproduced
    faithfully; anything else falls back to Go's generic wording, which
    is the honest answer for an input neither runtime has pinned.
    """
    text = body.decode("utf-8", errors="replace")
    if not text.strip():
        return "unexpected end of JSON input"
    stripped = text.lstrip()
    if stripped.startswith("{"):
        # Go reports the first byte that cannot begin an object key.
        rest = stripped[1:].lstrip()
        if rest and rest[0] != '"' and rest[0] != "}":
            return (
                f"invalid character {_go_char(rest[0])} "
                "looking for beginning of object key string"
            )
    if stripped:
        return f"invalid character {_go_char(stripped[0])} looking for beginning of value"
    return "unexpected end of JSON input"


def _go_char(char: str) -> str:
    """Quote one character the way Go's JSON errors do (single quotes)."""
    escaped = json.dumps(char)[1:-1].replace("'", "\\'")
    return f"'{escaped}'"
