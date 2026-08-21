"""Era-detection rules the shared wire fixtures cannot reach.

The fixtures are the parity contract, but they are not complete coverage
of the detection rules. Two gaps matter enough to close here:

- The only fixture carrying a bare ``params._meta`` sends it on an
  ``initialize`` request, which D2 routes legacy *before* the marker set
  is ever consulted. So the fixtures pass whether or not bare ``_meta`` is
  treated as a modern marker — precisely the silent divergence the
  non-marker rule exists to prevent. The cases below send bare ``_meta``
  on ``tools/list`` and ``tools/call``, where M3 is actually reached.
- Marker precedence over an incomplete modern envelope (D3's "not demoted
  to legacy" rule) has no fixture at all, because every fixture's modern
  request is well-formed.
"""

from __future__ import annotations

import json

import pytest

from hop_top_kit.mcp import (
    ERA_LEGACY,
    ERA_MODERN,
    Bridge,
    Command,
    Flag,
    Headers,
    Request,
    Result,
    detect_era,
    mount_mcp,
)
from hop_top_kit.mcp.protocol import (
    ERR_HEADER_MISMATCH,
    ERR_INVALID_PARAMS,
    ERR_INVALID_REQUEST,
    ERR_METHOD_NOT_FOUND,
    META_CLIENT_CAPABILITIES,
    META_PROTOCOL_VERSION,
    MODERN_PROTOCOL_VERSION,
    parse_rpc,
)


