"""MRTR confirmation, cacheable lists, and the tasks extension.

Covers the three additive pieces of the modern era that the shared wire
fixtures do not reach: the elicitation confirmation round trip, the cache
hints on list results, and ``io.modelcontextprotocol/tasks``.
"""

from __future__ import annotations

import json
import time

import pytest

from hop_top_kit.mcp import (
    Bridge,
    Command,
    Headers,
    InMemoryTaskStore,
    MountError,
    Request,
    Result,
    TasksExtension,
    mount_mcp,
)
from hop_top_kit.mcp.modern_confirm import (
    Binding,
    StateStatus,
    args_digest,
    mint_state,
    verify_state,
)
from hop_top_kit.mcp.protocol import (
    ERR_INVALID_PARAMS,
    ERR_METHOD_NOT_FOUND,
    META_CLIENT_CAPABILITIES,
    META_PROTOCOL_VERSION,
    MODERN_PROTOCOL_VERSION,
    RESULT_TYPE_COMPLETE,
    RESULT_TYPE_INPUT_REQUIRED,
)

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
                name="deploy",
                short="Deploy",
                run=lambda flags: Result(stdout="deployed\n"),
                annotations={"kit/requires-confirmation": "true"},
            ),
        ],
    )


def meta(*, elicitation: dict | None = None) -> dict:
    caps: dict = {} if elicitation is None else {"elicitation": elicitation}
    return {
        META_PROTOCOL_VERSION: MODERN_PROTOCOL_VERSION,
        META_CLIENT_CAPABILITIES: caps,
    }


