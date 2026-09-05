"""Tests for the serve hierarchy and service lifecycle.

The contract these pin is ``docs/contracts/serve-lifecycle.md``,
§"Cross-language parity". Where a value is cross-language (a topic
string, an exit code, the name grammar) it is asserted as a literal
rather than against the implementation's own constant, so a change to
the constant does not silently take the assertion with it.
"""

from __future__ import annotations

import asyncio
import os
import re
import signal
import subprocess
import sys
import textwrap
import time
from pathlib import Path
from typing import Any

import pytest
from typer.testing import CliRunner

from hop_top_kit.bus import Bus, Event
from hop_top_kit.cli import create_app
from hop_top_kit.serve import (
    DEFAULT_FAILURE_POLICY,
    DEFAULT_READY_TIMEOUT,
    DEFAULT_SHUTDOWN_TIMEOUT,
    DEFAULT_STOP_TIMEOUT,
    NAME_PATTERN,
    RESERVED_NAMES,
    SHUTDOWN_SIGNALS,
    ResolveOutcome,
    ServeOptions,
    ServiceConfig,
    ServiceRegistrationError,
    ServiceRegistry,
    SignalController,
    Supervisor,
    SupervisorConfig,
    _RunState,
    code_for,
    configs_from_mapping,
    default_topics,
    exit_code_for,
    is_failure,
    is_failure_policy,
    is_reserved_name,
    register_serve,
    resolve,
    start_order,
    validate_name,
    worst_outcome,
)

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

_stop_sequence = 0


class FakeService:
    """A service whose whole lifecycle is scriptable from the test."""

    def __init__(
        self,
        name: str,
        *,
        fail_start_with: str | None = None,
        never_ready: bool = False,
        return_after: float | None = None,
        crash_after: str | None = None,
        hang_in_stop: bool = False,
        fail_stop_with: str | None = None,
        validate_msg: str | None = None,
        depends: list[str] | None = None,
        addr: str | None = None,
        cls: tuple[str, str] | None = None,
    ) -> None:
        self.name = name
        self._ready = False
        self.start_count = 0
        self.stop_count = 0
        self.stop_order: list[int] = []
        self._fail_start_with = fail_start_with
        self._never_ready = never_ready
        self._return_after = return_after
        self._crash_after = crash_after
        self._hang_in_stop = hang_in_stop
        self._fail_stop_with = fail_stop_with
        self._validate_msg = validate_msg
        self._depends = depends
        self._addr = addr
        self._cls = cls

        # The optional declarations are attached only when the test asked
        # for one, so a service that declares nothing is genuinely
        # indistinguishable from one that never had the member.
        if validate_msg is not None:
            self.validate = lambda: self._validate_msg  # type: ignore[method-assign]
        if depends is not None:
            self.depends_on = lambda: list(self._depends or [])  # type: ignore[method-assign]
        if addr is not None:
            self.addr = lambda: self._addr or ""  # type: ignore[method-assign]
        if cls is not None:
            self.service_class = lambda: self._cls  # type: ignore[method-assign]

    async def start(self, cancel: asyncio.Event, ready: Any) -> None:
        self.start_count += 1
        if self._fail_start_with:
            raise RuntimeError(self._fail_start_with)
        if not self._never_ready:
            self._ready = True
            ready()
        if self._return_after is not None:
            await asyncio.sleep(self._return_after)
            return
        if self._crash_after is not None:
            await asyncio.sleep(0.01)
            raise RuntimeError(self._crash_after)
        await cancel.wait()

    def ready(self) -> bool:
        return self._ready

    async def stop(self) -> None:
        global _stop_sequence
        self.stop_count += 1
        _stop_sequence += 1
        self.stop_order.append(_stop_sequence)
        if self._fail_stop_with:
            raise RuntimeError(self._fail_stop_with)
        if self._hang_in_stop:
            await asyncio.Event().wait()
        self._ready = False


def registry_of(*svcs: Any) -> ServiceRegistry:
    r = ServiceRegistry()
    for s in svcs:
        r.register(s)
    return r


class Recorder:
    """Collects every published event so a test can assert on the trace."""

    def __init__(self) -> None:
        self.events: list[Event] = []

    def publish(self, event: Event) -> None:
        self.events.append(event)

    def topics(self) -> list[str]:
        return [e.topic for e in self.events]

    def by_topic(self, topic: str) -> list[Event]:
        return [e for e in self.events if e.topic == topic]


class RecordingLogger:
    """A structured logger that keeps the fields rather than rendering them."""

    def __init__(self) -> None:
        self.records: list[tuple[str, str, dict[str, Any]]] = []

    def info(self, msg: str, **fields: Any) -> None:
        self.records.append(("info", msg, fields))

    def error(self, msg: str, **fields: Any) -> None:
        self.records.append(("error", msg, fields))


class Gate:
    """A policy gate with a fixed verdict."""

    def __init__(self, allowed: bool, reason: str = "") -> None:
        self._allowed = allowed
        self._reason = reason
        self.calls: list[tuple[str, str]] = []

    def allow(self, side_effect: str, network: str) -> tuple[bool, str]:
        self.calls.append((side_effect, network))
        return self._allowed, self._reason


def enabled(*names: str) -> dict[str, ServiceConfig]:
    return {n: ServiceConfig(enabled=True) for n in names}


async def cancel_after(evt: asyncio.Event, seconds: float) -> None:
    await asyncio.sleep(seconds)
    evt.set()


#: Upper bound on any single supervised run a test drives.
#:
#: A test that passes `cancel_in=None` is asserting that something other
#: than a timer ends the run — the failure policy, a start failure, a
#: service returning. If that mechanism regresses the run would otherwise
#: block forever, turning a clear assertion failure into a hung suite on
#: a shared machine. The cap converts the regression back into a failure.
RUN_CAP_SECONDS = 10.0


def run_supervised(
    registry: ServiceRegistry,
    selected: list[str],
    configs: dict[str, ServiceConfig] | None = None,
    *,
    cancel_in: float | None = 0.05,
    **sup_kwargs: Any,
) -> Any:
    """Drive one supervised run to completion on a fresh event loop."""

    async def main() -> Any:
        sup = Supervisor(registry, **sup_kwargs)
        cancel = asyncio.Event()
        timer = None
        if cancel_in is not None:
            timer = asyncio.create_task(cancel_after(cancel, cancel_in))
        try:
            return await asyncio.wait_for(
                sup.run(cancel, selected, configs or {}), timeout=RUN_CAP_SECONDS
            )
        finally:
            if timer is not None:
                timer.cancel()

    return asyncio.run(main())


