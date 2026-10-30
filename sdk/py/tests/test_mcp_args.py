"""Positional arguments over MCP, on both protocol revisions.

Mirrors the Go surface: a leaf that declares positionals publishes them
as one ``args`` property (an array of strings in declared order), and a
``tools/call`` ``args`` array is split out of the flags and handed to the
leaf as positional argv. The byte-exact half of this contract lives in
the ``positionals`` section of the shared wire fixture; these tests pin
the behaviour around it.
"""

from __future__ import annotations

import json
from collections.abc import Mapping, Sequence
from typing import Any

import pytest

from hop_top_kit.mcp import (
    Arg,
    Bridge,
    Command,
    Flag,
    Headers,
    Invocation,
    Request,
    Result,
    mount_mcp,
)
from hop_top_kit.mcp.protocol import (
    META_CLIENT_CAPABILITIES,
    META_PROTOCOL_VERSION,
    MODERN_PROTOCOL_VERSION,
)


def echo(verb: str):
    def run(flags: Mapping[str, Any], args: Sequence[str]) -> Result:
        extra = ""
        if "count" in flags:
            extra = f" x{flags['count']}"
        if "args" in flags:
            extra = f" --args={flags['args']}"
        return Result(stdout=f"{verb} {list(args)!r}{extra}\n")

    return run


def tree() -> Command:
    """Same shape as Go's args test tree, minus the undeclared leaf.

    item add      args name, note?; --count
    item pin      args id; an ``args`` flag holds the name
    item approve  args name; kit/requires-confirmation
    ping          no positionals
    """
    return Command(
        name="root",
        children=[
            Command(
                name="item",
                children=[
                    Command(
                        name="add",
                        short="Add an item",
                        run=echo("added"),
                        annotations={"kit/side-effect": "write"},
                        args=[Arg("name"), Arg("note", required=False)],
                        flags=[Flag("count", "how many", "int")],
                    ),
                    Command(
                        name="pin",
                        short="Pin an item",
                        run=echo("pinned"),
                        annotations={"kit/side-effect": "write"},
                        args=[Arg("id")],
                        flags=[Flag("args", "extra arguments", "string")],
                    ),
                    Command(
                        name="approve",
                        short="Approve an item",
                        run=echo("approved"),
                        annotations={
                            "kit/side-effect": "write",
                            "kit/requires-confirmation": "true",
                        },
                        args=[Arg("name")],
                    ),
                ],
            ),
            Command(
                name="ping",
                short="Ping",
                run=lambda flags: Result(stdout=f"pong {sorted(flags)!r}\n"),
                annotations={"kit/side-effect": "read"},
            ),
        ],
    )


MODERN_META = {
    META_CLIENT_CAPABILITIES: {},
    META_PROTOCOL_VERSION: MODERN_PROTOCOL_VERSION,
}


class Era:
    def __init__(self, name: str, **mount: Any) -> None:
        self.name = name
        self.surface = mount_mcp(Bridge(tree()), **mount)

    def _post(self, method: str, params: dict[str, Any], tool: str = "") -> tuple[int, Any]:
        headers: dict[str, str] = {}
        if self.name == "modern":
            params = {**params, "_meta": MODERN_META}
            headers = {"MCP-Protocol-Version": MODERN_PROTOCOL_VERSION, "Mcp-Method": method}
            if tool:
                headers["Mcp-Name"] = tool
        body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
        response = self.surface.handle(
            Request(
                method="POST",
                path="/mcp",
                headers=Headers.from_mapping(headers),
                body=body.encode("utf-8"),
            )
        )
        return response.status, json.loads(response.body)

    def tools(self) -> dict[str, dict[str, Any]]:
        _, msg = self._post("tools/list", {})
        return {tool["name"]: tool for tool in msg["result"]["tools"]}

    def call(self, tool: str, arguments: dict[str, Any]) -> tuple[int, str, bool]:
        status, msg = self._post("tools/call", {"name": tool, "arguments": arguments}, tool)
        result = msg["result"]
        text = "".join(block.get("text", "") for block in result.get("content", []))
        return status, text, bool(result.get("isError"))


@pytest.fixture(params=["legacy", "modern"])
def era(request: pytest.FixtureRequest) -> Era:
    return Era(request.param)


