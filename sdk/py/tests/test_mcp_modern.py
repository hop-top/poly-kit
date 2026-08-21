"""The modern handler's validation chain, V1 through V9.

The chain's *order* is as normative as its contents: several checks would
produce a different error code if they ran later, and a client that
retries on the wrong code loops. Each case below pins one step by
constructing a request that fails two checks at once and asserting the
earlier one wins.
"""

from __future__ import annotations

import base64
import json

import pytest

from hop_top_kit.mcp import Bridge, Command, Headers, Request, Result, mount_mcp
from hop_top_kit.mcp.protocol import (
    ERR_HEADER_MISMATCH,
    ERR_INVALID_PARAMS,
    ERR_INVALID_REQUEST,
    ERR_METHOD_NOT_FOUND,
    ERR_UNSUPPORTED_VERSION,
    META_CLIENT_CAPABILITIES,
    META_CLIENT_INFO,
    META_PROTOCOL_VERSION,
    MODERN_PROTOCOL_VERSION,
)

MODERN_META = {
    META_PROTOCOL_VERSION: MODERN_PROTOCOL_VERSION,
    META_CLIENT_CAPABILITIES: {},
}


def surface(**kwargs):
    root = Command(
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
            Command(
                name="boom",
                short="Explodes",
                run=lambda flags: Result(stdout="out", stderr="bad", exit_code=3),
            ),
            Command(
                name="structured",
                short="Structured",
                run=lambda flags: Result(stdout="ok\n", data={"b": 2, "a": 1}),
            ),
        ],
    )
    return mount_mcp(Bridge(root), **kwargs)


def send(app, body, headers: dict[str, str] | None = None):
    raw = body if isinstance(body, str) else json.dumps(body)
    response = app.handle(
        Request(
            method="POST",
            path="/mcp",
            headers=Headers.from_mapping(headers),
            body=raw.encode("utf-8"),
        )
    )
    return response, (json.loads(response.body) if response.body else None)


def modern_headers(method: str, name: str | None = None) -> dict[str, str]:
    headers = {
        "MCP-Protocol-Version": MODERN_PROTOCOL_VERSION,
        "Mcp-Method": method,
    }
    if name is not None:
        headers["Mcp-Name"] = name
    return headers


# --- V1 / V2 ---------------------------------------------------------------


def test_v1_rejects_a_wrong_jsonrpc_version() -> None:
    response, body = send(
        surface(),
        {"jsonrpc": "1.0", "id": 1, "method": "tools/list", "params": {"_meta": MODERN_META}},
        modern_headers("tools/list"),
    )
    assert response.status == 400
    assert body["error"]["code"] == ERR_INVALID_REQUEST


def test_v1_tolerates_an_absent_jsonrpc_member() -> None:
    """Same tolerance as legacy: absent is accepted, wrong is not."""
    response, _ = send(
        surface(),
        {"id": 1, "method": "tools/list", "params": {"_meta": MODERN_META}},
        modern_headers("tools/list"),
    )
    assert response.status == 200


@pytest.mark.parametrize("bad_id", [None, True, 1.5, {"a": 1}, [1]])
def test_v2_rejects_malformed_ids(bad_id) -> None:
    """Only a string or a fractionless number is a legal modern id."""
    response, body = send(
        surface(),
        {
            "jsonrpc": "2.0",
            "id": bad_id,
            "method": "tools/list",
            "params": {"_meta": MODERN_META},
        },
        modern_headers("tools/list"),
    )
    assert response.status == 400
    assert body["error"]["code"] == ERR_INVALID_REQUEST


@pytest.mark.parametrize("good_id", ["abc", 1, -7, 0])
def test_v2_accepts_strings_and_integers(good_id) -> None:
    response, _ = send(
        surface(),
        {
            "jsonrpc": "2.0",
            "id": good_id,
            "method": "tools/list",
            "params": {"_meta": MODERN_META},
        },
        modern_headers("tools/list"),
    )
    assert response.status == 200


# --- V3 --------------------------------------------------------------------


@pytest.mark.parametrize(
    "params",
    [
        None,
        {},
        {"_meta": {}},
        {"_meta": {META_PROTOCOL_VERSION: MODERN_PROTOCOL_VERSION}},
        {"_meta": {META_CLIENT_CAPABILITIES: {}}},
        {"_meta": "not an object"},
    ],
)
def test_v3_requires_both_reserved_meta_keys(params) -> None:
    body = {"jsonrpc": "2.0", "id": 1, "method": "tools/list"}
    if params is not None:
        body["params"] = params
    response, decoded = send(surface(), body, modern_headers("tools/list"))
    assert response.status == 400
    assert decoded["error"]["code"] == ERR_INVALID_PARAMS