def tree() -> Command:
    return Command(
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


def surface():
    return mount_mcp(Bridge(tree()))


def post(body: dict | str, headers: dict[str, str] | None = None) -> Request:
    raw = body if isinstance(body, str) else json.dumps(body)
    return Request(
        method="POST",
        path="/mcp",
        headers=Headers.from_mapping(headers),
        body=raw.encode("utf-8"),
    )


def era_of(body: dict | str, headers: dict[str, str] | None = None) -> str:
    request = post(body, headers)
    return detect_era(request, parse_rpc(request.body))


MODERN_META = {
    META_PROTOCOL_VERSION: MODERN_PROTOCOL_VERSION,
    META_CLIENT_CAPABILITIES: {},
}


# --- the two deliberate non-markers ---------------------------------------


@pytest.mark.parametrize("method", ["tools/list", "tools/call"])
def test_bare_meta_is_not_a_modern_marker(method: str) -> None:
    """``_meta`` without the reserved key stays legacy.

    2024-11-05 clients legitimately send ``_meta.progressToken`` and OTel
    trace context. Routing those modern would reject working legacy
    traffic with modern errors it cannot interpret.
    """
    assert (
        era_of(
            {
                "jsonrpc": "2.0",
                "id": 1,
                "method": method,
                "params": {"_meta": {"progressToken": "p1"}, "name": "ping"},
            }
        )
        == ERA_LEGACY
    )


def test_bare_meta_with_otel_context_is_not_a_marker() -> None:
    """OTel trace headers inside ``_meta`` are not a modern signal either."""
    assert (
        era_of(
            {
                "jsonrpc": "2.0",
                "id": 1,
                "method": "tools/list",
                "params": {
                    "_meta": {
                        "traceparent": "00-abc-def-01",
                        "tracestate": "x=1",
                        "baggage": "k=v",
                    }
                },
            }
        )
        == ERA_LEGACY
    )


def test_bare_meta_still_gets_a_legacy_response() -> None:
    """The end-to-end consequence: a legacy answer, not a modern error."""
    response = surface().handle(
        post(
            {
                "jsonrpc": "2.0",
                "id": 1,
                "method": "tools/list",
                "params": {"_meta": {"progressToken": "p1"}},
            }
        )
    )
    assert response.status == 200
    body = json.loads(response.body)
    # A modern tools/list would carry resultType, ttlMs, and cacheScope.
    assert set(body["result"]) == {"tools"}


@pytest.mark.parametrize("version", ["2024-11-05", "2025-06-18", "2026-07-28"])
def test_protocol_version_header_is_not_a_modern_marker(version: str) -> None:
    """The header predates 2026-07-28 and cannot route on its own.

    Clients that negotiated *down* to legacy through the handshake send
    this header on every subsequent request. Treating it as a modern
    signal would serve their handshake and then brick the session — and
    nothing is lost, since a conforming modern request always carries
    ``Mcp-Method`` and the reserved ``_meta`` key too.
    """
    assert (
        era_of(
            {"jsonrpc": "2.0", "id": 1, "method": "tools/list"},
            {"MCP-Protocol-Version": version},
        )
        == ERA_LEGACY
    )


# --- the marker set --------------------------------------------------------


def test_mcp_method_header_routes_modern() -> None:
    """M1: the ``Mcp-Method`` header alone is a marker."""
    assert (
        era_of(
            {"jsonrpc": "2.0", "id": 1, "method": "tools/list"},
            {"Mcp-Method": "tools/list"},
        )
        == ERA_MODERN
    )


def test_mcp_name_header_routes_modern() -> None:
    """M2: the ``Mcp-Name`` header alone is a marker."""
    assert (
        era_of(
            {"jsonrpc": "2.0", "id": 1, "method": "tools/call"},
            {"Mcp-Name": "ping"},
        )
        == ERA_MODERN
    )


def test_reserved_meta_key_routes_modern() -> None:
    """M3: the reserved key routes modern — presence only, value unread."""
    assert (
        era_of(
            {
                "jsonrpc": "2.0",
                "id": 1,
                "method": "tools/list",
                "params": {"_meta": {META_PROTOCOL_VERSION: "anything at all"}},
            }
        )
        == ERA_MODERN
    )


def test_server_discover_routes_modern() -> None:
    """M4: the method alone is a marker, with no headers and no ``_meta``."""
    assert era_of({"jsonrpc": "2.0", "id": 1, "method": "server/discover"}) == ERA_MODERN


# --- precedence ------------------------------------------------------------


def test_initialize_beats_every_marker() -> None:
    """D2: ``initialize`` is legacy unconditionally.

    The spec's two dual-era bullets conflict here; method-wins is pinned
    because a working handshake is the most recoverable answer for a
    confused client.
    """
    assert (
        era_of(
            {
                "jsonrpc": "2.0",
                "id": 1,
                "method": "initialize",
                "params": {"_meta": MODERN_META},
            },
            {"Mcp-Method": "initialize", "Mcp-Name": "ping"},
        )
        == ERA_LEGACY
    )


def test_incomplete_modern_request_is_not_demoted() -> None:
    """D3: a marker routes modern even when the envelope is incomplete.

    Demoting to legacy would hand a modern client a non-spec error and
    defeat the fallback algorithm, which keys on recognising a *modern*
    error body.
    """
    response = surface().handle(
        post(
            {"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": "ping"}},
            {"Mcp-Method": "tools/call"},
        )
    )
    assert response.status == 400
    assert json.loads(response.body)["error"]["code"] == ERR_INVALID_PARAMS


def test_no_markers_routes_legacy() -> None:
    """D4: the byte-for-byte preservation path."""
    assert era_of({"jsonrpc": "2.0", "id": 1, "method": "tools/list"}) == ERA_LEGACY


# --- worked edge cases from the detection table ----------------------------


def test_unknown_method_is_404_on_modern_but_200_on_legacy() -> None:
    """The deliberate status asymmetry between the two eras.

    Legacy answers method-not-found with HTTP 200 and the modern handler
    with 404. Changing legacy's status would violate byte-for-byte
    preservation, so the asymmetry is kept on purpose.
    """
    legacy = surface().handle(post({"jsonrpc": "2.0", "id": 1, "method": "nope"}))
    assert legacy.status == 200
    assert json.loads(legacy.body)["error"]["code"] == ERR_METHOD_NOT_FOUND

    modern = surface().handle(
        post(
            {
                "jsonrpc": "2.0",
                "id": 1,
                "method": "nope",
                "params": {"_meta": MODERN_META},
            },
            {"MCP-Protocol-Version": MODERN_PROTOCOL_VERSION, "Mcp-Method": "nope"},
        )
    )
    assert modern.status == 404
    assert json.loads(modern.body)["error"]["code"] == ERR_METHOD_NOT_FOUND


