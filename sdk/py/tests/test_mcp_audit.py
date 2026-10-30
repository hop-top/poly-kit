"""The mount-level ``audit`` hook: one record per refusal the surface makes.

Mirrors the Go bridge's sinks for the MCP surface: every refusal decided
before adopter code runs reaches the hook, with Go's sentinel message on
``error``. Best-effort: the hook never changes the wire response, and a
hook that raises is swallowed.
"""

from __future__ import annotations

import io
import json
import os
import time
from datetime import UTC
from typing import Any

import pytest

from hop_top_kit.mcp import (
    AuditRecord,
    Bridge,
    Command,
    Headers,
    Identity,
    Request,
    Response,
    Result,
    mount_mcp,
    serve_stdio,
)
from hop_top_kit.mcp.audit import (
    ERR_AUTH_REFUSED,
    ERR_CONFIRMATION_DECLINED,
    ERR_CONFIRMATION_REQUIRED,
    ERR_CONFIRMATION_STATE_REJECTED,
    ERR_DESTRUCTIVE_BLOCKED,
)
from hop_top_kit.mcp.modern_confirm import Binding, args_digest, mint_state
from hop_top_kit.mcp.protocol import (
    LEGACY_PROTOCOL_VERSION,
    META_CLIENT_CAPABILITIES,
    META_PROTOCOL_VERSION,
    MODERN_PROTOCOL_VERSION,
    RESULT_TYPE_INPUT_REQUIRED,
)

ERAS = ("legacy", "modern")
VERSION = {"legacy": LEGACY_PROTOCOL_VERSION, "modern": MODERN_PROTOCOL_VERSION}
KEY = b"shared-across-every-instance"


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
                run=lambda flags: Result(stdout="secret\n"),
                annotations={"kit/auth-required": "true"},
            ),
            Command(
                name="deploy",
                short="Deploy",
                run=lambda flags: Result(stdout="deployed\n"),
                annotations={"kit/requires-confirmation": "true"},
            ),
            Command(
                name="purge",
                short="Purge",
                run=lambda flags: Result(stdout="purged\n"),
                annotations={"kit/side-effect": "destructive"},
            ),
        ],
    )


class Sink:
    """Collects every record the hook receives."""

    def __init__(self) -> None:
        self.records: list[AuditRecord] = []

    def __call__(self, record: AuditRecord) -> None:
        self.records.append(record)


def mount(**options: Any):
    """A surface with a recording hook, plus a twin without one."""
    sink = Sink()
    with_hook = mount_mcp(Bridge(tree()), audit=sink, **options)
    without = mount_mcp(Bridge(tree()), **options)
    return with_hook, without, sink


