"""The stdio binding: newline framing, notifications never answered,
ping, parse / batch refusals, concurrent requests, both eras with no
header layer, fail-closed gates, and a clean end of session.
"""

from __future__ import annotations

import io
import json
import os
import subprocess
import sys
import textwrap
import threading
import time
from pathlib import Path

import pytest

from hop_top_kit.mcp import (
    Bridge,
    Command,
    Identity,
    McpSurface,
    Request,
    Response,
    Result,
    StdioServer,
    mount_mcp,
    serve_stdio,
)

MODERN_META = (
    '"_meta":{"io.modelcontextprotocol/clientCapabilities":{},'
    '"io.modelcontextprotocol/protocolVersion":"2026-07-28"}'
)


def ping_bridge() -> Bridge:
    """One read-only ``ping`` leaf answering ``pong``."""
    return Bridge(
        Command(
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
    )


def ordered_bridge() -> Bridge:
    """A bridge whose ``slow`` leaf answers only after ``fast`` has."""
    fast_done = threading.Event()

    def slow(flags):
        fast_done.wait(5)
        return Result(stdout="slow\n")

    def fast(flags):
        threading.Timer(0.05, fast_done.set).start()
        return Result(stdout="fast\n")

    return Bridge(
        Command(
            name="root",
            children=[
                Command(
                    name="slow", short="Slow", run=slow, annotations={"kit/side-effect": "read"}
                ),
                Command(
                    name="fast", short="Fast", run=fast, annotations={"kit/side-effect": "read"}
                ),
            ],
        )
    )


class Session:
    """A server on a pipe, with captured output and log streams."""

    def __init__(self, target, mount=None) -> None:
        r, w = os.pipe()
        self.input = os.fdopen(w, "wb", buffering=0)
        self._reader = os.fdopen(r, "rb")
        self.output = io.StringIO()
        self.log = io.StringIO()
        self.server: StdioServer = serve_stdio(
            target, input=self._reader, output=self.output, log=self.log, mount=mount
        )

    def send(self, *messages: str) -> None:
        for m in messages:
            self.input.write(m.encode() + b"\n")

    def end(self) -> None:
        self.input.close()

    def lines(self) -> list[str]:
        return [line for line in self.output.getvalue().split("\n") if line]


def run(s: Session, *messages: str) -> list[str]:
    """Writes each message as one line, ends input, waits for the session."""
    s.send(*messages)
    s.end()
    assert s.server.wait(5)
    return s.lines()


def by_id(lines: list[str]) -> dict:
    return {json.loads(line)["id"]: json.loads(line) for line in lines}


def test_legacy_handshake_and_notification_silence() -> None:
    lines = run(
        Session(ping_bridge()),
        '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05",'
        '"capabilities":{},"clientInfo":{"name":"pipe","version":"0"}}}',
        '{"jsonrpc":"2.0","method":"notifications/initialized"}',
        '{"jsonrpc":"2.0","id":2,"method":"tools/list"}',
        '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"ping"}}',
    )
    assert len(lines) == 3
    got = by_id(lines)
    assert sorted(got) == [1, 2, 3]
    assert got[1]["result"]["protocolVersion"] == "2024-11-05"
    assert "pong" in json.dumps(got[3]["result"])


def test_ping_answers_empty_result() -> None:
    lines = run(Session(ping_bridge()), '{"jsonrpc":"2.0","id":"p1","method":"ping"}')
    assert lines == ['{"jsonrpc":"2.0","id":"p1","result":{}}']


def test_drops_every_notification_even_malformed_or_failing() -> None:
    lines = run(
        Session(ping_bridge()),
        '{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}',
        '{"jsonrpc":"1.0","method":"notifications/initialized"}',
        '{"jsonrpc":"2.0","method":"tools/call","params":{"name":"ping"}}',
        '{"jsonrpc":"2.0","method":"nope"}',
        f'{{"jsonrpc":"2.0","method":"tools/list","params":{{{MODERN_META}}}}}',
    )
    assert lines == []


def test_unparseable_input_is_32700_id_null() -> None:
    lines = run(Session(ping_bridge()), "{not json")
    assert lines == [
        '{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"parse error: '
        "invalid character 'n' looking for beginning of object key string\"}}"
    ]


def test_batch_and_non_object_are_32600_id_null() -> None:
    lines = run(Session(ping_bridge()), '[{"jsonrpc":"2.0","id":1,"method":"ping"}]', "42")
    assert lines == [
        '{"jsonrpc":"2.0","id":null,"error":{"code":-32600,'
        '"message":"invalid request: batch requests are not supported"}}',
        '{"jsonrpc":"2.0","id":null,"error":{"code":-32600,'
        '"message":"invalid request: not a JSON-RPC object"}}',
    ]


def test_request_without_method_is_32600_with_its_id() -> None:
    lines = run(Session(ping_bridge()), '{"jsonrpc":"2.0","id":9}')
    assert lines == [
        '{"jsonrpc":"2.0","id":9,"error":{"code":-32600,"message":"invalid request: missing method"}}'
    ]


def test_client_response_is_ignored_and_logged() -> None:
    s = Session(ping_bridge())
    lines = run(s, '{"jsonrpc":"2.0","id":1,"result":{}}', '{"jsonrpc":"2.0","id":2,"error":{}}')
    assert lines == []
    assert "ignored a JSON-RPC response" in s.log.getvalue()


def test_blank_lines_skipped_and_crlf_accepted() -> None:
    s = Session(ping_bridge())
    s.input.write(b'\n   \n{"jsonrpc":"2.0","id":1,"method":"ping"}\r\n')
    s.end()
    assert s.server.wait(5)
    assert s.lines() == ['{"jsonrpc":"2.0","id":1,"result":{}}']


def test_concurrent_requests_reply_out_of_order() -> None:
    lines = run(
        Session(ordered_bridge()),
        '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"slow"}}',
        '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"fast"}}',
    )
    assert [json.loads(line)["id"] for line in lines] == [2, 1]
    assert "fast" in lines[0]
    assert "slow" in lines[1]


def test_modern_era_from_body_alone() -> None:
    lines = run(
        Session(ping_bridge()),
        f'{{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{{{MODERN_META}}}}}',
        f'{{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{{"name":"ping",{MODERN_META}}}}}',
        f'{{"jsonrpc":"2.0","id":3,"method":"ping","params":{{{MODERN_META}}}}}',
    )
    got = by_id(lines)
    assert got[1]["result"]["resultType"] == "complete"
    assert got[1]["result"]["supportedVersions"] == ["2026-07-28"]
    assert got[2]["result"]["resultType"] == "complete"
    assert got[2]["result"]["isError"] is False
    # 2026-07-28 removed ping: a modern ping is an unknown method.
    assert got[3]["error"]["code"] == -32601


def test_http_still_enforces_header_checks() -> None:
    """The stdio flag is per call: the same surface over HTTP keeps V6."""
    surface = mount_mcp(ping_bridge())
    body = f'{{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{{{MODERN_META}}}}}'
    res = surface.handle(Request(body=body.encode()))
    assert json.loads(res.body)["error"]["code"] == -32020
    lines = run(Session(surface), body)
    assert json.loads(lines[0])["result"]["resultType"] == "complete"


def test_keeps_remaining_modern_validation() -> None:
    lines = run(
        Session(ping_bridge()),
        '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{'
        '"io.modelcontextprotocol/clientCapabilities":{},'
        '"io.modelcontextprotocol/protocolVersion":"2099-01-01"}}}',
    )
    got = json.loads(lines[0])
    assert got["id"] == 1
    assert got["error"]["code"] == -32022


def test_reuses_a_surface_and_its_options() -> None:
    surface = mount_mcp(ping_bridge(), server_name="stdio-tool", server_version="9.9.9")
    got = by_id(
        run(
            Session(surface),
            '{"jsonrpc":"2.0","id":1,"method":"initialize"}',
            f'{{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{{{MODERN_META}}}}}',
        )
    )
    assert got[1]["result"]["serverInfo"] == {"name": "stdio-tool", "version": "9.9.9"}
    assert got[2]["result"]["resultType"] == "complete"


def test_applies_mount_options_for_a_bridge() -> None:
    lines = run(
        Session(ping_bridge(), mount={"spec_versions": ("2026-07-28",)}),
        '{"jsonrpc":"2.0","id":1,"method":"initialize"}',
    )
    assert "supported protocol versions: 2026-07-28" in lines[0]


def test_refuses_mount_options_with_a_surface() -> None:
    with pytest.raises(TypeError):
        serve_stdio(mount_mcp(ping_bridge()), input=io.BytesIO(), output=io.StringIO(), mount={})


def test_drives_a_foreign_handler_as_post() -> None:
    inner = mount_mcp(ping_bridge())
    seen: list[Request] = []

    def foreign(req: Request) -> Response:
        seen.append(req)
        return inner.handle(req)

    lines = run(
        Session(foreign),
        '{"jsonrpc":"2.0","id":1,"method":"ping"}',
        '{"jsonrpc":"2.0","method":"notifications/initialized"}',
    )
    assert lines == ['{"jsonrpc":"2.0","id":1,"result":{}}']
    assert all(r.method == "POST" for r in seen)


def test_handler_raising_is_32603_and_logged() -> None:
    def boom(req: Request) -> Response:
        raise RuntimeError("boom")

    s = Session(boom)
    lines = run(s, '{"jsonrpc":"2.0","id":4,"method":"ping"}')
    assert lines == ['{"jsonrpc":"2.0","id":4,"error":{"code":-32603,"message":"internal error"}}']
    assert "boom" in s.log.getvalue()


def test_auth_required_is_fail_closed_with_headerless_verifier_call() -> None:
    seen: list[Request] = []

    def verifier(req: Request) -> Identity | None:
        seen.append(req)
        return None

    bridge = Bridge(
        Command(
            name="root",
            children=[
                Command(
                    name="secret",
                    short="Secret",
                    run=lambda flags: Result(stdout="nope\n"),
                    annotations={"kit/side-effect": "read", "kit/auth-required": "true"},
                )
            ],
        )
    )
    got = by_id(
        run(
            Session(bridge, mount={"verifier": verifier}),
            '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"secret"}}',
            f'{{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{{"name":"secret",{MODERN_META}}}}}',
        )
    )
    assert got[1]["result"]["isError"] is True
    assert got[2]["result"]["isError"] is True
    assert "nope" not in json.dumps(got)
    assert len(seen) == 2
    assert all(not r.headers.values("authorization") for r in seen)


def test_confirmation_header_gate_cannot_be_satisfied() -> None:
    calls: list[int] = []
    bridge = Bridge(
        Command(
            name="root",
            children=[
                Command(
                    name="wipe",
                    short="Wipe",
                    run=lambda flags: calls.append(1) or Result(stdout="wiped\n"),
                    annotations={"kit/side-effect": "read", "kit/requires-confirmation": "true"},
                )
            ],
        )
    )
    got = by_id(
        run(
            Session(bridge),
            f'{{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{{"name":"wipe",{MODERN_META}}}}}',
        )
    )
    assert got[1]["result"]["isError"] is True
    assert "confirmation required" in json.dumps(got[1])
    assert calls == []


def test_confirms_in_band_over_mrtr_and_refuses_without_elicitation() -> None:
    calls: list[int] = []
    bridge = Bridge(
        Command(
            name="root",
            children=[
                Command(
                    name="purge",
                    short="Purge",
                    run=lambda flags: calls.append(1) or Result(stdout="purged\n"),
                    annotations={"kit/side-effect": "read", "kit/requires-confirmation": "true"},
                )
            ],
        )
    )
    s = Session(bridge, mount={"confirmation_key": b"stdio-mrtr-suite-shared-secret-32"})

    def call(id_: int, caps: dict, **extra) -> str:
        meta = {
            "io.modelcontextprotocol/clientCapabilities": caps,
            "io.modelcontextprotocol/protocolVersion": "2026-07-28",
        }
        params = {"name": "purge", "_meta": meta, **extra}
        return json.dumps({"jsonrpc": "2.0", "id": id_, "method": "tools/call", "params": params})

    def reply(n: int) -> dict:
        deadline = time.monotonic() + 5
        while len(s.lines()) < n and time.monotonic() < deadline:
            time.sleep(0.005)
        return json.loads(s.lines()[n - 1])

    s.send(call(1, {"elicitation": {"form": {}}}))
    r1 = reply(1)["result"]
    assert r1["resultType"] == "input_required"
    assert list(r1["inputRequests"]) == ["confirm"]
    assert calls == []

    s.send(
        call(
            2,
            {"elicitation": {"form": {}}},
            requestState=r1["requestState"],
            inputResponses={"confirm": {"action": "accept"}},
        )
    )
    r2 = reply(2)["result"]
    assert r2["resultType"] == "complete"
    assert r2["isError"] is False
    assert calls == [1]

    # No elicitation declared: the header gate applies, and stdio has no headers.
    lines = run(s, call(3, {}))
    r3 = json.loads(lines[2])["result"]
    assert r3["resultType"] == "complete"
    assert r3["isError"] is True
    assert "confirmation required" in json.dumps(r3)
    assert calls == [1]


def test_waits_for_in_flight_before_done() -> None:
    gate = threading.Event()

    def wait(flags):
        gate.wait(5)
        return Result(stdout="done\n")

    bridge = Bridge(
        Command(
            name="root",
            children=[
                Command(
                    name="wait", short="Wait", run=wait, annotations={"kit/side-effect": "read"}
                )
            ],
        )
    )
    s = Session(bridge)
    s.send('{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"wait"}}')
    s.end()
    assert not s.server.wait(0.1)
    assert not s.server.done
    gate.set()
    assert s.server.wait(5)
    assert s.server.done
    assert len(s.lines()) == 1


def test_close_stops_reading_and_returns_once_idle() -> None:
    s = Session(ping_bridge())
    s.send('{"jsonrpc":"2.0","id":1,"method":"ping"}')
    deadline = time.monotonic() + 5
    while not s.lines() and time.monotonic() < deadline:
        time.sleep(0.005)
    assert s.server.close(5)
    s.send('{"jsonrpc":"2.0","id":2,"method":"ping"}')
    time.sleep(0.05)
    assert s.lines() == ['{"jsonrpc":"2.0","id":1,"result":{}}']
    s.end()


def test_end_to_end_over_a_real_process(tmp_path: Path) -> None:
    script = tmp_path / "serve.py"
    script.write_text(
        textwrap.dedent(
            """
            from hop_top_kit.mcp import Bridge, Command, Result, serve_stdio

            root = Command(name="root", children=[Command(
                name="ping", short="Ping", run=lambda flags: Result(stdout="pong\\n"),
                annotations={"kit/side-effect": "read"})])
            serve_stdio(Bridge(root)).wait()
            """
        )
    )
    stdin = "\n".join(
        [
            '{"jsonrpc":"2.0","id":1,"method":"ping"}',
            '{"jsonrpc":"2.0","method":"notifications/initialized"}',
            '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ping"}}',
            "{oops",
            "",
        ]
    )
    proc = subprocess.run(
        [sys.executable, str(script)],
        input=stdin.encode(),
        capture_output=True,
        timeout=30,
        check=True,
    )
    lines = proc.stdout.decode().splitlines()
    assert len(lines) == 3
    got = {json.loads(line)["id"]: json.loads(line) for line in lines}
    assert got[1] == {"jsonrpc": "2.0", "id": 1, "result": {}}
    assert "pong" in json.dumps(got[2])
    assert got[None]["error"]["code"] == -32700


def test_surface_dispatch_rejects_unknown_transport() -> None:
    surface: McpSurface = mount_mcp(ping_bridge())
    from hop_top_kit.mcp.protocol import parse_rpc

    with pytest.raises(ValueError):
        surface.dispatch(
            Request(), parse_rpc(b'{"id":1,"method":"ping"}'), transport="carrier-pigeon"
        )