def test_v3_treats_client_info_as_optional() -> None:
    """``clientInfo`` only feeds audit metadata, so it is never required."""
    response, _ = send(
        surface(),
        {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "tools/list",
            "params": {
                "_meta": {
                    **MODERN_META,
                    META_CLIENT_INFO: {"name": "probe", "version": "1"},
                }
            },
        },
        modern_headers("tools/list"),
    )
    assert response.status == 200


def test_v3_runs_before_v4() -> None:
    """Missing ``_meta`` is ``-32602``, even with no headers at all.

    Both checks fail here; V3 is earlier, so params wins over headers.
    """
    response, body = send(surface(), {"jsonrpc": "2.0", "id": 1, "method": "server/discover"})
    assert response.status == 400
    assert body["error"]["code"] == ERR_INVALID_PARAMS


# --- V4 / V5 ---------------------------------------------------------------


def test_v4_requires_the_protocol_version_header() -> None:
    response, body = send(
        surface(),
        {"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": {"_meta": MODERN_META}},
        {"Mcp-Method": "tools/list"},
    )
    assert response.status == 400
    assert body["error"]["code"] == ERR_HEADER_MISMATCH


def test_v4_requires_header_and_meta_to_agree() -> None:
    response, body = send(
        surface(),
        {"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": {"_meta": MODERN_META}},
        {"MCP-Protocol-Version": "2025-06-18", "Mcp-Method": "tools/list"},
    )
    assert response.status == 400
    assert body["error"]["code"] == ERR_HEADER_MISMATCH


def test_v4_runs_before_v5() -> None:
    """A header/``_meta`` disagreement is ``-32020``, not ``-32022``.

    The version in ``_meta`` is unsupported *and* contradicts the header.
    V4 is earlier, so the client is told to fix its headers rather than
    sent into a version-renegotiation loop it cannot win.
    """
    response, body = send(
        surface(),
        {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "tools/list",
            "params": {"_meta": {**MODERN_META, META_PROTOCOL_VERSION: "2099-01-01"}},
        },
        {"MCP-Protocol-Version": MODERN_PROTOCOL_VERSION, "Mcp-Method": "tools/list"},
    )
    assert response.status == 400
    assert body["error"]["code"] == ERR_HEADER_MISMATCH


def test_v5_supported_list_excludes_the_legacy_revision() -> None:
    """``supported`` names per-request versions only.

    2024-11-05 is reachable only through its handshake, so advertising it
    in a retry hint would send a modern client into a dead end.
    """
    response, body = send(
        surface(),
        {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "tools/list",
            "params": {"_meta": {**MODERN_META, META_PROTOCOL_VERSION: "2099-01-01"}},
        },
        {"MCP-Protocol-Version": "2099-01-01", "Mcp-Method": "tools/list"},
    )
    assert response.status == 400
    assert body["error"]["code"] == ERR_UNSUPPORTED_VERSION
    assert body["error"]["data"]["supported"] == [MODERN_PROTOCOL_VERSION]
    assert "2024-11-05" not in body["error"]["data"]["supported"]


# --- V6 / V7: header agreement and duplicates -------------------------------


def test_v6_requires_the_method_header_to_match_the_body() -> None:
    response, body = send(
        surface(),
        {"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": {"_meta": MODERN_META}},
        {"MCP-Protocol-Version": MODERN_PROTOCOL_VERSION, "Mcp-Method": "tools/call"},
    )
    assert response.status == 400
    assert body["error"]["code"] == ERR_HEADER_MISMATCH


def test_identical_duplicate_headers_are_tolerated() -> None:
    """Benign proxy duplication must not break a conforming request."""
    headers = Headers(
        [
            ("MCP-Protocol-Version", MODERN_PROTOCOL_VERSION),
            ("MCP-Protocol-Version", MODERN_PROTOCOL_VERSION),
            ("Mcp-Method", "tools/list"),
        ]
    )
    body = json.dumps(
        {"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": {"_meta": MODERN_META}}
    ).encode()
    response = surface().handle(Request(headers=headers, body=body))
    assert response.status == 200


def test_conflicting_duplicate_headers_are_a_failure_in_their_own_right() -> None:
    """Two sources of truth is exactly what the agreement check closes.

    The server must not pick one occurrence and execute on it while a
    load balancer trusted the other.
    """
    headers = Headers(
        [
            ("MCP-Protocol-Version", MODERN_PROTOCOL_VERSION),
            ("MCP-Protocol-Version", "2025-06-18"),
            ("Mcp-Method", "tools/list"),
        ]
    )
    body = json.dumps(
        {"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": {"_meta": MODERN_META}}
    ).encode()
    response = surface().handle(Request(headers=headers, body=body))
    assert response.status == 400
    assert json.loads(response.body)["error"]["code"] == ERR_HEADER_MISMATCH