def call(
    era: str,
    name: str,
    *,
    headers: dict[str, str] | None = None,
    elicitation: dict | None = None,
    extra_params: dict[str, Any] | None = None,
) -> Request:
    params: dict[str, Any] = {"name": name}
    all_headers = dict(headers or {})
    if era == "modern":
        caps: dict = {} if elicitation is None else {"elicitation": elicitation}
        params["_meta"] = {
            META_PROTOCOL_VERSION: MODERN_PROTOCOL_VERSION,
            META_CLIENT_CAPABILITIES: caps,
        }
        all_headers.update(
            {
                "MCP-Protocol-Version": MODERN_PROTOCOL_VERSION,
                "Mcp-Method": "tools/call",
                "Mcp-Name": name,
            }
        )
    params.update(extra_params or {})
    body = {"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params}
    return Request(
        method="POST",
        path="/mcp",
        headers=Headers.from_mapping(all_headers),
        body=json.dumps(body).encode("utf-8"),
    )


def same_response(a: Response, b: Response) -> None:
    assert a.status == b.status
    assert a.headers == b.headers
    assert a.body == b.body


def check(record: AuditRecord, *, path: tuple[str, ...], era: str, error: str) -> None:
    assert record.path == path
    assert record.surface == "mcp"
    assert record.spec_version == VERSION[era]
    assert record.error == error
    assert record.extra["mcp_spec_version"] == VERSION[era]
    assert record.requested_at.tzinfo is not None
    assert record.requested_at.utcoffset().total_seconds() == 0
    assert abs((record.requested_at - time_now()).total_seconds()) < 5


def time_now():
    from datetime import datetime

    return datetime.now(UTC)


# --- auth refused ------------------------------------------------------------


@pytest.mark.parametrize("era", ERAS)
def test_auth_refused_is_audited(era: str) -> None:
    surface, twin, sink = mount()
    request = call(era, "secret", headers={"Authorization": "Bearer anything"})
    response = surface.handle(request)
    same_response(response, twin.handle(request))
    assert response.status == 401
    assert len(sink.records) == 1
    check(sink.records[0], path=("secret",), era=era, error=ERR_AUTH_REFUSED)
    assert sink.records[0].extra == {"mcp_spec_version": VERSION[era]}


@pytest.mark.parametrize("era", ERAS)
def test_verifier_refusal_is_audited(era: str) -> None:
    surface, twin, sink = mount(verifier=lambda _request: None)
    request = call(era, "secret", headers={"Authorization": "Bearer bad"})
    same_response(surface.handle(request), twin.handle(request))
    assert [r.error for r in sink.records] == [ERR_AUTH_REFUSED]


# --- confirmation required ---------------------------------------------------


@pytest.mark.parametrize("era", ERAS)
def test_header_gate_refusal_is_audited(era: str) -> None:
    surface, twin, sink = mount()
    request = call(era, "deploy")
    response = surface.handle(request)
    same_response(response, twin.handle(request))
    assert response.status == 428
    assert len(sink.records) == 1
    check(sink.records[0], path=("deploy",), era=era, error=ERR_CONFIRMATION_REQUIRED)
    assert sink.records[0].extra == {"mcp_spec_version": VERSION[era]}


def test_no_elicitation_fallback_refusal_is_audited() -> None:
    """A keyed mount still refuses a client without form elicitation."""
    surface, twin, sink = mount(confirmation_key=KEY)
    request = call("modern", "deploy")
    response = surface.handle(request)
    same_response(response, twin.handle(request))
    assert response.status == 428
    assert len(sink.records) == 1
    check(sink.records[0], path=("deploy",), era="modern", error=ERR_CONFIRMATION_REQUIRED)


# --- confirmation declined ---------------------------------------------------


@pytest.mark.parametrize("action", ["decline", "cancel"])
def test_declined_confirmation_is_audited(action: str) -> None:
    surface, twin, sink = mount(confirmation_key=KEY)
    first = json.loads(surface.handle(call("modern", "deploy", elicitation={})).body)
    assert sink.records == []
    retry = call(
        "modern",
        "deploy",
        elicitation={},
        extra_params={
            "requestState": first["result"]["requestState"],
            "inputResponses": {"confirm": {"action": action}},
        },
    )
    response = surface.handle(retry)
    same_response(response, twin.handle(retry))
    assert json.loads(response.body)["result"]["content"][0]["text"] == "confirmation declined"
    assert len(sink.records) == 1
    check(sink.records[0], path=("deploy",), era="modern", error=ERR_CONFIRMATION_DECLINED)
    assert sink.records[0].extra == {"mcp_spec_version": MODERN_PROTOCOL_VERSION}


# --- confirmation state rejected ---------------------------------------------


def forged_retry(surface) -> Request:
    first = json.loads(surface.handle(call("modern", "deploy", elicitation={})).body)
    version, expiry, tag = first["result"]["requestState"].split(".")
    return call(
        "modern",
        "deploy",
        elicitation={},
        extra_params={
            "requestState": f"{version}.{expiry}.{'A' * len(tag)}",
            "inputResponses": {"confirm": {"action": "accept"}},
        },
    )


def without_state(response: Response) -> dict:
    body = json.loads(response.body)
    body["result"].pop("requestState")
    return body


def test_rejected_state_is_audited_and_reprompted() -> None:
    surface, twin, sink = mount(confirmation_key=KEY)
    retry = forged_retry(surface)
    response = surface.handle(retry)
    assert response.status == 200
    assert without_state(response) == without_state(twin.handle(retry))
    assert json.loads(response.body)["result"]["resultType"] == RESULT_TYPE_INPUT_REQUIRED
    assert len(sink.records) == 1
    check(sink.records[0], path=("deploy",), era="modern", error=ERR_CONFIRMATION_STATE_REJECTED)
    assert sink.records[0].extra == {
        "mcp_spec_version": MODERN_PROTOCOL_VERSION,
        "mcp_confirm_rejection": "request_state_verification_failed",
    }


def test_rejected_state_keeps_the_gate_rejections_list() -> None:
    surface, _, sink = mount(confirmation_key=KEY)
    surface.handle(forged_retry(surface))
    assert surface.modern._confirm.rejections == [
        {"path": "deploy", "mcp_confirm_rejection": "request_state_verification_failed"}
    ]
    assert len(sink.records) == 1


def test_expired_authentic_state_is_not_audited() -> None:
    """Go re-prompts an expired state silently; so does this port."""
    surface, _, sink = mount(confirmation_key=KEY)
    stale = mint_state(KEY, Binding("deploy", args_digest(None), ""), int(time.time()) - 1)
    response = surface.handle(
        call(
            "modern",
            "deploy",
            elicitation={},
            extra_params={
                "requestState": stale,
                "inputResponses": {"confirm": {"action": "accept"}},
            },
        )
    )
    assert json.loads(response.body)["result"]["resultType"] == RESULT_TYPE_INPUT_REQUIRED
    assert sink.records == []


# --- destructive ceiling -----------------------------------------------------


@pytest.mark.parametrize("era", ERAS)
def test_destructive_ceiling_refusal_is_audited(era: str) -> None:
    surface, twin, sink = mount()
    request = call(era, "purge")
    response = surface.handle(request)
    same_response(response, twin.handle(request))
    body = json.loads(response.body)
    assert body["result"]["isError"] is True
    assert "destructive command blocked" in body["result"]["content"][0]["text"]
    assert len(sink.records) == 1
    check(sink.records[0], path=("purge",), era=era, error=ERR_DESTRUCTIVE_BLOCKED)
    assert sink.records[0].extra == {"mcp_spec_version": VERSION[era]}


# --- best effort -------------------------------------------------------------


@pytest.mark.parametrize("era", ERAS)
def test_a_raising_hook_never_changes_the_response(era: str) -> None:
    calls: list[AuditRecord] = []

    def boom(record: AuditRecord) -> None:
        calls.append(record)
        raise RuntimeError("sink down")

    surface = mount_mcp(Bridge(tree()), audit=boom)
    twin = mount_mcp(Bridge(tree()))
    for name in ("secret", "deploy", "purge"):
        request = call(era, name)
        same_response(surface.handle(request), twin.handle(request))
    assert len(calls) == 3


@pytest.mark.parametrize("era", ERAS)
def test_a_successful_call_is_not_audited(era: str) -> None:
    surface, _, sink = mount(verifier=lambda _request: Identity(caller="alice"))
    for name, headers in (("ping", None), ("secret", None), ("deploy", {"X-Confirm-Token": "t"})):
        response = surface.handle(call(era, name, headers=headers))
        assert json.loads(response.body)["result"]["isError"] is False
    assert sink.records == []


def test_the_record_is_frozen() -> None:
    surface, _, sink = mount()
    surface.handle(call("legacy", "secret"))
    with pytest.raises(AttributeError):
        sink.records[0].error = "other"  # type: ignore[misc]


# --- stdio -------------------------------------------------------------------


def test_state_rejected_over_stdio() -> None:
    sink = Sink()
    r, w = os.pipe()
    stdin = os.fdopen(w, "wb", buffering=0)
    reader = os.fdopen(r, "rb")
    output = io.StringIO()
    server = serve_stdio(
        Bridge(tree()),
        input=reader,
        output=output,
        log=io.StringIO(),
        mount={"confirmation_key": KEY, "audit": sink},
    )
    stale_binding = Binding("deploy", args_digest(None), "")
    minted = mint_state(KEY, stale_binding, int(time.time()) + 60)
    version, expiry, tag = minted.split(".")
    params = {
        "name": "deploy",
        "_meta": {
            META_PROTOCOL_VERSION: MODERN_PROTOCOL_VERSION,
            META_CLIENT_CAPABILITIES: {"elicitation": {}},
        },
        "requestState": f"{version}.{expiry}.{'A' * len(tag)}",
        "inputResponses": {"confirm": {"action": "accept"}},
    }
    message = {"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params}
    stdin.write(json.dumps(message).encode() + b"\n")
    stdin.close()
    assert server.wait(5)

    lines = [line for line in output.getvalue().split("\n") if line]
    assert len(lines) == 1
    assert json.loads(lines[0])["result"]["resultType"] == RESULT_TYPE_INPUT_REQUIRED
    assert len(sink.records) == 1
    check(sink.records[0], path=("deploy",), era="modern", error=ERR_CONFIRMATION_STATE_REJECTED)
    assert sink.records[0].extra["mcp_confirm_rejection"] == "request_state_verification_failed"
