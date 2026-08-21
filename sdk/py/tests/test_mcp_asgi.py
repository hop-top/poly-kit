"""The ASGI hosting contract.

ADR 0043 fixes the hosting model as a transport-agnostic handler that the
adopter binds to their own server: kit does not own anyone's HTTP stack,
and a surface that hard-bound to one would force that dependency on every
consumer and make this very suite spin up a socket to test wire bytes.

ASGI rather than WSGI is the deliberate half of that choice: the modern
era's streaming affordances are not expressible in WSGI, and the official
Python MCP SDK's modern transport is async. These cases drive the ASGI
callable directly, with no server involved.
"""

from __future__ import annotations

import asyncio
import json

import pytest

from hop_top_kit.mcp import Bridge, Command, Result, mount_mcp
from hop_top_kit.mcp.protocol import MODERN_PROTOCOL_VERSION


def surface():
    root = Command(
        name="root",
        children=[
            Command(
                name="ping",
                short="Ping the server",
                run=lambda flags: Result(stdout="pong\n"),
                annotations={"kit/side-effect": "read"},
            )
        ],
    )
    return mount_mcp(Bridge(root))


async def drive(
    app, body: bytes, *, method: str = "POST", headers: list[tuple[bytes, bytes]] = ()
):
    """Run one request through the ASGI callable, chunking the body."""
    scope = {
        "type": "http",
        "method": method,
        "path": "/mcp",
        "headers": list(headers),
    }
    # Two chunks, so the receive loop's more_body handling is exercised.
    messages = [
        {"type": "http.request", "body": body[:3], "more_body": True},
        {"type": "http.request", "body": body[3:], "more_body": False},
    ]
    sent: list[dict] = []

    async def receive():
        return messages.pop(0)

    async def send(message):
        sent.append(message)

    await app(scope, receive, send)
    return sent


def test_asgi_roundtrip_returns_the_same_bytes_as_handle() -> None:
    """The ASGI wrapper is a binding, not a second implementation."""
    app = surface()
    body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "tools/list"}).encode()
    sent = asyncio.run(drive(app, body))

    start, payload = sent
    assert start["type"] == "http.response.start"
    assert start["status"] == 200
    assert dict(start["headers"]) == {b"content-type": b"application/json"}

    from hop_top_kit.mcp import Headers, Request

    direct = app.handle(Request(body=body, headers=Headers()))
    assert payload["body"] == direct.body


def test_asgi_reassembles_a_chunked_body() -> None:
    """A body split across ASGI messages parses as one document."""
    app = surface()
    body = json.dumps({"jsonrpc": "2.0", "id": 9, "method": "initialize"}).encode()
    sent = asyncio.run(drive(app, body))
    assert json.loads(sent[1]["body"])["id"] == 9


def test_asgi_passes_headers_through_for_era_detection() -> None:
    """Headers survive the ASGI decode, so markers still route."""
    app = surface()
    body = json.dumps(
        {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "tools/list",
            "params": {
                "_meta": {
                    "io.modelcontextprotocol/protocolVersion": MODERN_PROTOCOL_VERSION,
                    "io.modelcontextprotocol/clientCapabilities": {},
                }
            },
        }
    ).encode()
    sent = asyncio.run(
        drive(
            app,
            body,
            headers=[
                (b"mcp-protocol-version", MODERN_PROTOCOL_VERSION.encode()),
                (b"mcp-method", b"tools/list"),
            ],
        )
    )
    result = json.loads(sent[1]["body"])["result"]
    # Modern-only members: the request routed to the modern handler.
    assert result["resultType"] == "complete"
    assert result["cacheScope"] == "private"


def test_session_era_verbs_answer_405() -> None:
    """Post-session servers refuse GET and DELETE at the mount path."""
    app = surface()
    for method in ("GET", "DELETE"):
        sent = asyncio.run(drive(app, b"", method=method))
        assert sent[0]["status"] == 405


def test_disconnect_before_the_body_arrives_sends_nothing() -> None:
    """A client that vanishes mid-request gets no response written."""
    app = surface()
    sent: list[dict] = []

    async def receive():
        return {"type": "http.disconnect"}

    async def send(message):  # pragma: no cover - must never run
        sent.append(message)

    asyncio.run(app({"type": "http", "method": "POST", "path": "/mcp", "headers": []}, receive, send))
    assert sent == []


def test_non_http_scopes_are_refused() -> None:
    """The surface serves HTTP; a lifespan or websocket scope is a wiring bug."""
    app = surface()

    async def receive():  # pragma: no cover - never reached
        return {}

    async def send(message):  # pragma: no cover - never reached
        pass

    with pytest.raises(RuntimeError):
        asyncio.run(app({"type": "websocket"}, receive, send))