# ---------------------------------------------------------------------------
# Naming
# ---------------------------------------------------------------------------


def test_name_grammar_accepts_contract_shape():
    for name in ("api", "socket", "a", "a1", "a-b", "mcp-stdio"):
        assert re.match(r"^[a-z][a-z0-9-]*$", name), name
        assert NAME_PATTERN.match(name), name
        assert validate_name(name) is None, name


def test_name_grammar_rejects_anything_outside_it():
    for name in ("", "API", "1api", "-api", "a_b", "a.b", "a b", "api!"):
        assert validate_name(name) is not None, name


def test_reserved_names_are_exactly_all_none_list():
    assert set(RESERVED_NAMES) == {"all", "none", "list"}
    for name in ("all", "none", "list"):
        assert is_reserved_name(name)
        assert "reserved" in (validate_name(name) or "")
    assert not is_reserved_name("api")


# ---------------------------------------------------------------------------
# Registration seam
# ---------------------------------------------------------------------------


def test_registry_lists_in_registration_order():
    r = registry_of(FakeService("zeta"), FakeService("alpha"), FakeService("mid"))
    assert r.names() == ["zeta", "alpha", "mid"]
    assert [s.name for s in r.list()] == ["zeta", "alpha", "mid"]


def test_registry_refuses_a_duplicate_at_construction():
    r = registry_of(FakeService("api"))
    with pytest.raises(ServiceRegistrationError, match="already registered"):
        r.register(FakeService("api"))
    # Refusal, not last-writer-wins: the first registration still stands.
    assert r.names() == ["api"]


def test_registry_refuses_an_invalid_or_reserved_name():
    r = ServiceRegistry()
    with pytest.raises(ServiceRegistrationError):
        r.register(FakeService("API"))
    with pytest.raises(ServiceRegistrationError):
        r.register(FakeService("list"))
    assert r.names() == []


def test_override_replaces_in_place_and_keeps_position():
    first = FakeService("api")
    r = registry_of(first, FakeService("socket"))
    replacement = FakeService("api")
    r.override(replacement)
    assert r.names() == ["api", "socket"]
    assert r.lookup("api") is replacement


def test_override_still_refuses_an_invalid_name():
    r = ServiceRegistry()
    with pytest.raises(ServiceRegistrationError):
        r.override(FakeService("Nope"))


# ---------------------------------------------------------------------------
# resolve — the hierarchy
# ---------------------------------------------------------------------------


def test_supervisor_form_selects_every_configured_and_enabled_service():
    r = registry_of(FakeService("api"), FakeService("socket"))
    out = resolve(r, [], configs=enabled("api", "socket"))
    assert out.selected == ["api", "socket"]
    assert out.explicit is False
    assert out.error is None


def test_supervisor_form_skips_a_disabled_service_silently():
    r = registry_of(FakeService("api"), FakeService("socket"))
    out = resolve(
        r, [], configs={"api": ServiceConfig(enabled=True), "socket": ServiceConfig(enabled=False)}
    )
    assert out.selected == ["api"]
    assert out.skipped == ["socket"]
    # Skipping is not an error and must not affect the exit code.
    assert out.error is None
    assert out.outcome is None


def test_supervisor_form_ignores_a_service_with_no_config_block():
    r = registry_of(FakeService("api"), FakeService("socket"))
    out = resolve(r, [], configs=enabled("api"))
    assert out.selected == ["api"]
    assert out.skipped == []


def test_selector_form_selects_exactly_the_named_service():
    r = registry_of(FakeService("api"), FakeService("socket"))
    out = resolve(r, ["socket"], configs=enabled("api", "socket"))
    assert out.selected == ["socket"]
    assert out.explicit is True


def test_selection_preserves_registration_order_not_config_order():
    r = registry_of(FakeService("zeta"), FakeService("alpha"))
    out = resolve(
        r, [], configs={"alpha": ServiceConfig(enabled=True), "zeta": ServiceConfig(enabled=True)}
    )
    assert out.selected == ["zeta", "alpha"]


# ---------------------------------------------------------------------------
# resolve — the override rule
# ---------------------------------------------------------------------------


def test_override_rule_starts_a_disabled_service_named_explicitly():
    r = registry_of(FakeService("api"))
    out = resolve(r, ["api"], configs={"api": ServiceConfig(enabled=False)})
    assert out.selected == ["api"]
    assert out.error is None


def test_override_rule_starts_a_service_with_no_config_block_at_all():
    r = registry_of(FakeService("api"))
    out = resolve(r, ["api"], configs={})
    assert out.selected == ["api"]
    assert out.error is None


def test_the_same_service_is_refused_under_the_supervisor_form():
    """The override rule is what separates the two forms, nothing else."""
    r = registry_of(FakeService("api"))
    cfgs = {"api": ServiceConfig(enabled=False)}
    assert resolve(r, ["api"], configs=cfgs).selected == ["api"]
    agg = resolve(r, [], configs=cfgs)
    assert agg.selected == []
    assert agg.error is not None
    assert agg.error.exit_code == 2


def test_override_rule_does_not_override_the_configuration_gate():
    r = registry_of(FakeService("api", validate_msg="addr: missing"))
    out = resolve(r, ["api"], configs={})
    assert out.selected == []
    assert out.outcome == "config-invalid"
    assert out.error is not None
    assert out.error.exit_code == 2
    assert out.error.code == "USAGE"
    assert 'service "api": addr: missing' in out.error.message


def test_override_rule_does_not_override_the_policy_gate():
    r = registry_of(FakeService("api", cls=("write", "remote")))
    out = resolve(r, ["api"], configs={}, policy=Gate(False, "remote writes denied"))
    assert out.selected == []
    assert out.outcome == "policy-denied"
    assert out.error is not None
    assert out.error.exit_code == 5
    assert out.error.code == "UNAUTHORIZED"
    assert "side_effect=write" in out.error.message
    assert "network=remote" in out.error.message