def test_notification_with_markers_is_accepted_and_discarded() -> None:
    """A modern request with no ``id`` is a notification: 202, empty body."""
    response = surface().handle(
        post(
            {
                "jsonrpc": "2.0",
                "method": "tools/list",
                "params": {"_meta": MODERN_META},
            },
            {"MCP-Protocol-Version": MODERN_PROTOCOL_VERSION, "Mcp-Method": "tools/list"},
        )
    )
    assert response.status == 202
    assert response.body == b""


def test_null_id_with_markers_is_malformed_on_modern() -> None:
    """``null`` is a legal base JSON-RPC id but forbidden by this revision."""
    response = surface().handle(
        post(
            {
                "jsonrpc": "2.0",
                "id": None,
                "method": "tools/list",
                "params": {"_meta": MODERN_META},
            },
            {"MCP-Protocol-Version": MODERN_PROTOCOL_VERSION, "Mcp-Method": "tools/list"},
        )
    )
    assert response.status == 400
    assert json.loads(response.body)["error"]["code"] == ERR_INVALID_REQUEST


def test_null_id_is_echoed_verbatim_on_legacy() -> None:
    """Legacy round-trips the id it was given, ``null`` included."""
    response = surface().handle(post({"jsonrpc": "2.0", "id": None, "method": "initialize"}))
    assert response.status == 200
    assert b'"id":null' in response.body


def test_meta_marker_without_headers_reaches_v4() -> None:
    """M3 alone routes modern; the missing header then fails V4.

    A complete ``_meta`` with no headers gets ``-32020``, not ``-32602``:
    V3 passes, and the first thing missing is the protocol-version
    header.
    """
    response = surface().handle(
        post(
            {
                "jsonrpc": "2.0",
                "id": 1,
                "method": "tools/call",
                "params": {"name": "ping", "_meta": MODERN_META},
            }
        )
    )
    assert response.status == 400
    assert json.loads(response.body)["error"]["code"] == ERR_HEADER_MISMATCH


def test_parse_error_is_identical_regardless_of_headers() -> None:
    """D1: an unparseable body answers the same whatever headers arrive."""
    bare = surface().handle(post("{not json"))
    marked = surface().handle(post("{not json", {"Mcp-Method": "tools/call", "Mcp-Name": "ping"}))
    assert bare.status == marked.status == 400
    assert bare.body == marked.body


# --- lazy help-flag registration -------------------------------------------
#
# The fixture's sequence pins the observable wire consequence: one
# tools/call on ping, and the next tools/list reports a help property.
# It cannot reach the properties below, because it invokes exactly one
# leaf exactly once. Each of these is a way the mechanism could be wrong
# while still replaying that sequence byte-exactly.


def help_tree() -> Command:
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
                name="other",
                short="Other",
                run=lambda flags: Result(stdout="other\n"),
            ),
        ],
    )


def leaf_properties(app, tool: str) -> dict:
    """The declared inputSchema properties of one tool, via tools/list."""
    response = app.handle(post({"jsonrpc": "2.0", "id": 1, "method": "tools/list"}))
    tools = json.loads(response.body)["result"]["tools"]
    return next(t for t in tools if t["name"] == tool)["inputSchema"]["properties"]


def invoke(app, tool: str):
    return app.handle(
        post(
            {
                "jsonrpc": "2.0",
                "id": 1,
                "method": "tools/call",
                "params": {"name": tool},
            }
        )
    )


