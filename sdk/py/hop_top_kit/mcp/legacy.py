"""Legacy MCP handler — protocol revision 2024-11-05.

This module is subject to one hard invariant, inherited from the Go
reference: **2024-11-05 behaviour is preserved byte-for-byte; changes are
additive only; nothing is deprecated.** It is a separate module rather
than a branch inside a combined handler precisely so that invariant stays
structurally checkable — a reviewer can confirm a modern-era change did
not touch this file.

Serves three methods: ``initialize`` (server identity and capabilities),
``tools/list`` (one tool per exposed leaf), and ``tools/call`` (invoke via
the bridge and render the result as content blocks). Everything else is
``-32601`` at HTTP **200** — a status the modern handler deliberately does
not share, because changing it here would break the preservation rule.
"""

from __future__ import annotations

from typing import Any

from .bridge import Bridge, Invocation, Leaf, Meta, Result, tool_path
from .protocol import (
    ERR_INVALID_PARAMS,
    ERR_INVALID_REQUEST,
    ERR_METHOD_NOT_FOUND,
    LEGACY_PROTOCOL_VERSION,
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

#: HTTP statuses the pre-flight gates use. Mirrored on the modern path so
#: MCP-aware clients see ``isError`` while HTTP-only clients see a status
#: they can act on.
STATUS_OK = 200
STATUS_UNAUTHORIZED = 401
STATUS_PRECONDITION_REQUIRED = 428


class LegacyHandler:
    """Serves one mount's 2024-11-05 traffic.

    Stateless across requests: all mutable state lives on the bridge, so
    one instance safely serves concurrent requests.
    """

    def __init__(self, bridge: Bridge, *, server_name: str, server_version: str) -> None:
        self._bridge = bridge
        self._server_name = server_name
        self._server_version = server_version

    def serve(self, request: Request, rpc: RPCRequest) -> Response:
        """Route one already-parsed request by method."""
        if rpc.jsonrpc not in ("", "2.0"):
            return write_error(rpc.id_raw, ERR_INVALID_REQUEST, "invalid jsonrpc version", 400)
        if rpc.method == "initialize":
            return self._initialize(rpc)
        if rpc.method == "tools/list":
            return self._tools_list(rpc)
        if rpc.method == "tools/call":
            return self._tools_call(request, rpc)
        return write_error(
            rpc.id_raw,
            ERR_METHOD_NOT_FOUND,
            f"method not found: {rpc.method}",
            STATUS_OK,
        )

    def _initialize(self, rpc: RPCRequest) -> Response:
        """The handshake response: protocol version, capabilities, identity."""
        return write_result(
            rpc.id_raw,
            {
                "protocolVersion": LEGACY_PROTOCOL_VERSION,
                "capabilities": {"tools": {}},
                "serverInfo": {
                    "name": self._server_name,
                    "version": self._server_version,
                },
            },
            STATUS_OK,
        )

    def _tools_list(self, rpc: RPCRequest) -> Response:
        """Enumerate every leaf exposed on the MCP surface."""
        return write_result(rpc.id_raw, {"tools": exposed_tools(self._bridge)}, STATUS_OK)

    def _tools_call(self, request: Request, rpc: RPCRequest) -> Response:
        """Resolve, gate, invoke, and render one tool call.

        Error mapping follows the surface contract: an unknown or
        unexposed tool is a JSON-RPC ``-32602``; everything else — bridge
        refusals, runner failures, non-zero exit codes — comes back as a
        result with ``isError: true``, so an MCP client sees a tool
        failure rather than a transport failure.
        """
        params = rpc.params if isinstance(rpc.params, dict) else {}
        name = params.get("name")
        if not isinstance(name, str) or not name:
            return write_error(rpc.id_raw, ERR_INVALID_PARAMS, "missing tool name", STATUS_OK)

        leaf = resolve_exposed_leaf(self._bridge, name)
        if leaf is None:
            return write_error(rpc.id_raw, ERR_INVALID_PARAMS, f"unknown tool: {name}", STATUS_OK)

        gate = preflight_refusal(request, leaf)
        if gate is not None:
            message, status = gate
            return write_result(rpc.id_raw, error_result_block(message), status)

        arguments = params.get("arguments")
        inv = Invocation(
            path=leaf.path,
            flags=arguments if isinstance(arguments, dict) else {},
            meta=Meta(surface=Surface.MCP),
        )
        try:
            result = self._bridge.invoke(inv)
        except (UnknownCommandError, SurfaceNotEnabledError):
            return write_error(rpc.id_raw, ERR_INVALID_PARAMS, f"unknown tool: {name}", STATUS_OK)
        except DestructiveBlockedError as exc:
            return write_result(rpc.id_raw, error_result_block(str(exc)), STATUS_OK)
        except Exception as exc:
            return write_result(rpc.id_raw, error_result_block(str(exc)), STATUS_OK)

        return write_result(rpc.id_raw, render_call_result(result), STATUS_OK)


# --- shared renderers ---------------------------------------------------
#
# Both eras call these. They live here rather than in a third module
# because the modern era is defined as "legacy's rendering plus envelope
# members": keeping one implementation is what makes schema and content
# drift between the two impossible rather than merely unlikely.


def exposed_tools(bridge: Bridge) -> list[dict[str, Any]]:
    """Tool descriptors for every leaf exposed on the MCP surface."""
    return [
        leaf.tool_envelope() for leaf in bridge.leaves() if leaf.enabled.get(Surface.MCP, False)
    ]


def resolve_exposed_leaf(bridge: Bridge, name: str) -> Leaf | None:
    """Resolve a dotted tool name to an MCP-exposed leaf, or ``None``."""
    try:
        leaf = bridge.resolve_leaf(tool_path(name))
    except UnknownCommandError:
        return None
    if not leaf.enabled.get(Surface.MCP, False):
        return None
    return leaf


def preflight_refusal(request: Request, leaf: Leaf) -> tuple[str, int] | None:
    """Apply the auth and confirmation header gates.

    Returns ``(message, status)`` when the call must be refused, or
    ``None`` to proceed. The modern path reuses the auth half and swaps
    the confirmation half for a strategy slot, so the two eras cannot
    drift on what "authenticated" means.
    """
    if leaf.cls.auth_required and not request.headers.get("authorization"):
        return "authentication required", STATUS_UNAUTHORIZED
    if leaf.cls.requires_confirmation and not request.headers.get("x-confirm-token"):
        return "confirmation required", STATUS_PRECONDITION_REQUIRED
    return None


def render_call_result(result: Result) -> dict[str, Any]:
    """Map a bridge result onto the ``tools/call`` result envelope.

    The content list always carries at least the stdout block (possibly
    empty); stderr adds a ``[stderr] `` prefixed block, and a structured
    payload adds its JSON serialisation as a third text block — which
    doubles as the spec-recommended serialised fallback for
    ``structuredContent`` on the modern path.
    """
    from ._json import dumps

    content: list[dict[str, Any]] = [{"type": "text", "text": result.stdout}]
    if result.stderr:
        content.append({"type": "text", "text": "[stderr] " + result.stderr})
    if result.data is not None:
        content.append({"type": "text", "text": dumps(result.data)})
    return {"content": content, "isError": result.exit_code != 0}


def error_result_block(message: str) -> dict[str, Any]:
    """A ``tools/call`` result flagged ``isError`` with one text block."""
    return {"content": [{"type": "text", "text": message}], "isError": True}