def test_gates_are_evaluated_in_order_registration_config_policy():
    """An unknown name never reaches the config gate, and an invalid
    config never reaches the policy gate."""
    gate = Gate(False)
    r = registry_of(FakeService("api", validate_msg="bad", cls=("write", "remote")))

    unknown = resolve(r, ["ghost"], configs={}, policy=gate)
    assert unknown.outcome == "unknown-service"
    assert gate.calls == []  # policy not consulted for an unknown name

    invalid = resolve(r, ["api"], configs={}, policy=gate)
    assert invalid.outcome == "config-invalid"
    assert gate.calls == []  # policy not consulted past a config failure


def test_unclassified_service_passes_the_policy_gate():
    gate = Gate(False)
    r = registry_of(FakeService("api"))
    out = resolve(r, ["api"], configs={}, policy=gate)
    assert out.selected == ["api"]
    assert gate.calls == []


def test_every_service_passes_when_no_gate_is_wired():
    r = registry_of(FakeService("api", cls=("write", "remote")))
    out = resolve(r, ["api"], configs={}, policy=None)
    assert out.selected == ["api"]


# ---------------------------------------------------------------------------
# resolve — invalid selection
# ---------------------------------------------------------------------------


def test_two_or_more_positional_arguments_is_usage_exit_2():
    r = registry_of(FakeService("api"), FakeService("socket"))
    out = resolve(r, ["api", "socket"], configs=enabled("api", "socket"))
    assert out.selected == []
    assert out.outcome == "invalid-selection"
    assert out.error is not None
    assert out.error.code == "USAGE"
    assert out.error.exit_code == 2

    three = resolve(r, ["a", "b", "c"], configs={})
    assert three.error is not None
    assert three.error.exit_code == 2


def test_unknown_service_is_not_found_exit_3_naming_the_known_set():
    r = registry_of(FakeService("api"), FakeService("socket"))
    out = resolve(r, ["ghost"], configs={})
    assert out.outcome == "unknown-service"
    assert out.error is not None
    assert out.error.code == "NOT_FOUND"
    assert out.error.exit_code == 3
    assert "api" in out.error.message
    assert "socket" in out.error.message


def test_a_reserved_word_is_refused_as_a_selection():
    """`list` can never be registered, so naming it can only be a miss."""
    r = registry_of(FakeService("api"))
    out = resolve(r, ["list"], configs={})
    assert out.outcome == "unknown-service"
    assert out.error is not None
    assert out.error.exit_code == 3


def test_zero_resolved_services_under_the_supervisor_form_exits_2():
    r = registry_of(FakeService("api"))
    out = resolve(r, [], configs={"api": ServiceConfig(enabled=False)})
    assert out.selected == []
    assert out.outcome == "no-services"
    assert out.error is not None
    assert out.error.code == "USAGE"
    # Not a clean exit: a process that exits 0 without listening is
    # indistinguishable from a successful start to systemd.
    assert out.error.exit_code == 2


def test_empty_registry_under_the_supervisor_form_exits_2():
    out = resolve(ServiceRegistry(), [], configs={})
    assert out.outcome == "no-services"
    assert out.error is not None
    assert out.error.exit_code == 2


# ---------------------------------------------------------------------------
# Exit-code taxonomy
# ---------------------------------------------------------------------------


def test_every_outcome_maps_onto_the_contract_table():
    """The table from the contract, transcribed as literals."""
    table = {
        "clean-stop": ("OK", 0),
        "invalid-selection": ("USAGE", 2),
        "config-invalid": ("USAGE", 2),
        "no-services": ("USAGE", 2),
        "unknown-service": ("NOT_FOUND", 3),
        "policy-denied": ("UNAUTHORIZED", 5),
        "start-failed": ("GENERIC", 1),
        "runtime-crash": ("GENERIC", 1),
        "shutdown-timeout": ("GENERIC", 1),
    }
    for outcome, (code, exit_code) in table.items():
        assert code_for(outcome) == code, outcome
        assert exit_code_for(outcome) == exit_code, outcome


def test_clean_stop_is_success_and_everything_else_is_failure():
    assert not is_failure("clean-stop")
    for outcome in (
        "invalid-selection",
        "config-invalid",
        "no-services",
        "unknown-service",
        "policy-denied",
        "start-failed",
        "runtime-crash",
        "shutdown-timeout",
    ):
        assert is_failure(outcome), outcome


def test_an_unknown_outcome_is_a_failure_not_a_success():
    """A kind added without a table row must fail loudly, not exit 0."""
    assert exit_code_for("something-new") == 1
    assert code_for("something-new") == "GENERIC"
    assert is_failure("something-new")


def test_worst_outcome_keeps_the_first_failure_across_a_whole_run():
    assert worst_outcome([]) == "clean-stop"
    assert worst_outcome(["clean-stop", "clean-stop"]) == "clean-stop"
    # Under isolate a run may observe several; the exit code reflects the
    # worst across the whole run, not the last one.
    assert worst_outcome(["runtime-crash", "clean-stop"]) == "runtime-crash"
    assert worst_outcome(["start-failed", "shutdown-timeout"]) == "start-failed"
    assert worst_outcome(["clean-stop", "shutdown-timeout"]) == "shutdown-timeout"


def test_transient_propagation_keeps_exit_6():
    """A serve failure wrapping a kit transient error keeps exit 6, so an
    agent's retry branch behaves the same whichever language it drives."""
    from hop_top_kit.output.error import CODE_TRANSIENT, EXIT_TRANSIENT, wrap_error

    err = wrap_error(RuntimeError("upstream blip"), CODE_TRANSIENT, EXIT_TRANSIENT)
    assert err is not None
    assert err.exit_code == 6
    assert err.transience == "transient"


# ---------------------------------------------------------------------------
# Ordering
# ---------------------------------------------------------------------------


def test_start_order_is_registration_order_with_no_declarations():
    r = registry_of(FakeService("a"), FakeService("b"), FakeService("c"))
    assert start_order(r, ["a", "b", "c"]) == ["a", "b", "c"]


def test_start_order_puts_a_dependency_before_its_dependent():
    r = registry_of(FakeService("api", depends=["db"]), FakeService("db"))
    assert start_order(r, ["api", "db"]) == ["db", "api"]


