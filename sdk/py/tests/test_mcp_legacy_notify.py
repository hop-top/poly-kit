"""Legacy-era notifications and ping.

A message with no ``id`` member is a JSON-RPC notification, which must
never be answered: the legacy path acknowledges it with HTTP 202 and an
empty body and does not process it. ``ping`` (a 2024-11-05 utility) must
be answered with an empty result. Both hold on the dual-era mount and on
a legacy-only mount.
"""

from __future__ import annotations

import pytest

from hop_top_kit.mcp import Bridge, Command, Headers, Request, Result, mount_mcp

MOUNTS = {
    "dual-era": {},
    "legacy-only": {"spec_versions": ("2024-11-05",)},
}


def _surface(calls: list[int], options: dict):
    def run(flags):
        calls.append(1)
        return Result(stdout="pong\n")

    tree = Command(
        name="root",
        children=[
            Command(
                name="ping",
                short="Ping the server",
                run=run,
                annotations={"kit/side-effect": "read"},
            )
        ],
    )
    return mount_mcp(Bridge(tree), **options)


def _post(body: str) -> Request:
    return Request(
        method="POST", path="/mcp", headers=Headers.from_mapping(None), body=body.encode()
    )


@pytest.mark.parametrize("mount", MOUNTS)
@pytest.mark.parametrize(
    "body",
    [
        '{"jsonrpc":"2.0","method":"notifications/initialized"}',
        '{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}',
        '{"jsonrpc":"2.0","method":"tools/call","params":{"name":"ping"}}',
    ],
)
def test_notification_is_never_answered(mount: str, body: str) -> None:
    calls: list[int] = []
    response = _surface(calls, MOUNTS[mount]).handle(_post(body))
    assert response.status == 202
    assert response.body == b""
    assert calls == []


@pytest.mark.parametrize("mount", MOUNTS)
@pytest.mark.parametrize(
    ("body", "want"),
    [
        ('{"jsonrpc":"2.0","id":7,"method":"ping"}', b'{"jsonrpc":"2.0","id":7,"result":{}}\n'),
        (
            '{"jsonrpc":"2.0","id":"p","method":"ping","params":{}}',
            b'{"jsonrpc":"2.0","id":"p","result":{}}\n',
        ),
        (
            '{"jsonrpc":"2.0","id":null,"method":"ping"}',
            b'{"jsonrpc":"2.0","id":null,"result":{}}\n',
        ),
    ],
)
def test_ping_answers_an_empty_result(mount: str, body: str, want: bytes) -> None:
    response = _surface([], MOUNTS[mount]).handle(_post(body))
    assert response.status == 200
    assert response.body == want