def test_repeated_invocation_attaches_the_help_flag_once() -> None:
    """Attachment is idempotent, so the schema cannot grow without bound.

    A non-idempotent implementation still replays the fixture's sequence
    byte-exactly, because that sequence invokes its leaf exactly once.
    Here the leaf is invoked repeatedly, which is what an adopter's
    long-lived process does — and where a duplicate-appending bug turns
    into an ever-growing ``inputSchema``.
    """
    tree = help_tree()
    app = mount_mcp(Bridge(tree))
    for _ in range(5):
        invoke(app, "ping")

    ping = next(c for c in tree.children if c.name == "ping")
    assert [f.name for f in ping.flags] == ["help"]
    assert list(leaf_properties(app, "ping")) == ["help"]


def test_the_flag_attaches_only_to_the_invoked_leaf() -> None:
    """Invoking one command must not alter any other command's schema."""
    app = mount_mcp(Bridge(help_tree()))
    invoke(app, "ping")

    assert "help" in leaf_properties(app, "ping")
    assert leaf_properties(app, "other") == {}


def test_listing_alone_never_attaches_the_flag() -> None:
    """It is execution that registers the flag, not enumeration.

    Were listing enough, the very first ``tools/list`` would already
    report ``help`` and the fixture's before/after distinction would
    collapse.
    """
    app = mount_mcp(Bridge(help_tree()))
    for _ in range(3):
        leaf_properties(app, "ping")
    assert leaf_properties(app, "ping") == {}


def test_a_blocked_invocation_does_not_attach_the_flag() -> None:
    """A call refused before execution never reaches the registration point.

    The destructive ceiling rejects inside the bridge, ahead of the
    runner, so a blocked leaf's schema must be untouched — otherwise the
    schema would advertise an input for a command this surface will never
    run.
    """
    tree = Command(
        name="root",
        children=[
            Command(
                name="purge",
                short="Purge",
                run=lambda flags: Result(stdout="purged\n"),
                annotations={"kit/side-effect": "destructive"},
            )
        ],
    )
    app = mount_mcp(Bridge(tree))
    response = invoke(app, "purge")
    body = json.loads(response.body)
    assert body["result"]["isError"] is True

    assert leaf_properties(app, "purge") == {}


def test_a_failing_command_still_attaches_the_flag() -> None:
    """Registration happens on the execution path, not on success.

    cobra registers the flag when it runs the command, so a command that
    raises has still had its flag created by the time the error surfaces.
    """

    def explode(_flags):
        raise RuntimeError("boom")

    tree = Command(
        name="root",
        children=[Command(name="boom", short="Boom", run=explode)],
    )
    app = mount_mcp(Bridge(tree))
    body = json.loads(invoke(app, "boom").body)
    assert body["result"]["isError"] is True

    assert "help" in leaf_properties(app, "boom")


def test_a_caller_declared_help_flag_is_never_overwritten() -> None:
    """An explicit ``help`` flag wins; lazy registration defers to it."""
    tree = Command(
        name="root",
        children=[
            Command(
                name="ping",
                short="Ping",
                run=lambda flags: Result(),
                flags=[Flag("help", "custom help text", "string")],
            )
        ],
    )
    app = mount_mcp(Bridge(tree))
    invoke(app, "ping")
    assert leaf_properties(app, "ping") == {
        "help": {"type": "string", "description": "custom help text"}
    }


def test_lazy_help_flag_can_be_opted_out() -> None:
    """A tree not built on cobra should not inherit cobra's quirk.

    The behaviour is real for kit's own cobra-backed trees, but it is a
    property of that framework rather than of MCP, so an adopter driving
    a different one can switch it off.
    """
    tree = Command(
        name="root",
        children=[
            Command(
                name="ping",
                short="Ping",
                run=lambda flags: Result(),
                lazy_help_flag=False,
            )
        ],
    )
    app = mount_mcp(Bridge(tree))
    invoke(app, "ping")
    assert leaf_properties(app, "ping") == {}