def test_start_order_ignores_a_dependency_outside_the_selected_set():
    r = registry_of(FakeService("api", depends=["db"]), FakeService("db"))
    assert start_order(r, ["api"]) == ["api"]


def test_start_order_raises_on_a_dependency_cycle():
    r = registry_of(FakeService("a", depends=["b"]), FakeService("b", depends=["a"]))
    with pytest.raises(ServiceRegistrationError, match="dependency cycle"):
        start_order(r, ["a", "b"])


# ---------------------------------------------------------------------------
# Lifecycle topics
# ---------------------------------------------------------------------------


def test_default_topics_are_exactly_the_six_contract_strings():
    """Asserted as literals: a subscriber is written against the string
    and does not know which language published it."""
    assert set(default_topics().values()) == {
        "kit.serve.service.started",
        "kit.serve.service.ready_reported",
        "kit.serve.service.failed",
        "kit.serve.service.stopped",
        "kit.serve.supervisor.ready_reported",
        "kit.serve.supervisor.stopped",
    }


def test_no_topic_uses_a_bare_ready_action():
    """A bare `ready` fails past-tense validation; Go subscribers reject it."""
    for topic in default_topics().values():
        assert not topic.endswith(".ready")
        assert len(topic.split(".")) == 4


def test_topic_prefix_is_rebrandable():
    topics = default_topics("mytool.serve")
    assert topics["service.started"] == "mytool.serve.service.started"
    assert topics["supervisor.stopped"] == "mytool.serve.supervisor.stopped"
    # An empty prefix falls back rather than producing a 3-segment topic.
    assert default_topics("")["service.started"] == "kit.serve.service.started"


# ---------------------------------------------------------------------------
# Supervisor
# ---------------------------------------------------------------------------


def test_supervisor_starts_reports_ready_and_stops_cleanly_on_a_signal():
    api = FakeService("api")
    rec = Recorder()
    res = run_supervised(registry_of(api), ["api"], enabled("api"), publisher=rec)

    assert res.outcome == "clean-stop"
    assert res.exit_code == 0
    assert res.started == ["api"]
    assert res.ready == ["api"]
    assert res.failed == {}
    assert api.start_count == 1
    assert api.stop_count == 1
    assert rec.topics() == [
        "kit.serve.service.started",
        "kit.serve.service.ready_reported",
        "kit.serve.supervisor.ready_reported",
        "kit.serve.service.stopped",
        "kit.serve.supervisor.stopped",
    ]


def test_address_rides_on_ready_reported_and_nowhere_else():
    rec = Recorder()
    run_supervised(
        registry_of(FakeService("api", addr="127.0.0.1:54321")),
        ["api"],
        enabled("api"),
        publisher=rec,
    )
    ready = rec.by_topic("kit.serve.service.ready_reported")
    assert len(ready) == 1
    assert ready[0].payload["address"] == "127.0.0.1:54321"
    for e in rec.events:
        if e.topic != "kit.serve.service.ready_reported":
            assert "address" not in e.payload, e.topic


def test_service_without_an_address_carries_no_address_key():
    rec = Recorder()
    run_supervised(registry_of(FakeService("api")), ["api"], enabled("api"), publisher=rec)
    ready = rec.by_topic("kit.serve.service.ready_reported")
    assert "address" not in ready[0].payload


def test_identifier_travels_in_the_payload_never_in_the_topic():
    rec = Recorder()
    run_supervised(registry_of(FakeService("api")), ["api"], enabled("api"), publisher=rec)
    for e in rec.events:
        assert "api" not in e.topic, e.topic
    for e in rec.by_topic("kit.serve.service.started"):
        assert e.payload["service"] == "api"


def test_failure_reason_travels_in_the_payload_under_error():
    rec = Recorder()
    run_supervised(
        registry_of(FakeService("api", fail_start_with="bind: address in use")),
        ["api"],
        enabled("api"),
        publisher=rec,
        cancel_in=None,
    )
    failed = rec.by_topic("kit.serve.service.failed")
    assert len(failed) == 1
    assert failed[0].payload["error"] == "bind: address in use"
    assert "bind" not in failed[0].topic


def test_aggregate_ready_only_fires_when_every_service_is_ready():
    rec = Recorder()
    run_supervised(
        registry_of(FakeService("api"), FakeService("socket")),
        ["api", "socket"],
        enabled("api", "socket"),
        publisher=rec,
    )
    order = rec.topics()
    agg = order.index("kit.serve.supervisor.ready_reported")
    readies = [i for i, t in enumerate(order) if t == "kit.serve.service.ready_reported"]
    assert len(readies) == 2
    assert all(i < agg for i in readies)


def test_aggregate_ready_never_fires_when_a_start_fails():
    rec = Recorder()
    run_supervised(
        registry_of(FakeService("api"), FakeService("socket", fail_start_with="nope")),
        ["api", "socket"],
        enabled("api", "socket"),
        publisher=rec,
        cancel_in=None,
    )
    assert "kit.serve.supervisor.ready_reported" not in rec.topics()


def test_aggregate_ready_is_withheld_while_any_started_service_is_not_ready():
    """The guard on its own, not through the start path.

    `_start_all` refuses to advance past a service that has not reported
    ready, so no integration route reaches the aggregate with a partial
    ready set today. That makes the check inside the emit look redundant
    — it is not: it is the invariant the contract states ("the aggregate
    is ready when every started service is ready"), and it is what keeps
    a future concurrent start honest. Driving it directly is the only
    way to pin it.
    """
    rec = Recorder()
    sup = Supervisor(registry_of(FakeService("api"), FakeService("socket")), publisher=rec)

    async def main() -> None:
        st = _RunState(time.monotonic)
        st.started.extend(["api", "socket"])
        st.ready.append("api")  # socket started but never reported
        sup._emit_aggregate_ready(st)

    asyncio.run(main())
    assert "kit.serve.supervisor.ready_reported" not in rec.topics()

    # ...and it does fire once the set is complete.
    rec2 = Recorder()
    sup2 = Supervisor(registry_of(FakeService("api")), publisher=rec2)

    async def complete() -> None:
        st = _RunState(time.monotonic)
        st.started.append("api")
        st.ready.append("api")
        sup2._emit_aggregate_ready(st)

    asyncio.run(complete())
    assert "kit.serve.supervisor.ready_reported" in rec2.topics()