def test_v7_requires_the_name_header_on_tools_call() -> None:
    response, body = send(
        surface(),
        {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "tools/call",
            "params": {"name": "ping", "_meta": MODERN_META},
        },
        modern_headers("tools/call"),
    )
    assert response.status == 400
    assert body["error"]["code"] == ERR_HEADER_MISMATCH


def test_v7_runs_before_the_params_decode() -> None:
    """A missing ``Mcp-Name`` is ``-32020`` even with unparseable params.

    Headers are the signal a gateway trusts without reading the body, so
    an absent one is malformed at the header layer — decided ahead of any
    body-shape check, and without needing the body to have decoded.
    """
    response, body = send(
        surface(),
        {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "tools/call",
            "params": {"_meta": MODERN_META, "arguments": "not an object"},
        },
        modern_headers("tools/call"),
    )
    assert response.status == 400
    assert body["error"]["code"] == ERR_HEADER_MISMATCH


def test_v7_decodes_the_base64_sentinel_before_comparing() -> None:
    encoded = base64.b64encode(b"ping").decode()
    response, _ = send(
        surface(),
        {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "tools/call",
            "params": {"name": "ping", "_meta": MODERN_META},
        },
        modern_headers("tools/call", f"=?base64?{encoded}?="),
    )
    assert response.status == 200


def test_an_empty_sentinel_counts_as_an_empty_header() -> None:
    response, body = send(
        surface(),
        {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "tools/call",
            "params": {"name": "ping", "_meta": MODERN_META},
        },
        modern_headers("tools/call", "=?base64??="),
    )
    assert response.status == 400
    assert body["error"]["code"] == ERR_HEADER_MISMATCH


def test_a_malformed_sentinel_fails_closed() -> None:
    """A value that merely *looks* encoded is always treated as encoded.

    Falling back to a literal comparison would let a tampered header win
    by being undecodable.
    """
    response, body = send(
        surface(),
        {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "tools/call",
            "params": {"name": "ping", "_meta": MODERN_META},
        },
        modern_headers("tools/call", "=?base64?!!!not-base64!!!?="),
    )
    assert response.status == 400
    assert body["error"]["code"] == ERR_HEADER_MISMATCH


def test_the_name_header_is_ignored_on_other_methods() -> None:
    """V7 applies to ``tools/call`` only."""
    response, _ = send(
        surface(),
        {"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": {"_meta": MODERN_META}},
        modern_headers("tools/list", "irrelevant"),
    )
    assert response.status == 200


# --- V8 / V9 and the call path ---------------------------------------------


def test_v8_answers_an_unknown_method_with_404() -> None:
    response, body = send(
        surface(),
        {"jsonrpc": "2.0", "id": 1, "method": "prompts/list", "params": {"_meta": MODERN_META}},
        modern_headers("prompts/list"),
    )
    assert response.status == 404
    assert body["error"]["code"] == ERR_METHOD_NOT_FOUND


def test_v9_answers_an_unknown_tool_at_http_200() -> None:
    """A params-level problem is an application error, not a transport one."""
    response, body = send(
        surface(),
        {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "tools/call",
            "params": {"name": "nope", "_meta": MODERN_META},
        },
        modern_headers("tools/call", "nope"),
    )
    assert response.status == 200
    assert body["error"]["code"] == ERR_INVALID_PARAMS


def test_auth_required_leaf_refuses_without_a_bearer() -> None:
    response, body = send(
        surface(),
        {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "tools/call",
            "params": {"name": "secret", "_meta": MODERN_META},
        },
        modern_headers("tools/call", "secret"),
    )
    assert response.status == 401
    assert body["result"]["isError"] is True
    assert body["result"]["resultType"] == "complete"


def test_a_nonzero_exit_code_is_an_error_result_not_a_transport_error() -> None:
    """A failing tool is a complete result flagged ``isError``, at HTTP 200."""
    response, body = send(
        surface(),
        {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "tools/call",
            "params": {"name": "boom", "_meta": MODERN_META},
        },
        modern_headers("tools/call", "boom"),
    )
    assert response.status == 200
    result = body["result"]
    assert result["isError"] is True
    assert result["resultType"] == "complete"
    assert result["content"][0]["text"] == "out"
    assert result["content"][1]["text"] == "[stderr] bad"