def call(surface, params: dict, *, headers: dict[str, str] | None = None):
    base = {
        "MCP-Protocol-Version": MODERN_PROTOCOL_VERSION,
        "Mcp-Method": "tools/call",
        "Mcp-Name": params["name"],
    }
    base.update(headers or {})
    body = {"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params}
    response = surface.handle(
        Request(
            method="POST",
            path="/mcp",
            headers=Headers.from_mapping(base),
            body=json.dumps(body).encode("utf-8"),
        )
    )
    return response, json.loads(response.body)


def rpc(surface, method: str, params: dict | None = None, *, id_: int = 1):
    body = {"jsonrpc": "2.0", "id": id_, "method": method}
    if params is not None:
        body["params"] = params
    response = surface.handle(
        Request(
            method="POST",
            path="/mcp",
            headers=Headers.from_mapping(
                {
                    "MCP-Protocol-Version": MODERN_PROTOCOL_VERSION,
                    "Mcp-Method": method,
                }
            ),
            body=json.dumps(body).encode("utf-8"),
        )
    )
    return response, json.loads(response.body)


# --- confirmation: the header gate is the default -------------------------


def test_without_a_key_the_header_gate_applies() -> None:
    """No key material means every client keeps the ``X-Confirm-Token`` gate."""
    surface = mount_mcp(Bridge(tree()))
    response, body = call(surface, {"name": "deploy", "_meta": meta(elicitation={})})
    assert response.status == 428
    assert body["result"]["isError"] is True
    assert body["result"]["content"][0]["text"] == "confirmation required"


def test_header_token_satisfies_the_default_gate() -> None:
    surface = mount_mcp(Bridge(tree()))
    response, body = call(
        surface,
        {"name": "deploy", "_meta": meta()},
        headers={"X-Confirm-Token": "t"},
    )
    assert response.status == 200
    assert body["result"]["isError"] is False


def test_empty_confirmation_key_is_refused_at_mount() -> None:
    """Misconfiguration fails fast rather than degrading silently."""
    with pytest.raises(MountError):
        mount_mcp(Bridge(tree()), confirmation_key=b"")


# --- confirmation: the MRTR flow ------------------------------------------


def test_client_without_elicitation_keeps_the_header_gate() -> None:
    """The spec forbids sending ``inputRequests`` for an undeclared capability.

    The capability is optional precisely because this fallback exists, so
    an undeclared client is never answered with ``-32021``.
    """
    surface = mount_mcp(Bridge(tree()), confirmation_key=KEY)
    response, body = call(surface, {"name": "deploy", "_meta": meta()})
    assert response.status == 428
    assert body["result"]["content"][0]["text"] == "confirmation required"


def test_url_only_elicitation_client_keeps_the_header_gate() -> None:
    """A url-only client cannot receive this flow's *form* request."""
    surface = mount_mcp(Bridge(tree()), confirmation_key=KEY)
    response, _ = call(
        surface, {"name": "deploy", "_meta": meta(elicitation={"url": {}})}
    )
    assert response.status == 428


def test_first_call_returns_input_required() -> None:
    """The prompt carries both ``inputRequests`` and ``requestState``."""
    surface = mount_mcp(Bridge(tree()), confirmation_key=KEY)
    response, body = call(
        surface, {"name": "deploy", "_meta": meta(elicitation={})}
    )
    assert response.status == 200
    result = body["result"]
    assert result["resultType"] == RESULT_TYPE_INPUT_REQUIRED
    assert result["requestState"]
    confirm = result["inputRequests"]["confirm"]
    assert confirm["method"] == "elicitation/create"
    assert confirm["params"]["mode"] == "form"


def test_interim_results_carry_no_cache_hints() -> None:
    """An ``input_required`` result is never cacheable, ever."""
    surface = mount_mcp(Bridge(tree()), confirmation_key=KEY)
    _, body = call(surface, {"name": "deploy", "_meta": meta(elicitation={})})
    assert "ttlMs" not in body["result"]
    assert "cacheScope" not in body["result"]


def test_accepting_the_prompt_lets_the_call_proceed() -> None:
    surface = mount_mcp(Bridge(tree()), confirmation_key=KEY)
    _, first = call(surface, {"name": "deploy", "_meta": meta(elicitation={})})
    state = first["result"]["requestState"]

    response, body = call(
        surface,
        {
            "name": "deploy",
            "_meta": meta(elicitation={}),
            "requestState": state,
            "inputResponses": {"confirm": {"action": "accept"}},
        },
    )
    assert response.status == 200
    assert body["result"]["resultType"] == RESULT_TYPE_COMPLETE
    assert body["result"]["isError"] is False
    assert body["result"]["content"][0]["text"] == "deployed\n"


@pytest.mark.parametrize("action", ["decline", "cancel"])
def test_declining_refuses_the_call(action: str) -> None:
    surface = mount_mcp(Bridge(tree()), confirmation_key=KEY)
    _, first = call(surface, {"name": "deploy", "_meta": meta(elicitation={})})
    _, body = call(
        surface,
        {
            "name": "deploy",
            "_meta": meta(elicitation={}),
            "requestState": first["result"]["requestState"],
            "inputResponses": {"confirm": {"action": action}},
        },
    )
    assert body["result"]["isError"] is True
    assert body["result"]["content"][0]["text"] == "confirmation declined"


def test_unusable_answer_reprompts_rather_than_erroring() -> None:
    """The requested information was not provided: re-request it."""
    surface = mount_mcp(Bridge(tree()), confirmation_key=KEY)
    _, first = call(surface, {"name": "deploy", "_meta": meta(elicitation={})})
    _, body = call(
        surface,
        {
            "name": "deploy",
            "_meta": meta(elicitation={}),
            "requestState": first["result"]["requestState"],
            "inputResponses": {"confirm": {"action": "shrug"}},
        },
    )
    assert body["result"]["resultType"] == RESULT_TYPE_INPUT_REQUIRED


def test_tampered_state_is_never_honoured() -> None:
    """A state that fails the MAC is rejected and re-prompted, never accepted."""
    surface = mount_mcp(Bridge(tree()), confirmation_key=KEY)
    _, first = call(surface, {"name": "deploy", "_meta": meta(elicitation={})})
    state = first["result"]["requestState"]
    version, expiry, tag = state.split(".")
    forged = f"{version}.{expiry}.{'A' * len(tag)}"

    _, body = call(
        surface,
        {
            "name": "deploy",
            "_meta": meta(elicitation={}),
            "requestState": forged,
            "inputResponses": {"confirm": {"action": "accept"}},
        },
    )
    assert body["result"]["resultType"] == RESULT_TYPE_INPUT_REQUIRED
    assert "deployed" not in json.dumps(body)


def test_state_minted_for_another_tool_does_not_verify() -> None:
    """The binding covers the leaf, so state cannot be moved between tools."""
    minted = mint_state(KEY, Binding("deploy", args_digest(None), ""), int(time.time()) + 60)
    other = Binding("widget purge", args_digest(None), "")
    assert verify_state(KEY, minted, other, time.time()) is StateStatus.INVALID


def test_state_minted_for_other_arguments_does_not_verify() -> None:
    """The binding covers the arguments digest."""
    first = Binding("deploy", args_digest({"arguments": {"env": "dev"}}), "")
    second = Binding("deploy", args_digest({"arguments": {"env": "prod"}}), "")
    minted = mint_state(KEY, first, int(time.time()) + 60)
    assert verify_state(KEY, minted, second, time.time()) is StateStatus.INVALID


def test_argument_key_order_does_not_change_the_digest() -> None:
    """Canonical serialisation sorts keys, so client order is irrelevant."""
    assert args_digest({"arguments": {"a": 1, "b": 2}}) == args_digest(
        {"arguments": {"b": 2, "a": 1}}
    )


def test_authentic_expired_state_is_a_routine_reprompt() -> None:
    """An expired but authentic state is ``EXPIRED``, not a rejection."""
    binding = Binding("deploy", args_digest(None), "")
    stale = mint_state(KEY, binding, int(time.time()) - 1)
    assert verify_state(KEY, stale, binding, time.time()) is StateStatus.EXPIRED


def test_a_forged_expired_state_is_invalid_not_expired() -> None:
    """Authenticity is decided *before* expiry, and the order is load-bearing.

    A state that is both unverifiable and past its expiry must report
    ``INVALID``. Reporting ``EXPIRED`` would route a tampered state down
    the routine re-prompt path, skipping the audit event that makes
    tampering visible — and the two paths are otherwise identical, so
    nothing else would ever notice the difference.
    """
    binding = Binding("deploy", args_digest(None), "")
    stale = mint_state(KEY, binding, int(time.time()) - 1)
    version, expiry, tag = stale.split(".")
    forged = f"{version}.{expiry}.{'A' * len(tag)}"
    assert verify_state(KEY, forged, binding, time.time()) is StateStatus.INVALID


def test_a_state_retimed_into_the_future_fails_the_mac() -> None:
    """The expiry is inside the MAC, so it cannot be extended."""
    binding = Binding("deploy", args_digest(None), "")
    stale = mint_state(KEY, binding, int(time.time()) - 1)
    version, expiry, tag = stale.split(".")
    retimed = f"{version}.{int(expiry) + 10_000}.{tag}"
    assert verify_state(KEY, retimed, binding, time.time()) is StateStatus.INVALID


def test_a_different_key_never_verifies() -> None:
    """Instances must share the key; a mismatched one rejects everything."""
    binding = Binding("deploy", args_digest(None), "")
    minted = mint_state(KEY, binding, int(time.time()) + 60)
    assert verify_state(b"other-key", minted, binding, time.time()) is StateStatus.INVALID


@pytest.mark.parametrize(
    "state", ["", "v1", "v1.notanumber.tag", "v2.123.tag", "v1.123.!!!not-base64!!!"]
)
def test_structurally_broken_state_is_invalid(state: str) -> None:
    """A state that cannot be verified is never honoured."""
    binding = Binding("deploy", args_digest(None), "")
    assert verify_state(KEY, state, binding, time.time()) is StateStatus.INVALID


def test_mrtr_never_relaxes_the_destructive_ceiling() -> None:
    """Confirmation and the destructive lockdown are independent gates."""
    root = Command(
        name="root",
        children=[
            Command(
                name="purge",
                short="Purge",
                run=lambda flags: Result(stdout="purged\n"),
                annotations={
                    "kit/side-effect": "destructive",
                    "kit/requires-confirmation": "true",
                },
            )
        ],
    )
    surface = mount_mcp(Bridge(root), confirmation_key=KEY)
    _, first = call(surface, {"name": "purge", "_meta": meta(elicitation={})})
    response, body = call(
        surface,
        {
            "name": "purge",
            "_meta": meta(elicitation={}),
            "requestState": first["result"]["requestState"],
            "inputResponses": {"confirm": {"action": "accept"}},
        },
    )
    assert response.status == 200
    assert body["result"]["isError"] is True
    assert "destructive command blocked" in body["result"]["content"][0]["text"]


# --- cacheable list results ------------------------------------------------


def test_cache_hints_default_to_zero_and_private() -> None:
    """``ttlMs: 0`` is honest: the leaf set can change with no notification."""
    surface = mount_mcp(Bridge(tree()))
    for method in ("server/discover", "tools/list"):
        _, body = rpc(surface, method, {"_meta": meta()})
        assert body["result"]["ttlMs"] == 0
        assert body["result"]["cacheScope"] == "private"


def test_cache_hints_are_configurable() -> None:
    surface = mount_mcp(Bridge(tree()), cache_ttl_ms=30_000, cache_scope="public")
    _, body = rpc(surface, "tools/list", {"_meta": meta()})
    assert body["result"]["ttlMs"] == 30_000
    assert body["result"]["cacheScope"] == "public"


def test_tools_call_carries_no_cache_hints() -> None:
    """Invocation is not a cacheable operation."""
    surface = mount_mcp(Bridge(tree()))
    _, body = call(surface, {"name": "ping", "_meta": meta()})
    assert "ttlMs" not in body["result"]
    assert "cacheScope" not in body["result"]


@pytest.mark.parametrize(
    "kwargs", [{"cache_ttl_ms": -1}, {"cache_scope": "semi-public"}]
)
def test_bad_cache_hints_are_refused_at_mount(kwargs: dict) -> None:
    with pytest.raises(MountError):
        mount_mcp(Bridge(tree()), **kwargs)


@pytest.mark.parametrize(
    "versions", [(), ("2019-01-01",), ("2024-11-05", "nope")]
)
def test_bad_spec_versions_are_refused_at_mount(versions: tuple) -> None:
    """An empty set is a refusal, not a synonym for the default."""
    with pytest.raises(MountError):
        mount_mcp(Bridge(tree()), spec_versions=versions)


# --- tasks extension -------------------------------------------------------


def test_tasks_are_unsupported_until_the_extension_is_mounted() -> None:
    """No extension means no ``extensions`` map and a 404 on ``tasks/*``."""
    surface = mount_mcp(Bridge(tree()))
    _, discover = rpc(surface, "server/discover", {"_meta": meta()})
    assert "extensions" not in discover["result"]["capabilities"]

    response, body = rpc(surface, "tasks/get", {"_meta": meta(), "taskId": "t1"})
    assert response.status == 404
    assert body["error"]["code"] == ERR_METHOD_NOT_FOUND


def test_mounting_the_extension_advertises_it() -> None:
    surface = mount_mcp(Bridge(tree()), extensions=(TasksExtension(),))
    _, body = rpc(surface, "server/discover", {"_meta": meta()})
    extensions = body["result"]["capabilities"]["extensions"]
    assert "io.modelcontextprotocol/tasks" in extensions
    assert "tasks/get" in extensions["io.modelcontextprotocol/tasks"]["requests"]


def test_tasks_get_returns_the_stored_record() -> None:
    store = InMemoryTaskStore()
    store.create("task-1", status="working", ttl=60_000)
    surface = mount_mcp(Bridge(tree()), extensions=(TasksExtension(store),))

    response, body = rpc(surface, "tasks/get", {"_meta": meta(), "taskId": "task-1"})
    assert response.status == 200
    assert body["result"]["taskId"] == "task-1"
    assert body["result"]["status"] == "working"
    # The modern envelope is stamped on extension results too.
    assert body["result"]["resultType"] == RESULT_TYPE_COMPLETE
    assert "_meta" in body["result"]


def test_task_records_use_the_spec_wire_spelling() -> None:
    """camelCase aliases come from ``mcp-types``, not from re-declaration."""
    store = InMemoryTaskStore()
    store.create("task-1")
    surface = mount_mcp(Bridge(tree()), extensions=(TasksExtension(store),))
    _, body = rpc(surface, "tasks/get", {"_meta": meta(), "taskId": "task-1"})
    assert {"taskId", "createdAt", "lastUpdatedAt"} <= set(body["result"])
    assert "task_id" not in body["result"]


def test_unknown_task_is_an_application_error_not_a_transport_one() -> None:
    surface = mount_mcp(Bridge(tree()), extensions=(TasksExtension(),))
    response, body = rpc(surface, "tasks/get", {"_meta": meta(), "taskId": "nope"})
    assert response.status == 200
    assert body["error"]["code"] == ERR_INVALID_PARAMS


def test_missing_task_id_is_rejected() -> None:
    surface = mount_mcp(Bridge(tree()), extensions=(TasksExtension(),))
    _, body = rpc(surface, "tasks/get", {"_meta": meta()})
    assert body["error"]["code"] == ERR_INVALID_PARAMS


def test_tasks_cancel_moves_a_task_to_cancelled() -> None:
    store = InMemoryTaskStore()
    store.create("task-1")
    surface = mount_mcp(Bridge(tree()), extensions=(TasksExtension(store),))
    _, body = rpc(surface, "tasks/cancel", {"_meta": meta(), "taskId": "task-1"})
    assert body["result"]["status"] == "cancelled"


def test_cancelling_a_terminal_task_is_a_no_op() -> None:
    """The caller's intent is already satisfied; that is not an error."""
    store = InMemoryTaskStore()
    store.create("task-1", status="completed")
    surface = mount_mcp(Bridge(tree()), extensions=(TasksExtension(store),))
    _, body = rpc(surface, "tasks/cancel", {"_meta": meta(), "taskId": "task-1"})
    assert body["result"]["status"] == "completed"


def test_tasks_list_enumerates_the_store() -> None:
    store = InMemoryTaskStore()
    store.create("a")
    store.create("b")
    surface = mount_mcp(Bridge(tree()), extensions=(TasksExtension(store),))
    _, body = rpc(surface, "tasks/list", {"_meta": meta()})
    assert {task["taskId"] for task in body["result"]["tasks"]} == {"a", "b"}


def test_disabled_subcapabilities_are_neither_advertised_nor_served() -> None:
    """A client never polls a method this mount will 404."""
    surface = mount_mcp(
        Bridge(tree()),
        extensions=(TasksExtension(allow_list=False, allow_cancel=False),),
    )
    _, discover = rpc(surface, "server/discover", {"_meta": meta()})
    capability = discover["result"]["capabilities"]["extensions"][
        "io.modelcontextprotocol/tasks"
    ]
    assert "list" not in capability
    assert "cancel" not in capability

    response, _ = rpc(surface, "tasks/list", {"_meta": meta()})
    assert response.status == 404


def test_expired_tasks_are_evicted_on_read() -> None:
    """TTL is enforced lazily; no background sweeper is required."""
    store = InMemoryTaskStore()
    store.create("gone", ttl=0)
    time.sleep(0.01)
    assert store.get("gone") is None
    assert store.list() == []