def test_stop_runs_in_the_exact_reverse_of_start_order():
    a, b, c = FakeService("a"), FakeService("b"), FakeService("c")
    res = run_supervised(registry_of(a, b, c), ["a", "b", "c"], enabled("a", "b", "c"))
    assert res.started == ["a", "b", "c"]
    # Reverse of actual start order, one at a time.
    assert c.stop_order[0] < b.stop_order[0] < a.stop_order[0]


def test_a_start_failure_is_start_failed_at_exit_1():
    res = run_supervised(
        registry_of(FakeService("api", fail_start_with="bind failed")),
        ["api"],
        enabled("api"),
        cancel_in=None,
    )
    assert res.outcome == "start-failed"
    assert res.exit_code == 1
    assert res.error is not None
    assert res.error.code == "GENERIC"
    assert res.failed["api"] == "bind failed"


def test_a_readiness_timeout_is_a_start_failure():
    res = run_supervised(
        registry_of(FakeService("api", never_ready=True)),
        ["api"],
        {"api": ServiceConfig(enabled=True, ready_timeout=0.05)},
        cancel_in=None,
    )
    assert res.outcome == "start-failed"
    assert res.exit_code == 1
    assert "not ready" in res.failed["api"]


def test_a_service_returning_before_ready_is_a_start_failure():
    """It was asked to serve and it did not, even returning cleanly."""
    res = run_supervised(
        registry_of(FakeService("api", never_ready=True, return_after=0.01)),
        ["api"],
        enabled("api"),
        cancel_in=None,
    )
    assert res.outcome == "start-failed"
    assert res.exit_code == 1


def test_a_later_service_is_not_started_after_an_earlier_one_fails():
    first = FakeService("a", fail_start_with="nope")
    second = FakeService("b")
    run_supervised(registry_of(first, second), ["a", "b"], enabled("a", "b"), cancel_in=None)
    assert first.start_count == 1
    assert second.start_count == 0


def test_fail_fast_brings_everything_down_when_one_service_crashes():
    api = FakeService("api")
    crasher = FakeService("crash", crash_after="upstream gone")
    res = run_supervised(
        registry_of(api, crasher),
        ["api", "crash"],
        enabled("api", "crash"),
        cancel_in=None,
        config=SupervisorConfig(failure_policy="fail-fast"),
    )
    assert res.outcome == "runtime-crash"
    assert res.exit_code == 1
    assert res.failed["crash"] == "upstream gone"
    # The healthy one was brought down too.
    assert api.stop_count == 1


def test_isolate_keeps_the_rest_running_until_the_last_one_stops():
    api = FakeService("api", return_after=0.2)
    crasher = FakeService("crash", crash_after="upstream gone")
    res = run_supervised(
        registry_of(crasher, api),
        ["crash", "api"],
        enabled("crash", "api"),
        cancel_in=None,
        config=SupervisorConfig(failure_policy="isolate"),
    )
    # The process survived the crash and ran on; the exit code still
    # reflects the worst outcome across the whole run.
    assert res.exit_code == 1
    assert res.failed["crash"] == "upstream gone"
    assert api.start_count == 1


def test_a_stop_that_exceeds_its_budget_is_abandoned_and_the_next_runs():
    straggler = FakeService("slow", hang_in_stop=True)
    quick = FakeService("quick")
    res = run_supervised(
        registry_of(quick, straggler),
        ["quick", "slow"],
        {
            "quick": ServiceConfig(enabled=True, stop_timeout=1.0),
            "slow": ServiceConfig(enabled=True, stop_timeout=0.05),
        },
    )
    # The straggler did not block the whole shutdown.
    assert quick.stop_count == 1
    assert "slow" in res.failed
    assert res.exit_code == 1


def test_a_stop_that_raises_is_reported_as_a_failure():
    res = run_supervised(
        registry_of(FakeService("api", fail_stop_with="unlink failed")),
        ["api"],
        enabled("api"),
    )
    assert res.failed["api"] == "unlink failed"
    assert res.exit_code == 1


def test_exceeding_the_total_shutdown_budget_is_exit_1():
    a = FakeService("a", hang_in_stop=True)
    b = FakeService("b", hang_in_stop=True)
    res = run_supervised(
        registry_of(a, b),
        ["a", "b"],
        {
            "a": ServiceConfig(enabled=True, stop_timeout=5.0),
            "b": ServiceConfig(enabled=True, stop_timeout=5.0),
        },
        config=SupervisorConfig(shutdown_timeout=0.06),
    )
    assert res.outcome == "shutdown-timeout"
    assert res.exit_code == 1
    assert res.error is not None
    assert res.error.code == "GENERIC"


def test_a_second_signal_abandons_the_drain():
    """Operators must be able to escalate without reaching for SIGKILL."""

    async def main() -> Any:
        escalate = asyncio.Event()
        escalate.set()  # a second signal already arrived
        sup = Supervisor(registry_of(FakeService("api")), escalate=escalate)
        cancel = asyncio.Event()
        timer = asyncio.create_task(cancel_after(cancel, 0.05))
        try:
            return await sup.run(cancel, ["api"], enabled("api"))
        finally:
            timer.cancel()

    res = asyncio.run(main())
    assert res.outcome == "runtime-crash"
    assert res.exit_code == 1
    assert "second signal" in res.failed["api"]


def test_an_empty_selection_is_refused_rather_than_exiting_0():
    res = run_supervised(registry_of(FakeService("api")), [], {}, cancel_in=None)
    assert res.outcome == "no-services"
    assert res.exit_code == 2


def test_the_log_counterpart_carries_structured_fields_with_no_bus():
    log = RecordingLogger()
    run_supervised(
        registry_of(FakeService("api", addr="127.0.0.1:9000")),
        ["api"],
        enabled("api"),
        logger=log,
    )
    actions = [msg for _lvl, msg, _f in log.records]
    assert "serve: started" in actions
    assert "serve: ready_reported" in actions
    assert "serve: stopped" in actions

    ready = next(
        f
        for lvl, msg, f in log.records
        if msg == "serve: ready_reported" and f.get("object") == "service"
    )
    # Structured fields, not interpolated into the message text: that is
    # what makes a startup trace greppable across a mixed-language fleet.
    assert ready["service"] == "api"
    assert ready["address"] == "127.0.0.1:9000"


