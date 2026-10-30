"""The ``tool_name_separator`` mount option.

Tool names join the leaf path with the separator in ``tools/list``,
resolve back through it on ``tools/call`` in both eras, and a separator
a command name contains fails the mount.
"""

from __future__ import annotations

import json

import pytest

from hop_top_kit.mcp import (
    Bridge,
    Command,
    Flag,
    Headers,
    MountError,
    Request,
    Result,
    mount_mcp,
    tool_name,
    tool_path,
)
from hop_top_kit.mcp.modern_confirm import Binding, ElicitationConfirmationGate
from hop_top_kit.mcp.protocol import (
    META_CLIENT_CAPABILITIES,
    META_PROTOCOL_VERSION,
    MODERN_PROTOCOL_VERSION,
)


def tree() -> Command:
    return Command(
        name="root",
        children=[
            Command(
                name="ping",
                short="Ping the server",
                run=lambda flags: Result(stdout="pong\n"),
            ),
            Command(
                name="widget",
                children=[
                    Command(
                        name="add",
                        short="Add a widget",
                        flags=[Flag(name="name", type="string")],
                        run=lambda flags: Result(stdout="added\n"),
                        annotations={"kit/side-effect": "write"},
                    ),
                ],
            ),
        ],
    )


def colliding_tree() -> Command:
    """``data_sync`` and ``data sync`` read alike under ``_``."""
    run = lambda flags: Result(stdout="")  # noqa: E731
    return Command(
        name="root",
        children=[
            Command(name="data_sync", run=run),
            Command(name="data", children=[Command(name="sync", run=run)]),
        ],
    )


def post(body: dict, headers: dict[str, str] | None = None) -> Request:
    return Request(
        method="POST",
        path="/mcp",
        headers=Headers.from_mapping(headers),
        body=json.dumps(body).encode("utf-8"),
    )


def listed(**options: object) -> list[str]:
    response = mount_mcp(Bridge(tree()), **options).handle(
        post({"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
    )
    return [tool["name"] for tool in json.loads(response.body)["result"]["tools"]]


def call(surface, name: str, *, modern: bool = False) -> dict:
    params: dict = {"name": name, "arguments": {"name": "bolt"}}
    headers = None
    if modern:
        params["_meta"] = {
            META_PROTOCOL_VERSION: MODERN_PROTOCOL_VERSION,
            META_CLIENT_CAPABILITIES: {},
        }
        headers = {
            "MCP-Protocol-Version": MODERN_PROTOCOL_VERSION,
            "Mcp-Method": "tools/call",
            "Mcp-Name": name,
        }
    response = surface.handle(
        post({"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": params}, headers)
    )
    return json.loads(response.body)


def test_tool_name_and_tool_path_default_to_a_dot() -> None:
    assert tool_name(("widget", "add")) == "widget.add"
    assert tool_path("widget.add") == ("widget", "add")


@pytest.mark.parametrize("sep", ["_", "-", "__"])
def test_tool_name_and_tool_path_are_symmetric(sep: str) -> None:
    name = tool_name(("widget", "add"), sep)
    assert name == f"widget{sep}add"
    assert tool_path(name, sep) == ("widget", "add")
    assert tool_path("", sep) == ()


def test_default_lists_dotted_names() -> None:
    assert "widget.add" in listed()


@pytest.mark.parametrize("sep", ["_", "-"])
def test_separator_renames_listed_tools(sep: str) -> None:
    names = listed(tool_name_separator=sep)
    assert f"widget{sep}add" in names
    assert "ping" in names
    assert "widget.add" not in names


def test_separator_resolves_a_legacy_call() -> None:
    surface = mount_mcp(Bridge(tree()), tool_name_separator="_")
    assert call(surface, "widget_add")["result"]["isError"] is False
    assert "unknown tool: widget.add" in call(surface, "widget.add")["error"]["message"]


def test_separator_resolves_a_modern_call() -> None:
    surface = mount_mcp(Bridge(tree()), tool_name_separator="-")
    body = call(surface, "widget-add", modern=True)
    assert "error" not in body
    assert body["result"]["isError"] is False


def test_separator_names_the_tool_in_the_confirmation_prompt() -> None:
    bridge = Bridge(tree())
    leaf = next(leaf for leaf in bridge.leaves() if leaf.path == ("widget", "add"))
    gate = ElicitationConfirmationGate(b"k", bridge, tool_name_separator="_")
    prompt = gate.input_required(leaf, Binding("widget add", "", ""))
    message = prompt["inputRequests"]["confirm"]["params"]["message"]
    assert message == 'Approve execution of "widget_add"?'


def test_empty_separator_is_refused() -> None:
    with pytest.raises(MountError, match="tool_name_separator: empty separator"):
        mount_mcp(Bridge(tree()), tool_name_separator="")


def test_separator_inside_a_command_name_is_refused() -> None:
    with pytest.raises(
        MountError,
        match=r"tool_name_separator: command 'data_sync' in 'data_sync' contains separator '_'",
    ):
        mount_mcp(Bridge(colliding_tree()), tool_name_separator="_")


def test_separator_no_command_name_contains_is_accepted() -> None:
    mount_mcp(Bridge(colliding_tree()), tool_name_separator="-")
