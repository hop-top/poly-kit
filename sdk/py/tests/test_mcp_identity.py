"""The kit/auth-required gate on both eras.

Only the mount's verifier establishes a caller. An ``Authorization``
header is presence, not verification, and a caller or scopes the request
claims never become identity.
"""

from __future__ import annotations

import json
from typing import Any

import pytest

from hop_top_kit.mcp import (
    ESTABLISHED_VERIFIED,
    Bridge,
    Command,
    Headers,
    Identity,
    Invocation,
    Request,
    Result,
    mount_mcp,
)
from hop_top_kit.mcp.protocol import (
    META_CLIENT_CAPABILITIES,
    META_CLIENT_INFO,
    META_PROTOCOL_VERSION,
    MODERN_PROTOCOL_VERSION,
)

ERAS = ("legacy", "modern")

#: A caller and scopes the request claims in its own body.
CLAIMS = {"caller": "admin", "tenant": "root", "scopes": ["admin"]}


class RecordingBridge(Bridge):
    """A bridge that records every invocation it runs."""

    def __init__(self, root: Command) -> None:
        super().__init__(root)
        self.calls: list[Invocation] = []

    def invoke(self, inv: Invocation) -> Result:
        self.calls.append(inv)
        return super().invoke(inv)


def tree() -> Command:
    return Command(
        name="root",
        children=[
            Command(
                name="ping",
                short="Ping the server",
                run=lambda flags: Result(stdout="pong\n"),
                annotations={"kit/side-effect": "read"},
            ),
            Command(
                name="secret",
                short="Locked",
                run=lambda flags: Result(),
                annotations={"kit/auth-required": "true"},
            ),
        ],
    )


def verify_good(request: Request) -> Identity | None:
    """Accept exactly ``Bearer good`` as alice in acme."""
    if request.headers.get("authorization") == "Bearer good":
        return Identity(caller="alice", tenant="acme", scopes=("read", "write"))
    return None


def mount(**options: Any):
    bridge = RecordingBridge(tree())
    return mount_mcp(bridge, **options), bridge


def call(
    era: str,
    name: str,
    headers: dict[str, str] | None = None,
    claims: dict[str, Any] | None = None,
) -> Request:
    params: dict[str, Any] = {"name": name}
    all_headers = dict(headers or {})
    meta = dict(claims or {})
    if era == "modern":
        meta.update(
            {
                META_PROTOCOL_VERSION: MODERN_PROTOCOL_VERSION,
                META_CLIENT_CAPABILITIES: {},
                META_CLIENT_INFO: {"name": "admin", "version": "1"},
            }
        )
        all_headers.update(
            {
                "MCP-Protocol-Version": MODERN_PROTOCOL_VERSION,
                "Mcp-Method": "tools/call",
                "Mcp-Name": name,
            }
        )
    if meta:
        params["_meta"] = meta
    body = {"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params}
    return Request(
        method="POST",
        path="/mcp",
        headers=Headers.from_mapping(all_headers),
        body=json.dumps(body).encode("utf-8"),
    )


@pytest.mark.parametrize("era", ERAS)
def test_bare_authorization_header_is_refused(era: str) -> None:
    surface, bridge = mount()
    response = surface.handle(call(era, "secret", {"Authorization": "Bearer good"}, CLAIMS))
    assert response.status == 401
    assert response.headers["www-authenticate"] == "Bearer"
    body = json.loads(response.body)
    assert body["result"]["isError"] is True
    assert body["result"]["content"] == [{"type": "text", "text": "authentication required"}]
    assert bridge.calls == []


@pytest.mark.parametrize("era", ERAS)
def test_verifier_refusal_is_refused(era: str) -> None:
    surface, bridge = mount(verifier=verify_good)
    response = surface.handle(call(era, "secret", {"Authorization": "Bearer bad"}))
    assert response.status == 401
    assert response.headers["www-authenticate"] == "Bearer"
    assert bridge.calls == []


@pytest.mark.parametrize("era", ERAS)
def test_raising_verifier_is_refused(era: str) -> None:
    def broken(_request: Request) -> Identity | None:
        raise RuntimeError("verifier down")

    surface, bridge = mount(verifier=broken)
    response = surface.handle(call(era, "secret", {"Authorization": "Bearer good"}))
    assert response.status == 401
    assert bridge.calls == []


@pytest.mark.parametrize("era", ERAS)
def test_non_identity_verdict_is_refused(era: str) -> None:
    surface, bridge = mount(verifier=lambda _request: True)
    response = surface.handle(call(era, "secret", {"Authorization": "Bearer good"}))
    assert response.status == 401
    assert bridge.calls == []


@pytest.mark.parametrize("era", ERAS)
def test_verified_caller_is_admitted_with_its_identity(era: str) -> None:
    surface, bridge = mount(verifier=verify_good)
    response = surface.handle(call(era, "secret", {"Authorization": "Bearer good"}, CLAIMS))
    assert response.status == 200
    assert json.loads(response.body)["result"]["isError"] is False
    assert len(bridge.calls) == 1
    meta = bridge.calls[0].meta
    assert meta.established == ESTABLISHED_VERIFIED
    assert meta.authenticated
    assert meta.caller == "alice"
    assert meta.tenant == "acme"
    assert meta.extra["scopes"] == "read,write"


@pytest.mark.parametrize("era", ERAS)
def test_request_claims_never_become_identity(era: str) -> None:
    surface, bridge = mount()
    response = surface.handle(call(era, "ping", {"Authorization": "Bearer good"}, CLAIMS))
    assert response.status == 200
    meta = bridge.calls[0].meta
    assert meta.established == ""
    assert not meta.authenticated
    assert meta.caller == ""
    assert meta.tenant == ""
    assert "scopes" not in meta.extra


@pytest.mark.parametrize("era", ERAS)
def test_open_leaf_runs_unestablished_when_verifier_refuses(era: str) -> None:
    surface, bridge = mount(verifier=verify_good)
    response = surface.handle(call(era, "ping", {"Authorization": "Bearer bad"}))
    assert response.status == 200
    assert bridge.calls[0].meta.established == ""
    assert bridge.calls[0].meta.caller == ""