def test_a_failure_logs_at_error_level_not_info():
    log = RecordingLogger()
    run_supervised(
        registry_of(FakeService("api", fail_start_with="boom")),
        ["api"],
        enabled("api"),
        logger=log,
        cancel_in=None,
    )
    failed = [(lvl, f) for lvl, msg, f in log.records if msg == "serve: failed"]
    assert failed
    assert all(lvl == "error" for lvl, _ in failed)
    assert failed[0][1]["error"] == "boom"
    # started/ready_reported/stopped stay at info.
    for lvl, msg, _f in log.records:
        if msg != "serve: failed":
            assert lvl == "info", msg


def test_a_publisher_that_raises_never_fails_the_lifecycle():
    class Exploding:
        def publish(self, event: Event) -> None:
            raise RuntimeError("bus is down")

    res = run_supervised(
        registry_of(FakeService("api")), ["api"], enabled("api"), publisher=Exploding()
    )
    assert res.outcome == "clean-stop"
    assert res.exit_code == 0


def test_a_real_bus_receives_the_lifecycle_topics():
    """Python has a bus, so the topic strings are a hard obligation."""
    seen: list[str] = []
    bus = Bus()
    bus.subscribe("kit.serve.#", lambda e: seen.append(e.topic))
    run_supervised(registry_of(FakeService("api")), ["api"], enabled("api"), publisher=bus)
    assert "kit.serve.service.started" in seen
    assert "kit.serve.service.ready_reported" in seen
    assert "kit.serve.supervisor.stopped" in seen


def test_two_runs_on_one_registry_each_observe_only_their_own_signal():
    """A second run must not inherit the first run's cancellation."""
    api = FakeService("api")
    reg = registry_of(api)

    async def main() -> tuple[Any, Any]:
        sup = Supervisor(reg)
        first_cancel = asyncio.Event()
        t1 = asyncio.create_task(cancel_after(first_cancel, 0.05))
        first = await sup.run(first_cancel, ["api"], enabled("api"))
        t1.cancel()

        second_cancel = asyncio.Event()
        t2 = asyncio.create_task(cancel_after(second_cancel, 0.05))
        second = await sup.run(second_cancel, ["api"], enabled("api"))
        t2.cancel()
        return first, second

    first, second = asyncio.run(main())
    assert first.outcome == "clean-stop"
    # The second run served under its own cancellation rather than
    # returning instantly on the first run's already-set event.
    assert second.outcome == "clean-stop"
    assert second.ready == ["api"]
    assert api.start_count == 2
    assert api.stop_count == 2


def test_a_run_does_not_leave_its_cancellation_behind_for_the_next_one():
    """The run controller must be per-run, not the caller's own event.

    The contract's re-execution rule is about a second run inheriting the
    first run's cancellation: alive, and the new run ignores its own
    signal; already cancelled, and the new run stops without serving.
    Handing both runs the *same* event is what exposes an implementation
    that aliases the caller's event instead of relaying into a fresh one
    — with a per-run event the aliasing is invisible.
    """
    api = FakeService("api")
    reg = registry_of(api)

    async def main() -> tuple[Any, Any]:
        sup = Supervisor(reg)
        shared = asyncio.Event()
        t1 = asyncio.create_task(cancel_after(shared, 0.05))
        first = await sup.run(shared, ["api"], enabled("api"))
        t1.cancel()

        # The caller's event stays set from the first run. A supervisor
        # that relays into its own controller still serves; one that
        # aliased the caller's event would return without serving.
        assert shared.is_set()
        second_cancel = asyncio.Event()
        t2 = asyncio.create_task(cancel_after(second_cancel, 0.05))
        second = await sup.run(second_cancel, ["api"], enabled("api"))
        t2.cancel()
        return first, second

    first, second = asyncio.run(main())
    assert first.outcome == "clean-stop"
    assert second.outcome == "clean-stop"
    # It actually served: it reported ready rather than falling straight
    # through on a cancellation it inherited.
    assert second.ready == ["api"]
    assert api.start_count == 2


def test_a_run_does_not_set_the_caller_s_own_cancellation():
    """The supervisor owns a controller of its own, not the caller's event.

    A failure under fail-fast, and the end of every run, trip the run's
    cancellation. If that were the caller's own event the supervisor
    would be writing to a signal it does not own — and a caller that
    reuses the event (a restart loop, an embedding host) would find its
    next run cancelled before it started.
    """
    caller = asyncio.Event()

    async def main() -> Any:
        sup = Supervisor(registry_of(FakeService("crash", crash_after="boom")))
        return await sup.run(caller, ["crash"], enabled("crash"))

    res = asyncio.run(main())
    assert res.outcome == "runtime-crash"
    # The run cancelled itself; the caller's event is untouched.
    assert not caller.is_set()


def test_an_already_cancelled_signal_still_performs_the_ordered_stop():
    api = FakeService("api")

    async def main() -> Any:
        cancel = asyncio.Event()
        cancel.set()
        return await Supervisor(registry_of(api)).run(cancel, ["api"], enabled("api"))

    res = asyncio.run(main())
    assert res.outcome == "clean-stop"
    assert api.stop_count == 1


# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------


def test_configuration_defaults_match_the_contract_table():
    """Literals from the contract's `services.*` table."""
    assert DEFAULT_READY_TIMEOUT == 30.0
    assert DEFAULT_STOP_TIMEOUT == 30.0
    assert DEFAULT_SHUTDOWN_TIMEOUT == 60.0
    assert DEFAULT_FAILURE_POLICY == "fail-fast"
    assert ServiceConfig().enabled is False
    assert ServiceConfig().ready_timeout == 30.0
    assert ServiceConfig().stop_timeout == 30.0
    assert SupervisorConfig().shutdown_timeout == 60.0
    assert SupervisorConfig().failure_policy == "fail-fast"


def test_failure_policy_has_exactly_two_values():
    assert is_failure_policy("fail-fast")
    assert is_failure_policy("isolate")
    assert not is_failure_policy("continue")
    assert not is_failure_policy("")


