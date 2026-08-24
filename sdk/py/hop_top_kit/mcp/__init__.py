"""Dual-spec MCP surface — serves 2024-11-05 and 2026-07-28 from one mount.

One mount, two protocol revisions, per-request era detection. Legacy
traffic takes the exact code path it always has; modern traffic gets the
stateless envelope, header routing, cacheable lists, and MRTR the newer
revision defines.

    from hop_top_kit.mcp import Bridge, Command, Flag, Result, mount_mcp

    root = Command(name="app", children=[
        Command(name="ping", short="Ping the server",
                run=lambda flags: Result(stdout="pong\\n"),
                annotations={"kit/side-effect": "read"}),
    ])
    app = mount_mcp(Bridge(root))   # an ASGI callable

The surface is a pure function from request to response
(:meth:`McpSurface.handle`); the ASGI binding is a thin wrapper over it,
so adopters keep control of their own HTTP stack and the conformance
suite can drive every case without a socket.

Module layout mirrors the protocol split — ``legacy`` and ``modern`` are
separate modules with a ``dispatch`` module choosing between them — which
keeps the "2024-11-05 is preserved byte-for-byte, additive only"
invariant structurally checkable rather than merely asserted.
"""

from __future__ import annotations

from .bridge import (
    Bridge,
    Command,
    Flag,
    Invocation,
    Leaf,
    Meta,
    Result,
    json_type,
    match_pattern,
    tool_path,
)
from .dispatch import (
    ERA_LEGACY,
    ERA_MODERN,
    McpSurface,
    MountError,
    detect_era,
    mount_mcp,
)
from .legacy import LegacyHandler
from .modern import ModernHandler, header_confirmation_gate
from .modern_confirm import ElicitationConfirmationGate
from .protocol import (
    CACHE_SCOPE_PRIVATE,
    CACHE_SCOPE_PUBLIC,
    DEFAULT_PATH,
    LEGACY_PROTOCOL_VERSION,
    MODERN_PROTOCOL_VERSION,
    SPEC_VERSIONS,
    Headers,
    Request,
    Response,
)
from .safety import (
    DestructiveBlockedError,
    Policy,
    SafetyClass,
    Surface,
    SurfaceNotEnabledError,
    UnknownCommandError,
    classify,
    default_policy,
)
from .tasks import InMemoryTaskStore, TasksExtension, TaskStore

__all__ = [
    "CACHE_SCOPE_PRIVATE",
    "CACHE_SCOPE_PUBLIC",
    "DEFAULT_PATH",
    "ERA_LEGACY",
    "ERA_MODERN",
    "LEGACY_PROTOCOL_VERSION",
    "MODERN_PROTOCOL_VERSION",
    "SPEC_VERSIONS",
    "Bridge",
    "Command",
    "DestructiveBlockedError",
    "ElicitationConfirmationGate",
    "Flag",
    "Headers",
    "InMemoryTaskStore",
    "Invocation",
    "Leaf",
    "LegacyHandler",
    "McpSurface",
    "Meta",
    "ModernHandler",
    "MountError",
    "Policy",
    "Request",
    "Response",
    "Result",
    "SafetyClass",
    "Surface",
    "SurfaceNotEnabledError",
    "TaskStore",
    "TasksExtension",
    "UnknownCommandError",
    "classify",
    "default_policy",
    "detect_era",
    "header_confirmation_gate",
    "json_type",
    "match_pattern",
    "mount_mcp",
    "tool_path",
]