def test_structured_data_appears_twice_by_design() -> None:
    """Once as ``structuredContent``, once as its serialised text block.

    The text block doubles as the spec-recommended serialised fallback
    for clients that do not read ``structuredContent``.
    """
    response, body = send(
        surface(),
        {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "tools/call",
            "params": {"name": "structured", "_meta": MODERN_META},
        },
        modern_headers("tools/call", "structured"),
    )
    assert response.status == 200
    result = body["result"]
    assert result["structuredContent"] == {"a": 1, "b": 2}
    assert json.loads(result["content"][1]["text"]) == {"a": 1, "b": 2}


def test_legacy_call_omits_structured_content() -> None:
    """``structuredContent`` is a modern addition, never back-ported.

    The legacy renderer still emits the serialised text block, so no
    information is lost — but the member itself must not appear, or the
    byte-for-byte preservation rule is broken.
    """
    response, body = send(
        surface(),
        {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "tools/call",
            "params": {"name": "structured"},
        },
    )
    assert response.status == 200
    assert "structuredContent" not in body["result"]
    assert json.loads(body["result"]["content"][1]["text"]) == {"a": 1, "b": 2}


def test_every_modern_result_is_stamped_with_server_info() -> None:
    """The result-level ``_meta`` always carries ``serverInfo``."""
    from hop_top_kit.mcp.protocol import META_SERVER_INFO

    app = surface(server_name="probe", server_version="9.9.9")
    for method in ("server/discover", "tools/list"):
        _, body = send(
            app,
            {"jsonrpc": "2.0", "id": 1, "method": method, "params": {"_meta": MODERN_META}},
            modern_headers(method),
        )
        info = body["result"]["_meta"][META_SERVER_INFO]
        assert info == {"name": "probe", "version": "9.9.9"}


def test_discover_omits_unimplemented_members() -> None:
    """No ``listChanged``, no ``instructions``, no empty ``extensions`` map."""
    _, body = send(
        surface(),
        {"jsonrpc": "2.0", "id": 1, "method": "server/discover", "params": {"_meta": MODERN_META}},
        modern_headers("server/discover"),
    )
    result = body["result"]
    assert result["supportedVersions"] == [MODERN_PROTOCOL_VERSION]
    assert result["capabilities"] == {"tools": {}}
    assert "instructions" not in result
    assert "listChanged" not in json.dumps(result)


def test_tools_list_ignores_a_cursor_and_returns_no_next_cursor() -> None:
    """Pagination is deliberately not implemented."""
    _, body = send(
        surface(),
        {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "tools/list",
            "params": {"_meta": MODERN_META, "cursor": "abc"},
        },
        modern_headers("tools/list"),
    )
    assert "nextCursor" not in body["result"]
    assert len(body["result"]["tools"]) == 4


# --- origin allowlist -------------------------------------------------------


def test_origin_check_is_off_by_default() -> None:
    """kit cannot judge origin validity without adopter input."""
    response, _ = send(
        surface(),
        {"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": {"_meta": MODERN_META}},
        {**modern_headers("tools/list"), "Origin": "https://evil.example"},
    )
    assert response.status == 200


def test_a_configured_allowlist_refuses_other_origins() -> None:
    app = surface(origin_allowlist=("https://app.example",))
    headers = modern_headers("tools/list")
    body = {"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": {"_meta": MODERN_META}}

    allowed, _ = send(app, body, {**headers, "Origin": "https://app.example"})
    assert allowed.status == 200

    refused, _ = send(app, body, {**headers, "Origin": "https://evil.example"})
    assert refused.status == 403


def test_a_request_without_an_origin_is_never_refused() -> None:
    """Non-browser clients send no Origin and must not be locked out."""
    app = surface(origin_allowlist=("https://app.example",))
    response, _ = send(
        app,
        {"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": {"_meta": MODERN_META}},
        modern_headers("tools/list"),
    )
    assert response.status == 200


# --- single-era mounts ------------------------------------------------------


def test_legacy_only_mount_ignores_markers() -> None:
    """With no modern handler mounted, markers mean nothing again."""
    app = surface(spec_versions=("2024-11-05",))
    response, body = send(
        app,
        {"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": {"_meta": MODERN_META}},
        modern_headers("tools/list"),
    )
    assert response.status == 200
    assert set(body["result"]) == {"tools"}


def test_modern_only_mount_rejects_a_bare_initialize() -> None:
    """No legacy handler means no demotion; ``initialize`` fails validation.

    The message names the supported versions because a legacy client has
    no fall-forward mechanism and this text is its only recovery hint.
    """
    app = surface(spec_versions=(MODERN_PROTOCOL_VERSION,))
    response, body = send(app, {"jsonrpc": "2.0", "id": 1, "method": "initialize"})
    assert response.status == 400
    assert MODERN_PROTOCOL_VERSION in body["error"]["message"]