def test_config_keys_are_read_by_their_contract_names():
    """A single YAML file is read by a fleet of tools that are not all
    the same language, so the key names travel."""
    per_service, supervisor = configs_from_mapping(
        {
            "api": {"enabled": True, "ready_timeout": "5s", "stop_timeout": "500ms"},
            "socket": {"enabled": False},
            "failure_policy": "isolate",
            "shutdown_timeout": "2m",
        }
    )
    assert per_service["api"].enabled is True
    assert per_service["api"].ready_timeout == 5.0
    assert per_service["api"].stop_timeout == 0.5
    assert per_service["socket"].enabled is False
    assert supervisor.failure_policy == "isolate"
    assert supervisor.shutdown_timeout == 120.0
    # The supervisor-scoped keys are not mistaken for services.
    assert "failure_policy" not in per_service
    assert "shutdown_timeout" not in per_service


def test_enabled_defaults_to_false_and_needs_an_explicit_true():
    """An unrequested open port is the risk the default guards against."""
    per_service, _ = configs_from_mapping(
        {"a": {}, "b": {"enabled": "yes"}, "c": {"enabled": 1}, "d": {"enabled": True}}
    )
    assert per_service["a"].enabled is False
    assert per_service["b"].enabled is False
    assert per_service["c"].enabled is False
    assert per_service["d"].enabled is True


def test_an_unparseable_duration_leaves_the_default_standing():
    per_service, sup = configs_from_mapping(
        {"api": {"ready_timeout": "soon"}, "shutdown_timeout": "later"}
    )
    assert per_service["api"].ready_timeout == 30.0
    assert sup.shutdown_timeout == 60.0


def test_an_unknown_failure_policy_falls_back_to_fail_fast():
    _per, sup = configs_from_mapping({"failure_policy": "whatever"})
    assert sup.failure_policy == "fail-fast"


# ---------------------------------------------------------------------------
# Signals
# ---------------------------------------------------------------------------


def test_shutdown_signals_are_sigint_and_sigterm():
    assert set(SHUTDOWN_SIGNALS) == {signal.SIGINT, signal.SIGTERM}


def test_signal_controller_drains_on_the_first_and_escalates_on_the_second():
    async def main() -> tuple[bool, bool, bool, bool]:
        c = SignalController()
        try:
            before = (c.cancel.is_set(), c.escalate.is_set())
            c._on_signal()
            after_first = c.cancel.is_set() and not c.escalate.is_set()
            c._on_signal()
            after_second = c.escalate.is_set()
            return (*before, after_first, after_second)
        finally:
            c.stop()

    cancel0, escalate0, after_first, after_second = asyncio.run(main())
    assert cancel0 is False
    assert escalate0 is False
    assert after_first is True
    assert after_second is True


def test_signal_controller_removes_its_handlers_on_stop():
    async def main() -> list[Any]:
        c = SignalController()
        installed = list(c._installed)
        c.stop()
        c.stop()  # idempotent
        assert c._installed == []
        return installed

    installed = asyncio.run(main())
    # On a platform that can install them at all, both were installed.
    assert set(installed) <= set(SHUTDOWN_SIGNALS)


# ---------------------------------------------------------------------------
# Command wiring
# ---------------------------------------------------------------------------


def app_with(
    registry: ServiceRegistry,
    configs: dict[str, ServiceConfig] | None = None,
    **extra: Any,
) -> tuple[Any, list[tuple[int, str | None]], list[str]]:
    """Build a Typer app whose serve run reports its exit code to the test."""
    exits: list[tuple[int, str | None]] = []
    out: list[str] = []

    class Sink:
        def write(self, s: str) -> int:
            out.append(s)
            return len(s)

    app, _theme = create_app(name="tool", version="1.0.0", help="Test tool")
    register_serve(
        app,
        ServeOptions(
            registry=registry,
            configs=configs or {},
            stdout=Sink(),
            on_exit=lambda code, err: exits.append((code, err.message if err else None)),
            **extra,
        ),
    )
    return app, exits, out


def test_serve_is_mounted_with_an_optional_service_operand():
    app, _exits, _out = app_with(registry_of(FakeService("api")))
    res = CliRunner().invoke(app, ["serve", "--help"])
    assert res.exit_code == 0
    assert "serve" in res.output


def test_no_list_child_is_mounted():
    """`list` is reserved selector vocabulary: a `serve list` child would
    be indistinguishable from the selector form naming a service."""
    app, exits, _out = app_with(registry_of(FakeService("api")))
    CliRunner().invoke(app, ["serve", "list"])
    # `list` reaches the selector, which refuses it as an unknown
    # service — not a child command that ran an inspection.
    assert exits == [(3, 'unknown service "list"; known: api')]


def test_list_flag_prints_every_service_in_registration_order():
    app, _exits, out = app_with(
        registry_of(FakeService("zeta"), FakeService("alpha")),
        {"zeta": ServiceConfig(enabled=True)},
    )
    res = CliRunner().invoke(app, ["serve", "--list"])
    assert res.exit_code == 0
    text = "".join(out)
    assert text.index("zeta") < text.index("alpha")
    assert "True" in text


def test_command_exits_2_on_two_or_more_service_operands():
    app, exits, _out = app_with(registry_of(FakeService("api"), FakeService("socket")))
    CliRunner().invoke(app, ["serve", "api", "socket"])
    assert exits[0][0] == 2


def test_command_exits_3_on_an_unknown_service():
    app, exits, _out = app_with(registry_of(FakeService("api")))
    CliRunner().invoke(app, ["serve", "ghost"])
    assert exits[0][0] == 3


def test_command_exits_5_when_the_policy_gate_denies_the_named_service():
    app, exits, _out = app_with(
        registry_of(FakeService("api", cls=("write", "remote"))),
        {},
        policy=Gate(False, "no remote writes"),
    )
    CliRunner().invoke(app, ["serve", "api"])
    assert exits[0][0] == 5


def test_command_exits_2_when_the_supervisor_form_resolves_to_nothing():
    app, exits, _out = app_with(
        registry_of(FakeService("api")), {"api": ServiceConfig(enabled=False)}
    )
    CliRunner().invoke(app, ["serve"])
    assert exits[0][0] == 2


def test_command_exits_2_on_an_invalid_configuration():
    app, exits, _out = app_with(registry_of(FakeService("api", validate_msg="addr: missing")))
    CliRunner().invoke(app, ["serve", "api"])
    assert exits[0][0] == 2