def test_tools_list_publishes_declared_positionals(era: Era) -> None:
    tools = era.tools()

    add = tools["item.add"]
    assert add["description"] == "Add an item"
    schema = add["inputSchema"]
    assert schema["properties"]["args"] == {
        "type": "array",
        "items": {"type": "string"},
        "description": "Positional arguments in order: name, note?",
        "minItems": 1,
    }
    assert "count" in schema["properties"], "flags stay beside the args array"
    assert schema["required"] == ["args"]

    pin = tools["item.pin"]
    pschema = pin["inputSchema"]
    assert pschema["properties"]["args"]["type"] == "string", 'the flag keeps "args"'
    assert "required" not in pschema
    assert pin["description"] == (
        "Pin an item Takes positional arguments that cannot be passed over MCP: "
        'its --args flag holds the "args" property.'
    )

    assert "args" not in tools["ping"]["inputSchema"]["properties"]


def test_optional_only_positionals_are_not_required() -> None:
    root = Command(
        name="root",
        children=[
            Command(
                name="ls",
                short="",
                run=lambda flags, args: Result(),
                args=[Arg("filter", required=False)],
            )
        ],
    )
    (leaf,) = Bridge(root).leaves()
    envelope = leaf.tool_envelope()
    assert envelope["inputSchema"] == {
        "type": "object",
        "properties": {
            "args": {
                "type": "array",
                "items": {"type": "string"},
                "description": "Positional arguments in order: filter?",
            }
        },
    }


def test_args_flag_note_stands_alone_without_summary() -> None:
    root = Command(
        name="root",
        children=[
            Command(
                name="pin",
                run=lambda flags, args: Result(),
                args=[Arg("id")],
                flags=[Flag("args")],
            )
        ],
    )
    (leaf,) = Bridge(root).leaves()
    assert leaf.tool_envelope()["description"] == (
        'Takes positional arguments that cannot be passed over MCP: its --args flag holds the "args" property.'
    )


def test_call_maps_args_onto_positionals(era: Era) -> None:
    status, text, is_error = era.call("item.add", {"args": ["bolt", "spare"], "count": 2})
    assert status == 200
    assert not is_error, text
    assert text == "added ['bolt', 'spare'] x2\n"

    _, text, is_error = era.call("item.add", {"args": ["nut"]})
    assert not is_error, text
    assert text == "added ['nut']\n", "an optional argument may be left out"


@pytest.mark.parametrize(
    ("arguments", "message"),
    [
        ({"count": 2}, "missing required argument: name"),
        ({"args": []}, "missing required argument: name"),
        ({"args": None}, "missing required argument: name"),
        ({"args": "bolt"}, '"args" must be an array of strings'),
        ({"args": [1]}, '"args" must be an array of strings'),
    ],
)
def test_call_refuses_missing_or_malformed_args(
    era: Era, arguments: dict[str, Any], message: str
) -> None:
    status, text, is_error = era.call("item.add", arguments)
    assert status == 200
    assert is_error
    assert text == message


def test_arguments_are_checked_before_confirmation(era: Era) -> None:
    status, text, is_error = era.call("item.approve", {})
    assert status == 200, "not the confirmation gate's 428"
    assert is_error
    assert text == "missing required argument: name"

    status, text, is_error = era.call("item.approve", {"args": ["bolt"]})
    assert status == 428
    assert is_error
    assert "confirmation required" in text


def test_unpublished_args_stay_flags(era: Era) -> None:
    _, text, is_error = era.call("item.pin", {"args": "x"})
    assert not is_error, text
    assert text == "pinned [] --args=x\n"


def test_leaf_without_positionals_is_unchanged(era: Era) -> None:
    _, text, is_error = era.call("ping", {"args": ["x"]})
    assert not is_error, text
    assert text == "pong ['args']\n", "args is just another flag key"


def test_custom_runner_receives_args_on_the_invocation() -> None:
    seen: list[Invocation] = []

    def runner(inv: Invocation) -> Result:
        seen.append(inv)
        return Result(stdout="ok\n")

    surface = mount_mcp(Bridge(tree(), runner=runner))
    body = json.dumps(
        {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "tools/call",
            "params": {"name": "item.add", "arguments": {"args": ["bolt"], "count": 3}},
        }
    )
    surface.handle(Request(method="POST", path="/mcp", headers=Headers(), body=body.encode()))
    (inv,) = seen
    assert inv.args == ["bolt"]
    assert inv.flags == {"count": 3}


def test_positionals_under_a_non_default_separator(era: Era) -> None:
    """The separator renames the tool; the args split is unchanged."""
    sep = Era(era.name, tool_name_separator="_")
    tools = sep.tools()
    assert "item.add" not in tools
    assert tools["item_add"]["inputSchema"]["properties"]["args"]["minItems"] == 1

    status, text, is_error = sep.call("item_add", {"args": ["bolt", "-x"], "count": 2})
    assert (status, text, is_error) == (200, "added ['bolt', '-x'] x2\n", False)

    status, text, is_error = sep.call("item_add", {})
    assert (status, is_error) == (200, True)
    assert "missing required argument: name" in text