def test_command_exits_1_when_the_selected_service_fails_to_start():
    app, exits, _out = app_with(registry_of(FakeService("api", fail_start_with="boom")))
    CliRunner().invoke(app, ["serve", "api"])
    assert exits[0][0] == 1


def test_command_runs_a_disabled_service_through_the_selector_and_exits_0():
    """The override rule, end to end through the command."""
    api = FakeService("api", return_after=0.05)
    app, exits, _out = app_with(registry_of(api), {"api": ServiceConfig(enabled=False)})
    CliRunner().invoke(app, ["serve", "api"])
    assert exits[0][0] == 0
    assert api.start_count == 1


# ---------------------------------------------------------------------------
# Serving in a real process
# ---------------------------------------------------------------------------
#
# Every test above drives the supervisor from inside a test process, so
# they would pass even if nothing kept the loop alive. Only a subprocess
# proves the process actually stays up: it is the difference between "the
# run returned" and "the tool served".

_E2E_TOOL = textwrap.dedent(
    """
    import asyncio, sys
    from hop_top_kit.cli import create_app
    from hop_top_kit.serve import (
        ServeOptions, ServiceConfig, ServiceRegistry, register_serve,
    )

    class Api:
        name = "api"
        def __init__(self):
            self._ready = False
        async def start(self, cancel, ready):
            self._ready = True
            ready()
            print("SERVING", flush=True)
            await cancel.wait()
        def ready(self):
            return self._ready
        async def stop(self):
            self._ready = False

    registry = ServiceRegistry()
    registry.register(Api())
    app, _theme = create_app(name="tool", version="1.0.0", help="Test tool")
    register_serve(app, ServeOptions(
        registry=registry, configs={"api": ServiceConfig(enabled=True)},
    ))
    app()
    """
)


@pytest.fixture(scope="module")
def e2e_tool(tmp_path_factory: pytest.TempPathFactory) -> Path:
    path = tmp_path_factory.mktemp("serve-e2e") / "tool.py"
    path.write_text(_E2E_TOOL, encoding="utf-8")
    return path


def _child_env() -> dict[str, str]:
    env = dict(os.environ)
    # The child imports the package under test from this checkout.
    root = str(Path(__file__).resolve().parents[1])
    env["PYTHONPATH"] = root + (os.pathsep + env["PYTHONPATH"] if env.get("PYTHONPATH") else "")
    return env


def _run_to_completion(tool: Path, argv: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [sys.executable, str(tool), *argv],
        capture_output=True,
        text=True,
        timeout=60,
        env=_child_env(),
        check=False,
    )


def test_process_stays_up_while_serving_and_exits_0_on_sigterm(e2e_tool: Path):
    proc = subprocess.Popen(
        [sys.executable, str(e2e_tool), "serve", "api"],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        env=_child_env(),
    )
    try:
        # Long enough that a process which exited "successfully" without
        # serving would already be gone.
        deadline = time.monotonic() + 2.0
        while time.monotonic() < deadline:
            time.sleep(0.1)
        assert proc.poll() is None, "process exited instead of serving"

        proc.send_signal(signal.SIGTERM)
        # A signal-initiated stop is a clean stop: answering SIGTERM
        # non-zero makes every rolling restart look like a crash.
        assert proc.wait(timeout=30) == 0
    finally:
        if proc.poll() is None:
            proc.kill()
            proc.wait(timeout=10)


def test_process_stays_up_under_the_supervisor_form_too(e2e_tool: Path):
    proc = subprocess.Popen(
        [sys.executable, str(e2e_tool), "serve"],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        env=_child_env(),
    )
    try:
        time.sleep(1.5)
        assert proc.poll() is None, "supervisor form exited instead of serving"
        proc.send_signal(signal.SIGINT)
        assert proc.wait(timeout=30) == 0
    finally:
        if proc.poll() is None:
            proc.kill()
            proc.wait(timeout=10)


def test_process_exits_2_on_two_service_operands(e2e_tool: Path):
    assert _run_to_completion(e2e_tool, ["serve", "api", "socket"]).returncode == 2


def test_process_exits_3_on_an_unknown_service(e2e_tool: Path):
    res = _run_to_completion(e2e_tool, ["serve", "ghost"])
    assert res.returncode == 3
    assert "NOT_FOUND" in res.stderr


def test_process_exits_0_on_the_list_flag(e2e_tool: Path):
    res = _run_to_completion(e2e_tool, ["serve", "--list"])
    assert res.returncode == 0
    assert "api" in res.stdout


# ---------------------------------------------------------------------------
# Conformance record
# ---------------------------------------------------------------------------


def test_parity_record_marks_python_shipped():
    """The fixture records the gap; shipping the port closes it."""
    import json

    root = Path(__file__).resolve().parents[3]
    fixture = root / "contracts" / "parity" / "serve.json"
    if not fixture.is_file():  # published package, outside the monorepo
        pytest.skip("contracts/parity/serve.json is monorepo-only")
    data = json.loads(fixture.read_text(encoding="utf-8"))

    py = data["ports"]["py"]
    behaviors = list(data["behaviors"])
    for behavior in behaviors:
        assert py[behavior] == "SHIPPED", behavior
    assert py["implementation"]

    # The constants block is the values a conforming port reproduces
    # exactly; read them back through the implementation.
    consts = data["constants"]
    assert consts["name_pattern"] == NAME_PATTERN.pattern
    assert tuple(consts["reserved_names"]) == RESERVED_NAMES
    topics = default_topics(consts["topics"]["prefix"])
    for key in (
        "service.started",
        "service.ready_reported",
        "service.failed",
        "service.stopped",
        "supervisor.ready_reported",
        "supervisor.stopped",
    ):
        assert topics[key] == consts["topics"][key], key
    for situation, row in consts["exit_codes"].items():
        assert code_for(situation) == row["code"], situation
        assert exit_code_for(situation) == row["exit"], situation
    assert tuple(consts["failure_policies"]) == ("fail-fast", "isolate")
    assert set(consts["signals"]) == {s.name for s in SHUTDOWN_SIGNALS}


def test_resolve_outcome_defaults_are_empty_not_shared():
    """Two outcomes must not share one list."""
    a, b = ResolveOutcome(), ResolveOutcome()
    a.selected.append("api")
    assert b.selected == []
