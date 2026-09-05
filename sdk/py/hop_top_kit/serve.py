"""
hop_top_kit.serve — serve hierarchy and service lifecycle.

The Python port of the contract in ``docs/contracts/serve-lifecycle.md``,
§"Cross-language parity".

``<tool> serve`` supervises every configured and enabled service;
``<tool> serve <service>`` selects exactly one and overrides aggregate
enablement. Both forms share one lifecycle implementation, so a service
started by the selector observes the same readiness, shutdown, and exit
semantics as the same service started by the supervisor.

The Go reference is ``go/console/serve`` and the closest sibling port is
``sdk/ts/src/serve.ts``. This module mirrors the behavior the contract
makes cross-language and deliberately does not mirror what the contract
rules out — there is no REST/OpenAPI projection, no socket service, no
command-tree reflection, and no permission/provenance/audit surface
here, because those depend on Go-only machinery.

Usage::

    from hop_top_kit.cli import create_app
    from hop_top_kit.serve import ServiceRegistry, register_serve

    registry = ServiceRegistry()
    registry.register(MyApiService())

    app, _theme = create_app(name="mytool", version="1.0.0", help="Does things")
    register_serve(app, ServeOptions(registry=registry,
                                     configs={"api": ServiceConfig(enabled=True)}))
"""

from __future__ import annotations

import asyncio
import contextlib
import re
import signal
import sys
import time
from collections.abc import Callable, Iterable, Mapping, Sequence
from dataclasses import dataclass, field
from typing import Any, Protocol, runtime_checkable

from hop_top_kit.output.error import (
    CODE_GENERIC,
    CODE_NOT_FOUND,
    CODE_OK,
    CODE_TRANSIENT,
    CODE_UNAUTHORIZED,
    CODE_USAGE,
    TRANSIENCE_PERMANENT,
    CLIError,
)

__all__ = [
    "ACTION_FAILED",
    "ACTION_READY_REPORTED",
    "ACTION_STARTED",
    "ACTION_STOPPED",
    "CODE_TRANSIENT",
    "DEFAULT_FAILURE_POLICY",
    "DEFAULT_READY_TIMEOUT",
    "DEFAULT_SHUTDOWN_TIMEOUT",
    "DEFAULT_STOP_TIMEOUT",
    "DEFAULT_TOPIC_PREFIX",
    "FAILURE_POLICIES",
    "NAME_PATTERN",
    "OBJECT_SERVICE",
    "OBJECT_SUPERVISOR",
    "RESERVED_NAMES",
    "SHUTDOWN_SIGNALS",
    "PolicyGate",
    "ResolveOutcome",
    "RunResult",
    "ServeOptions",
    "Service",
    "ServiceConfig",
    "ServiceRegistrationError",
    "ServiceRegistry",
    "SignalController",
    "Supervisor",
    "SupervisorConfig",
    "code_for",
    "default_topics",
    "exit_code_for",
    "is_failure",
    "is_failure_policy",
    "is_reserved_name",
    "register_serve",
    "resolve",
    "start_order",
    "validate_name",
    "worst_outcome",
]

# ---------------------------------------------------------------------------
# Naming
# ---------------------------------------------------------------------------

NAME_PATTERN = re.compile(r"^[a-z][a-z0-9-]*$")
"""Service identifier grammar: lowercase ASCII, digits, internal hyphens.

An identifier is a CLI word, a config key segment, and an event payload
value at once, which is why the grammar is contract rather than a local
convention.
"""

RESERVED_NAMES: tuple[str, ...] = ("all", "none", "list")
"""Names reserved for selector vocabulary.

Registering one would make ``serve <name>`` ambiguous with a future
aggregate form, and is why ``--list`` is a flag rather than a
``serve list`` child.
"""


def is_reserved_name(name: str) -> bool:
    """Report whether *name* is one of the reserved selector words."""
    return name in RESERVED_NAMES


def validate_name(name: str) -> str | None:
    """Validate a service identifier, returning a message or ``None``.

    Mirrors Go's ``serve.ValidateName`` and the TypeScript
    ``validateName``.
    """
    if not name:
        return "serve: service name is empty"
    if not NAME_PATTERN.match(name):
        return (
            f'serve: service name "{name}" must be lowercase letters, '
            "digits, or hyphens, starting with a letter"
        )
    if is_reserved_name(name):
        return f'serve: service name "{name}" is reserved'
    return None


# ---------------------------------------------------------------------------
# Registration seam
# ---------------------------------------------------------------------------


@runtime_checkable
class Service(Protocol):
    """One long-running thing a tool can serve.

    The four required capabilities are the contract's minimum: a name, a
    start that runs until cancelled or failed, a readiness report, and a
    stop. Go expresses them as an interface; here they are members of a
    Protocol, which the contract explicitly permits — what is fixed is
    the capability set and each one's behavior, not a method table.

    The optional declarations (``validate``, ``depends_on``, ``addr``,
    ``service_class``) are concepts a registration MAY carry; a service
    that omits one is treated as not declaring it. Python's structural
    typing means a plain object supplying only the four required members
    satisfies this Protocol.
    """

    name: str
    """Stable identifier. Must satisfy :func:`validate_name` and must not
    change across releases: renaming one is a breaking change to the
    command surface, the config file, and any subscriber."""

    async def start(self, cancel: asyncio.Event, ready: Callable[[], None]) -> None:
        """Begin serving.

        Returns when *cancel* is set (a clean stop) and raises when the
        service fails.

        ``ready`` must be called once, after every acquisition that can
        fail deterministically has succeeded — the listener bound, the
        file created, the subscription attached. Calling it more than
        once is ignored rather than an error; the supervisor reports
        ready at most once per start either way.
        """
        ...

    def ready(self) -> bool:
        """Whether the service is currently accepting work.

        Readiness, not liveness: a ready service may be idle, and may
        later fail.
        """
        ...

    async def stop(self) -> None:
        """Drain in-flight work and release resources.

        The supervisor bounds this by the stop timeout and abandons a
        stop that exceeds it, so an implementation must not assume it
        will be allowed to finish.
        """
        ...


class PolicyGate(Protocol):
    """The third validation gate.

    A service whose declared class the gate denies is refused at
    ``UNAUTHORIZED``, exit 5.

    The contract requires the gate, not Go's YAML-driven
    ``side_effect × network`` table: a two-argument predicate satisfies
    it. A registry with no gate passes every service, because a tool
    that has wired no policy has expressed no restriction.
    """

    def allow(self, side_effect: str, network: str) -> tuple[bool, str]:
        """Return ``(allowed, reason)`` for a declared class."""
        ...


class ServiceRegistrationError(Exception):
    """Raised when a registration is rejected at construction time.

    A duplicate name or an invalid one is a wiring bug in the tool's
    entry point, not a runtime condition: it surfaces on the first run
    rather than at the first ``serve``, and there is no last-writer-wins
    path. Go panics; raising is this port's equivalent. What the contract
    forbids is letting execution survive it as a warning.
    """


class ServiceRegistry:
    """The seam kit-owned and adopter-owned services register into.

    A tool builds one before the root command parses; the supervisor
    reads it.
    """

    def __init__(self) -> None:
        self._by_name: dict[str, Service] = {}
        self._order: list[str] = []

    def register(self, svc: Service) -> None:
        """Add *svc* under its name.

        Raises :class:`ServiceRegistrationError` on an invalid name and
        on a duplicate. An adopter deliberately replacing a kit-shipped
        service calls :meth:`override` instead — the documented escape
        hatch, and the only path that accepts a duplicate name.
        """
        invalid = validate_name(svc.name)
        if invalid:
            raise ServiceRegistrationError(invalid)
        if svc.name in self._by_name:
            raise ServiceRegistrationError(
                f'serve: service "{svc.name}" already registered (use override to replace)'
            )
        self._by_name[svc.name] = svc
        self._order.append(svc.name)

    def override(self, svc: Service) -> None:
        """Register *svc*, replacing any service already under its name.

        Keeps that name's original position in :meth:`list`. Still raises
        on an invalid name: override lifts the collision rule, not the
        grammar.
        """
        invalid = validate_name(svc.name)
        if invalid:
            raise ServiceRegistrationError(invalid)
        if svc.name not in self._by_name:
            self._order.append(svc.name)
        self._by_name[svc.name] = svc

    def lookup(self, name: str) -> Service | None:
        """The service registered under *name*, if any."""
        return self._by_name.get(name)

    def names(self) -> list[str]:
        """Every registered identifier, in registration order."""
        return list(self._order)

    def list(self) -> list[Service]:
        """Every registered service, in registration order."""
        return [self._by_name[n] for n in self._order]

    def __len__(self) -> int:
        return len(self._by_name)


# ---------------------------------------------------------------------------
# Optional declaration accessors
# ---------------------------------------------------------------------------
#
# The contract requires the optional declarations as *concepts* a
# registration MAY carry. Python's structural typing gives no way to ask
# a Protocol "did you implement this optional member", so each accessor
# reads the attribute defensively: a service that never declared one is
# indistinguishable from one that declared nothing, which is the
# behavior the contract asks for.


def _validate_of(svc: Service) -> str | None:
    """The service's own config gate, or ``None`` when it declares none."""
    fn = getattr(svc, "validate", None)
    if fn is None:
        return None
    return fn()


def _depends_on_of(svc: Service) -> list[str]:
    """The service's dependency list, or empty when it declares none."""
    fn = getattr(svc, "depends_on", None)
    if fn is None:
        return []
    return list(fn())


def _addr_of(svc: Service) -> str | None:
    """The service's resolved address, or ``None`` when it has none."""
    fn = getattr(svc, "addr", None)
    if fn is None:
        return None
    got = fn()
    return got or None


def _class_of(svc: Service) -> tuple[str, str] | None:
    """The service's ``(side_effect, network)`` class, or ``None``."""
    fn = getattr(svc, "service_class", None)
    if fn is None:
        return None
    side_effect, network = fn()
    return side_effect, network


# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------

DEFAULT_READY_TIMEOUT = 30.0
"""``services.<name>.ready_timeout`` default, seconds."""
DEFAULT_STOP_TIMEOUT = 30.0
"""``services.<name>.stop_timeout`` default, seconds."""
DEFAULT_SHUTDOWN_TIMEOUT = 60.0
"""``services.shutdown_timeout`` default, seconds."""

DEFAULT_FAILURE_POLICY = "fail-fast"
"""``services.failure_policy`` default."""

FAILURE_POLICIES: tuple[str, ...] = ("fail-fast", "isolate")
"""The two declared values of ``services.failure_policy``."""


def is_failure_policy(p: str) -> bool:
    """Report whether *p* is a declared failure policy."""
    return p in FAILURE_POLICIES


@dataclass
class ServiceConfig:
    """The resolved ``services.<name>`` block for one service.

    Only the lifecycle keys are modeled; service-specific keys live in
    the same block and are read by the service itself.
    """

    enabled: bool = False
    """``services.<name>.enabled``. Decides whether the supervisor form
    starts this service. Defaults to ``False``: a service that starts
    listening because a dependency upgrade added it to the registry is an
    unrequested open port."""

    ready_timeout: float = DEFAULT_READY_TIMEOUT
    """``services.<name>.ready_timeout``, seconds."""

    stop_timeout: float = DEFAULT_STOP_TIMEOUT
    """``services.<name>.stop_timeout``, seconds."""


@dataclass
class SupervisorConfig:
    """The supervisor-scoped half of the ``services`` block."""

    failure_policy: str = DEFAULT_FAILURE_POLICY
    """``services.failure_policy``: ``fail-fast`` or ``isolate``."""

    shutdown_timeout: float = DEFAULT_SHUTDOWN_TIMEOUT
    """``services.shutdown_timeout``, seconds."""


#: The ``services.*`` key names this module reads. They are contract: a
#: single YAML file is often read by a fleet of tools that are not all
#: the same language, so the names travel even where the resolution
#: machinery does not.
CONFIG_KEY_ENABLED = "enabled"
CONFIG_KEY_READY_TIMEOUT = "ready_timeout"
CONFIG_KEY_STOP_TIMEOUT = "stop_timeout"
CONFIG_KEY_FAILURE_POLICY = "failure_policy"
CONFIG_KEY_SHUTDOWN_TIMEOUT = "shutdown_timeout"
CONFIG_BLOCK = "services"


def configs_from_mapping(
    block: Mapping[str, Any] | None,
) -> tuple[dict[str, ServiceConfig], SupervisorConfig]:
    """Read a resolved ``services`` block into typed configuration.

    The contract fixes the key *names* and their defaults, not how a port
    resolves them. This reads the names out of whatever mapping the
    caller's own config layer produced — :mod:`hop_top_kit.config` merges
    whole YAML documents rather than resolving dotted keys, and the
    contract explicitly accepts that.

    The two supervisor-scoped keys live beside the per-service blocks, so
    they are recognized by name and never mistaken for a service.
    """
    per_service: dict[str, ServiceConfig] = {}
    supervisor = SupervisorConfig()
    if not block:
        return per_service, supervisor

    for key, value in block.items():
        if key == CONFIG_KEY_FAILURE_POLICY:
            if isinstance(value, str) and is_failure_policy(value):
                supervisor.failure_policy = value
            continue
        if key == CONFIG_KEY_SHUTDOWN_TIMEOUT:
            seconds = _as_seconds(value)
            if seconds is not None:
                supervisor.shutdown_timeout = seconds
            continue
        if not isinstance(value, Mapping):
            continue
        cfg = ServiceConfig()
        # `enabled` is true only when it resolves to the boolean True.
        # A truthy string or a 1 is a config the operator did not mean to
        # write, and an unrequested open port is the risk the default
        # guards against.
        cfg.enabled = value.get(CONFIG_KEY_ENABLED) is True
        ready = _as_seconds(value.get(CONFIG_KEY_READY_TIMEOUT))
        if ready is not None:
            cfg.ready_timeout = ready
        stop = _as_seconds(value.get(CONFIG_KEY_STOP_TIMEOUT))
        if stop is not None:
            cfg.stop_timeout = stop
        per_service[key] = cfg

    return per_service, supervisor


_DURATION = re.compile(r"^\s*(\d+(?:\.\d+)?)\s*(ms|s|m|h)?\s*$")
_DURATION_SCALE = {"ms": 0.001, "s": 1.0, "m": 60.0, "h": 3600.0, None: 1.0}


def _as_seconds(value: Any) -> float | None:
    """Read a duration key as seconds, or ``None`` when it is absent.

    Accepts a bare number (seconds) and the ``30s`` / ``500ms`` spelling
    the contract's defaults are written in. An unparseable value yields
    ``None`` so the default stands rather than a zero budget, which would
    turn a typo into an instant timeout.
    """
    if value is None or isinstance(value, bool):
        return None
    if isinstance(value, int | float):
        return float(value)
    if isinstance(value, str):
        m = _DURATION.match(value)
        if not m:
            return None
        return float(m.group(1)) * _DURATION_SCALE[m.group(2)]
    return None


# ---------------------------------------------------------------------------
# Outcomes and exit codes
# ---------------------------------------------------------------------------

#: The kinds of ending a serve run can have.
OUTCOME_CLEAN_STOP = "clean-stop"
OUTCOME_INVALID_SELECTION = "invalid-selection"
OUTCOME_CONFIG_INVALID = "config-invalid"
OUTCOME_NO_SERVICES = "no-services"
OUTCOME_UNKNOWN_SERVICE = "unknown-service"
OUTCOME_POLICY_DENIED = "policy-denied"
OUTCOME_START_FAILED = "start-failed"
OUTCOME_RUNTIME_CRASH = "runtime-crash"
OUTCOME_SHUTDOWN_TIMEOUT = "shutdown-timeout"

_EXIT_TABLE: dict[str, tuple[str, int]] = {
    OUTCOME_CLEAN_STOP: (CODE_OK, 0),
    OUTCOME_INVALID_SELECTION: (CODE_USAGE, 2),
    OUTCOME_CONFIG_INVALID: (CODE_USAGE, 2),
    OUTCOME_NO_SERVICES: (CODE_USAGE, 2),
    OUTCOME_UNKNOWN_SERVICE: (CODE_NOT_FOUND, 3),
    OUTCOME_POLICY_DENIED: (CODE_UNAUTHORIZED, 5),
    OUTCOME_START_FAILED: (CODE_GENERIC, 1),
    OUTCOME_RUNTIME_CRASH: (CODE_GENERIC, 1),
    OUTCOME_SHUTDOWN_TIMEOUT: (CODE_GENERIC, 1),
}
"""The contract's exit-behavior table, verbatim.

Codes come from the shared taxonomy in :mod:`hop_top_kit.output.error`;
this module allocates no new numbers.

``start-failed`` and ``runtime-crash`` share exit 1 deliberately: they
differ in *when*, not in what an operator does next, and the
distinguishing detail belongs in the message and the failed event rather
than in a second numeric code.
"""


def exit_code_for(outcome: str) -> int:
    """Process exit code for *outcome*.

    An outcome with no table row is treated as a generic failure rather
    than a success, so a kind added without a row fails loudly instead of
    silently exiting 0.
    """
    return _EXIT_TABLE.get(outcome, (CODE_GENERIC, 1))[1]


def code_for(outcome: str) -> str:
    """The ``CODE_*`` string for *outcome*, for the rendered envelope."""
    return _EXIT_TABLE.get(outcome, (CODE_GENERIC, 1))[0]


def is_failure(outcome: str) -> bool:
    """Whether *outcome* exits non-zero."""
    return exit_code_for(outcome) != 0


def _refusal(outcome: str, message: str) -> CLIError:
    """Build the envelope for *outcome*, reading its code from the table.

    Every refusal this module raises goes through here rather than
    through the per-code constructors in :mod:`hop_top_kit.output.error`.
    Those hardcode their own exit codes, so calling them would leave two
    independent sources of truth for one contract value — and a table
    edited without them would ship a refusal whose exit code no longer
    matched the row that documents it.
    """
    code, exit_code = _EXIT_TABLE.get(outcome, (CODE_GENERIC, 1))
    return CLIError(
        code=code,
        message=message,
        exit_code=exit_code,
        transience=TRANSIENCE_PERMANENT,
    )


def worst_outcome(observed: Sequence[str]) -> str:
    """The outcome the process should exit on given everything observed.

    "Worst" is severity, not exit-code magnitude: any failure beats a
    clean stop, and among failures the first observed wins, because it is
    the one that explains the rest. Under ``isolate`` a process may
    survive several failures, and the exit code must reflect the worst
    outcome across the whole run rather than the last one.
    """
    worst = OUTCOME_CLEAN_STOP
    for o in observed:
        if is_failure(o) and not is_failure(worst):
            worst = o
    return worst


# ---------------------------------------------------------------------------
# Resolution — the hierarchy and the override rule
# ---------------------------------------------------------------------------


@dataclass
class ResolveOutcome:
    """The result of resolving a ``serve`` invocation against a registry."""

    selected: list[str] = field(default_factory=list)
    """Identifiers to run, in registration order. Empty when *error* is set."""

    explicit: bool = False
    """True when the selector form was used. Under it *selected* holds
    exactly one name and aggregate enablement was overridden rather than
    consulted."""

    skipped: list[str] = field(default_factory=list)
    """Configured-but-disabled services the supervisor form passed over.
    Skipping is not an error and must not affect the exit code."""

    error: CLIError | None = None
    """The refusal, already carrying its code and exit code."""

    outcome: str | None = None
    """The outcome the refusal corresponds to."""


def resolve(
    registry: ServiceRegistry,
    args: Sequence[str],
    *,
    configs: Mapping[str, ServiceConfig] | None = None,
    policy: PolicyGate | None = None,
) -> ResolveOutcome:
    """Turn a ``serve`` invocation into a runnable set.

    Pure: nothing is started, nothing binds, nothing is written.

    Selector form runs the named service **even when
    ``services.<name>.enabled`` is false**, provided all three gates pass
    in order — registration, then configuration, then policy. Enablement
    is not a gate there: an operator naming a service on the command line
    has already made the decision the flag exists to automate.

    Supervisor form runs every service that is both configured and
    enabled, in registration order, skipping a disabled one silently.
    Resolving to zero services is a usage error, not a clean exit: a
    process that exits 0 without listening is indistinguishable from a
    successful start to systemd or a container runtime.
    """
    if len(args) > 1:
        return ResolveOutcome(
            outcome=OUTCOME_INVALID_SELECTION,
            error=_refusal(
                OUTCOME_INVALID_SELECTION,
                f"serve accepts at most one service name, got {len(args)}",
            ),
        )
    if len(args) == 1:
        return _resolve_explicit(registry, args[0], policy)
    return _resolve_aggregate(registry, configs or {}, policy)


def _resolve_explicit(
    registry: ServiceRegistry,
    name: str,
    policy: PolicyGate | None,
) -> ResolveOutcome:
    """The selector form and its override rule."""
    # Gate 1: registration.
    svc = registry.lookup(name)
    if svc is None:
        known = registry.names()
        err = _refusal(
            OUTCOME_UNKNOWN_SERVICE,
            f'unknown service "{name}"; known: {", ".join(known)}',
        )
        fix = _nearest_name(name, known)
        if fix:
            err.suggested_fix = f'did you mean "{fix}"?'
        return ResolveOutcome(explicit=True, outcome=OUTCOME_UNKNOWN_SERVICE, error=err)

    # Gate 2: configuration.
    invalid = _validate_of(svc)
    if invalid:
        return ResolveOutcome(
            explicit=True,
            outcome=OUTCOME_CONFIG_INVALID,
            error=_refusal(OUTCOME_CONFIG_INVALID, f'service "{name}": {invalid}'),
        )

    # Gate 3: policy.
    denied = _check_policy(policy, svc)
    if denied is not None:
        return ResolveOutcome(explicit=True, outcome=OUTCOME_POLICY_DENIED, error=denied)

    # Enablement is deliberately not consulted here.
    return ResolveOutcome(selected=[name], explicit=True)


def _resolve_aggregate(
    registry: ServiceRegistry,
    configs: Mapping[str, ServiceConfig],
    policy: PolicyGate | None,
) -> ResolveOutcome:
    """The supervisor form."""
    out = ResolveOutcome()

    for name in registry.names():
        cfg = configs.get(name)
        if cfg is None:
            continue  # not configured
        if cfg.enabled is not True:
            out.skipped.append(name)
            continue
        svc = registry.lookup(name)
        if svc is None:  # pragma: no cover - names() and lookup() cannot disagree
            continue

        invalid = _validate_of(svc)
        if invalid:
            return ResolveOutcome(
                skipped=out.skipped,
                outcome=OUTCOME_CONFIG_INVALID,
                error=_refusal(OUTCOME_CONFIG_INVALID, f'service "{name}": {invalid}'),
            )
        denied = _check_policy(policy, svc)
        if denied is not None:
            return ResolveOutcome(
                skipped=out.skipped,
                outcome=OUTCOME_POLICY_DENIED,
                error=denied,
            )
        out.selected.append(name)

    if not out.selected:
        err = _refusal(
            OUTCOME_NO_SERVICES,
            "no services configured and enabled; enable one under services.* "
            "or name one explicitly",
        )
        err.suggested_fix = "set services.<name>.enabled: true, or run: serve <service>"
        out.error = err
        out.outcome = OUTCOME_NO_SERVICES
    return out


def _check_policy(gate: PolicyGate | None, svc: Service) -> CLIError | None:
    """Apply the policy gate to a service that declares a class."""
    if gate is None:
        return None
    declared = _class_of(svc)
    if declared is None:
        return None
    side_effect, network = declared
    allowed, reason = gate.allow(side_effect, network)
    if allowed:
        return None
    msg = f'service "{svc.name}" denied by policy (side_effect={side_effect}, network={network})'
    if reason:
        msg += f": {reason}"
    return _refusal(OUTCOME_POLICY_DENIED, msg)


def _nearest_name(want: str, known: Iterable[str]) -> str | None:
    """The registered name closest to *want* by edit distance.

    Returns ``None`` when nothing is close enough to suggest. The
    threshold scales with the typed word's length so a short name does
    not attract an unrelated suggestion.

    The contract calls the suggestion a courtesy rather than an
    obligation — the refusal itself is what is contract. It is here
    because the refusal reads better with it, not because parity needs
    it.
    """
    best: str | None = None
    best_dist = -1
    limit = len(want) // 2 + 1
    for k in sorted(known):
        d = _edit_distance(want, k)
        if d > limit:
            continue
        if best_dist == -1 or d < best_dist:
            best, best_dist = k, d
    return best


def _edit_distance(a: str, b: str) -> int:
    prev = list(range(len(b) + 1))
    for i in range(1, len(a) + 1):
        cur = [i] + [0] * len(b)
        for j in range(1, len(b) + 1):
            cost = 0 if a[i - 1] == b[j - 1] else 1
            cur[j] = min(prev[j] + 1, cur[j - 1] + 1, prev[j - 1] + cost)
        prev = cur
    return prev[len(b)]


# ---------------------------------------------------------------------------
# Ordering
# ---------------------------------------------------------------------------


def start_order(registry: ServiceRegistry, selected: Sequence[str]) -> list[str]:
    """*selected* in topological order over the optional dependencies.

    Ties are broken by the order in *selected*, which :func:`resolve`
    returns in registration order.

    A dependency naming a service outside *selected* is ignored rather
    than an error: under the selector form exactly one service runs, and
    its dependencies are the operator's business, not a reason to refuse
    a deliberate single-service start.

    A dependency cycle raises, in the same class as a name collision: it
    is a wiring bug that can only be fixed by editing the registrations,
    and there is no order the supervisor could pick that would be right.
    """
    in_set = set(selected)
    deps: dict[str, list[str]] = {}
    for name in selected:
        svc = registry.lookup(name)
        if svc is None:  # pragma: no cover - selected comes from the registry
            continue
        want = [d for d in _depends_on_of(svc) if d in in_set and d != name]
        if want:
            deps[name] = want

    WHITE, GREY, BLACK = 0, 1, 2
    mark: dict[str, int] = {}
    out: list[str] = []

    def visit(name: str, path: list[str]) -> None:
        state = mark.get(name, WHITE)
        if state == BLACK:
            return
        if state == GREY:
            cycle = " -> ".join([*path, name])
            raise ServiceRegistrationError(f"serve: dependency cycle: {cycle}")
        mark[name] = GREY
        for want in deps.get(name, []):
            visit(want, [*path, name])
        mark[name] = BLACK
        out.append(name)

    for name in selected:
        visit(name, [])
    return out


# ---------------------------------------------------------------------------
# Lifecycle events
# ---------------------------------------------------------------------------

DEFAULT_TOPIC_PREFIX = "kit.serve"
"""The 2-segment source.category prefix serve events publish under."""

ACTION_STARTED = "started"
ACTION_READY_REPORTED = "ready_reported"
ACTION_FAILED = "failed"
ACTION_STOPPED = "stopped"
"""Action segments.

A bare ``ready`` fails the past-tense validation the bus applies and
would publish a topic Go subscribers reject; do not use one.
"""

OBJECT_SERVICE = "service"
OBJECT_SUPERVISOR = "supervisor"
"""Object segments.

The service identifier travels in the payload, not the topic, so
subscribers are not forced to re-bind when a tool gains a service.
"""

_TOPIC_PAIRS: tuple[tuple[str, str], ...] = (
    (OBJECT_SERVICE, ACTION_STARTED),
    (OBJECT_SERVICE, ACTION_READY_REPORTED),
    (OBJECT_SERVICE, ACTION_FAILED),
    (OBJECT_SERVICE, ACTION_STOPPED),
    (OBJECT_SUPERVISOR, ACTION_READY_REPORTED),
    (OBJECT_SUPERVISOR, ACTION_STOPPED),
)


def default_topics(prefix: str = DEFAULT_TOPIC_PREFIX) -> dict[str, str]:
    """The conformant topic set for *prefix*, keyed ``<object>.<action>``.

    These strings are contract: a subscriber is written against them and
    does not know which language published.
    """
    p = prefix or DEFAULT_TOPIC_PREFIX
    return {f"{obj}.{act}": f"{p}.{obj}.{act}" for obj, act in _TOPIC_PAIRS}


class _Publisher(Protocol):
    """The narrow slice of a bus the supervisor needs.

    :class:`hop_top_kit.bus.Bus` satisfies it without an adapter.
    Omitting one means events are not published; the log counterpart
    still runs, so a tool with no bus still produces an operator-legible
    startup trace.
    """

    def publish(self, event: Any) -> None: ...


class _ServeLogger(Protocol):
    """The narrow slice of a structured logger the supervisor needs.

    It matches what :func:`hop_top_kit.log.create_logger` returns, so a
    kit logger satisfies it without an adapter.
    """

    def info(self, msg: str, **fields: Any) -> None: ...

    def error(self, msg: str, **fields: Any) -> None: ...


class _Emitter:
    """Publishes one lifecycle transition to both sinks."""

    def __init__(
        self,
        topics: Mapping[str, str],
        source: str,
        publisher: _Publisher | None,
        logger: _ServeLogger | None,
    ) -> None:
        self._topics = dict(topics)
        self._source = source
        self._pub = publisher
        self._log = logger

    def emit(self, obj: str, action: str, payload: dict[str, Any]) -> None:
        self._log_event(obj, action, payload)
        if self._pub is None:
            return
        topic = self._topics.get(f"{obj}.{action}")
        if not topic:
            return
        try:
            from hop_top_kit.bus import create_event

            self._pub.publish(create_event(topic, self._source, payload))
        except Exception:
            # An event sink is observability, not correctness: a publish
            # failure never fails the lifecycle.
            pass

    def _log_event(self, obj: str, action: str, payload: Mapping[str, Any]) -> None:
        """Emit the log counterpart with structured fields, never text.

        The identifier and the address are fields rather than words
        interpolated into the message: that is what makes a startup trace
        greppable across a fleet whose tools are not all the same
        language.
        """
        if self._log is None:
            return
        fields: dict[str, Any] = {"object": obj}
        for key in ("service", "address", "reason", "elapsed_ms"):
            if payload.get(key) is not None:
                fields[key] = payload[key]
        if action == ACTION_FAILED:
            if payload.get("error") is not None:
                fields["error"] = payload["error"]
            self._log.error(f"serve: {action}", **fields)
            return
        self._log.info(f"serve: {action}", **fields)


# ---------------------------------------------------------------------------
# Supervisor
# ---------------------------------------------------------------------------


@dataclass
class RunResult:
    """What one supervised run produced."""

    outcome: str = OUTCOME_CLEAN_STOP
    """The worst outcome observed, which is what the process exits on."""

    error: CLIError | None = None
    """The rendered failure carrying code and exit code; absent on a clean stop."""

    started: list[str] = field(default_factory=list)
    """Identifiers whose start was invoked, in invocation order."""

    ready: list[str] = field(default_factory=list)
    """Identifiers that reported ready, in report order."""

    failed: dict[str, str] = field(default_factory=dict)
    """Identifier to the error it failed with."""

    exit_code: int = 0
    """The process exit code for this run."""


class _RunState:
    """The mutable half of one run, kept off the Supervisor.

    A Supervisor holds no run state of its own, so two can run
    concurrently in one process — which is exactly what a test does, and
    what the contract's re-execution rule requires.
    """

    def __init__(self, now: Callable[[], float]) -> None:
        self._now = now
        self._begin = now()
        self.started: list[str] = []
        self.ready: list[str] = []
        self.failed: dict[str, str] = {}
        self.observed: list[str] = []
        self.live: dict[str, asyncio.Task[None]] = {}
        self._stopped: set[str] = set()

    def elapsed_ms(self) -> int:
        return int((self._now() - self._begin) * 1000)

    def note_failure(self, name: str, error: str, outcome: str) -> None:
        self.failed[name] = error
        self.observed.append(outcome)

    def mark_stopped(self, name: str) -> bool:
        """Record that *name*'s stopped event fired; report whether it had.

        A service reports stopped once per run, whichever path noticed it
        first.
        """
        already = name in self._stopped
        self._stopped.add(name)
        return already


class Supervisor:
    """Runs a resolved set of services under one lifecycle.

    Ordered start, per-service readiness, policy-driven reaction to
    failure, and ordered stop bounded by the configured budgets.
    """

    def __init__(
        self,
        registry: ServiceRegistry,
        *,
        config: SupervisorConfig | None = None,
        publisher: _Publisher | None = None,
        logger: _ServeLogger | None = None,
        topics: Mapping[str, str] | None = None,
        event_source: str = DEFAULT_TOPIC_PREFIX,
        now: Callable[[], float] | None = None,
        escalate: asyncio.Event | None = None,
    ) -> None:
        cfg = config or SupervisorConfig()
        self._registry = registry
        self._failure_policy = (
            cfg.failure_policy if is_failure_policy(cfg.failure_policy) else DEFAULT_FAILURE_POLICY
        )
        self._shutdown_timeout = (
            cfg.shutdown_timeout if cfg.shutdown_timeout > 0 else DEFAULT_SHUTDOWN_TIMEOUT
        )
        self._emitter = _Emitter(
            topics or default_topics(),
            event_source,
            publisher,
            logger,
        )
        self._now = now or time.monotonic
        self._escalate = escalate

    async def run(
        self,
        cancel: asyncio.Event,
        selected: Sequence[str],
        configs: Mapping[str, ServiceConfig] | None = None,
    ) -> RunResult:
        """Start every service in *selected*, serve, then stop in order.

        The run ends when *cancel* is set (the clean path: a signal, or
        the caller's own shutdown), when a failure trips the failure
        policy, or when every started service has returned. Run always
        performs the ordered stop before returning, so a caller never has
        to clean up after it.

        *selected* is normally :attr:`ResolveOutcome.selected`; run does
        not re-resolve and does not consult enablement, because the
        decision the caller already made is the one to honor.

        Each call builds its own run controller from the *cancel* it was
        handed, so a second run on the same registry observes only its
        own cancellation — it neither ignores its own signal nor stops
        without serving.
        """
        cfgs = dict(configs or {})
        st = _RunState(self._now)

        if not selected:
            err = _refusal(
                OUTCOME_NO_SERVICES,
                "no services configured and enabled; enable one under services.* "
                "or name one explicitly",
            )
            return self._finish(st, OUTCOME_NO_SERVICES, err)

        order = start_order(self._registry, selected)

        # The run controller is the caller's cancellation plus a stop the
        # supervisor itself trips when the failure policy says to bring
        # everything down. Cancelling it sets one event every service is
        # already waiting on, so every service observes cancellation at
        # the same instant; nothing is queued behind another drain.
        run_cancel = asyncio.Event()
        relay = asyncio.create_task(_relay(cancel, run_cancel))

        try:
            start_failed = await self._start_all(run_cancel, order, cfgs, st)
            if not start_failed:
                self._emit_aggregate_ready(st)
                await self._await_run(run_cancel, st)
            run_cancel.set()
            await self._stop_all(st, cfgs)
            return self._finish(st, worst_outcome(st.observed))
        finally:
            relay.cancel()
            with contextlib.suppress(asyncio.CancelledError):
                await relay
            await self._reap(st)

    async def _start_all(
        self,
        run_cancel: asyncio.Event,
        order: Sequence[str],
        configs: Mapping[str, ServiceConfig],
        st: _RunState,
    ) -> bool:
        """Start each service in order, waiting for each to report ready.

        Serial start is what makes a dependency declaration mean
        anything: a dependent must not begin acquiring before its
        dependency is accepting work. Returns True when a start failure
        short-circuits the sequence.
        """
        for name in order:
            svc = self._registry.lookup(name)
            if svc is None:  # pragma: no cover - order comes from the registry
                msg = f'service "{name}" disappeared from the registry'
                st.note_failure(name, msg, OUTCOME_START_FAILED)
                self._emit(
                    OBJECT_SERVICE,
                    ACTION_FAILED,
                    st,
                    service=name,
                    error=msg,
                    reason="unregistered",
                )
                return True

            ready_evt = asyncio.Event()
            st.started.append(name)
            self._emit(OBJECT_SERVICE, ACTION_STARTED, st, service=name)

            task = asyncio.create_task(svc.start(run_cancel, ready_evt.set), name=f"serve:{name}")
            st.live[name] = task

            if await self._await_ready(name, ready_evt, task, configs, st):
                return True
        return False

    async def _await_ready(
        self,
        name: str,
        ready_evt: asyncio.Event,
        task: asyncio.Task[None],
        configs: Mapping[str, ServiceConfig],
        st: _RunState,
    ) -> bool:
        """Block until *name* reports ready, fails, or exhausts its budget.

        A service that has not reported ready within the budget is a
        start failure.
        """
        budget = configs.get(name, ServiceConfig()).ready_timeout
        waiter = asyncio.create_task(ready_evt.wait())
        done, _pending = await asyncio.wait(
            {waiter, task},
            timeout=max(0.0, budget),
            return_when=asyncio.FIRST_COMPLETED,
        )

        if waiter in done and not waiter.cancelled():
            st.ready.append(name)
            self._emit(
                OBJECT_SERVICE,
                ACTION_READY_REPORTED,
                st,
                service=name,
                address=self._addr_for(name),
            )
            return False

        waiter.cancel()
        with contextlib.suppress(asyncio.CancelledError):
            await waiter

        if not done:
            msg = f"not ready within {budget}s"
            st.note_failure(name, msg, OUTCOME_START_FAILED)
            self._emit(
                OBJECT_SERVICE, ACTION_FAILED, st, service=name, error=msg, reason="ready_timeout"
            )
            return True

        # The service returned before reporting ready. That is a start
        # failure even when it returned cleanly: it was asked to serve
        # and it did not.
        st.live.pop(name, None)
        msg = _task_error(task) or "returned before reporting ready"
        st.note_failure(name, msg, OUTCOME_START_FAILED)
        self._emit(OBJECT_SERVICE, ACTION_FAILED, st, service=name, error=msg, reason="start")
        return True

    def _emit_aggregate_ready(self, st: _RunState) -> None:
        """Publish the supervisor readiness event once everything is ready.

        The aggregate is never reported before every started service has
        reported its own.
        """
        if st.started and len(st.ready) == len(st.started):
            self._emit(OBJECT_SUPERVISOR, ACTION_READY_REPORTED, st)

    async def _await_run(self, run_cancel: asyncio.Event, st: _RunState) -> None:
        """Block while the services run.

        Returns when the run is cancelled, when the failure policy trips,
        or when the last running service has exited.
        """
        cancelled = asyncio.create_task(run_cancel.wait())
        try:
            while st.live:
                done, _pending = await asyncio.wait(
                    {cancelled, *st.live.values()},
                    return_when=asyncio.FIRST_COMPLETED,
                )
                if cancelled in done:
                    return

                for task in done:
                    name = _name_of(st, task)
                    if name is None:  # pragma: no cover - live keys own the tasks
                        continue
                    st.live.pop(name, None)
                    err = _task_error(task)
                    if err is not None:
                        st.note_failure(name, err, OUTCOME_RUNTIME_CRASH)
                        self._emit(
                            OBJECT_SERVICE,
                            ACTION_FAILED,
                            st,
                            service=name,
                            error=err,
                            reason="runtime",
                        )
                        if self._failure_policy == "fail-fast":
                            run_cancel.set()
                            return
                        continue
                    # A clean return under isolate is not a failure of
                    # that service, but the process must not survive as
                    # an empty shell: when the last one is gone the run
                    # is over.
                    if not st.mark_stopped(name):
                        self._emit(OBJECT_SERVICE, ACTION_STOPPED, st, service=name)

            if st.failed and self._failure_policy == "isolate":
                st.observed.append(OUTCOME_RUNTIME_CRASH)
        finally:
            cancelled.cancel()
            with contextlib.suppress(asyncio.CancelledError):
                await cancelled

    async def _stop_all(self, st: _RunState, configs: Mapping[str, ServiceConfig]) -> None:
        """Stop in the exact reverse of the order services actually started.

        One at a time, so a dependent is always fully stopped before its
        dependency. Each stop is bounded by that service's budget; one
        that exceeds it is abandoned — logged, emitted as failed, and the
        supervisor proceeds to the next rather than blocking the whole
        shutdown on one straggler. Exceeding the total budget ends the
        sequence with ``shutdown-timeout``.
        """
        order = list(st.started)
        deadline = self._now() + self._shutdown_timeout

        for i in range(len(order) - 1, -1, -1):
            name = order[i]

            # A second signal aborts the drain: the remaining services
            # are abandoned and the run exits with the crash code.
            if self._escalate is not None and self._escalate.is_set():
                msg = "drain aborted by second signal"
                st.observed.append(OUTCOME_RUNTIME_CRASH)
                for abandoned in order[: i + 1]:
                    st.failed[abandoned] = msg
                    self._emit(
                        OBJECT_SERVICE,
                        ACTION_FAILED,
                        st,
                        service=abandoned,
                        error=msg,
                        reason="escalated",
                    )
                return

            if self._now() >= deadline:
                st.observed.append(OUTCOME_SHUTDOWN_TIMEOUT)
                self._emit(
                    OBJECT_SERVICE,
                    ACTION_FAILED,
                    st,
                    service=name,
                    error=f"shutdown budget {self._shutdown_timeout}s exhausted before stopping",
                    reason="shutdown_timeout",
                )
                continue

            svc = self._registry.lookup(name)
            if svc is None:  # pragma: no cover - started names come from the registry
                continue

            budget = min(
                configs.get(name, ServiceConfig()).stop_timeout,
                max(0.0, deadline - self._now()),
            )
            await self._stop_one(svc, name, budget, deadline, st)

    async def _stop_one(
        self,
        svc: Service,
        name: str,
        budget: float,
        deadline: float,
        st: _RunState,
    ) -> None:
        """Bound one stop by its budget and by whatever remains of the total."""
        task = asyncio.create_task(svc.stop(), name=f"serve:stop:{name}")
        try:
            await asyncio.wait_for(asyncio.shield(task), timeout=max(0.0, budget))
        except TimeoutError:
            # Abandoned, not awaited: the task is left to settle on its
            # own so one straggler cannot hold the whole shutdown. The
            # shield keeps wait_for from cancelling it out from under a
            # service that is still draining.
            over_total = self._now() >= deadline
            msg = f"stop exceeded {budget}s"
            st.note_failure(
                name,
                msg,
                OUTCOME_SHUTDOWN_TIMEOUT if over_total else OUTCOME_RUNTIME_CRASH,
            )
            self._emit(
                OBJECT_SERVICE,
                ACTION_FAILED,
                st,
                service=name,
                error=msg,
                reason="shutdown_timeout" if over_total else "stop_timeout",
            )
            return
        except Exception as e:  # a stop may raise anything the service defines
            st.note_failure(name, _err_text(e), OUTCOME_RUNTIME_CRASH)
            self._emit(
                OBJECT_SERVICE, ACTION_FAILED, st, service=name, error=_err_text(e), reason="stop"
            )
            return

        # A service that returned on its own already reported stopped
        # when it did; stop released its resources, and the event is not
        # repeated — one stopped per service per run.
        if st.mark_stopped(name):
            return
        self._emit(OBJECT_SERVICE, ACTION_STOPPED, st, service=name)

    async def _reap(self, st: _RunState) -> None:
        """Cancel and await anything still live, so no task outlives a run.

        A start task that never observed cancellation would otherwise
        become a "task was destroyed but it is pending" warning, and in a
        long-lived process a leak.
        """
        for task in list(st.live.values()):
            task.cancel()
        for task in list(st.live.values()):
            with contextlib.suppress(asyncio.CancelledError, Exception):
                await task
        st.live.clear()

    def _addr_for(self, name: str) -> str | None:
        svc = self._registry.lookup(name)
        return None if svc is None else _addr_of(svc)

    def _emit(self, obj: str, action: str, st: _RunState, **payload: Any) -> None:
        """Emit one transition, dropping keys the service did not supply.

        The payload carries ``service``, ``error``, and ``address``
        because those three are contract; ``elapsed_ms`` rides along
        because it is cheap, and nothing downstream is specified to read
        it.
        """
        body: dict[str, Any] = {k: v for k, v in payload.items() if v is not None}
        body["elapsed_ms"] = st.elapsed_ms()
        self._emitter.emit(obj, action, body)

    def _finish(self, st: _RunState, outcome: str, err: CLIError | None = None) -> RunResult:
        """Assemble the result from everything the run observed."""
        error = err or (_failure_error(outcome, st.failed) if is_failure(outcome) else None)
        self._emit(OBJECT_SUPERVISOR, ACTION_STOPPED, st, reason=outcome)
        return RunResult(
            outcome=outcome,
            error=error,
            started=list(st.started),
            ready=list(st.ready),
            failed=dict(st.failed),
            exit_code=exit_code_for(outcome),
        )


async def _relay(source: asyncio.Event, target: asyncio.Event) -> None:
    """Set *target* when *source* fires, so one wait covers both."""
    await source.wait()
    target.set()


def _name_of(st: _RunState, task: asyncio.Task[None]) -> str | None:
    for name, t in st.live.items():
        if t is task:
            return name
    return None


def _task_error(task: asyncio.Task[None]) -> str | None:
    """The failure text of a settled task, or ``None`` when it returned."""
    if task.cancelled():
        return None
    exc = task.exception()
    return None if exc is None else _err_text(exc)


def _err_text(e: BaseException) -> str:
    return str(e) or e.__class__.__name__


def _failure_error(outcome: str, failed: Mapping[str, str]) -> CLIError:
    """Render the outcome as the envelope the command layer returns.

    Carries the contract's code and exit code. A failure wrapping a kit
    transient error keeps exit 6 unchanged, so agents and retry wrappers
    keep their existing branch.
    """
    if outcome == OUTCOME_START_FAILED:
        msg = "service failed to start"
    elif outcome == OUTCOME_SHUTDOWN_TIMEOUT:
        msg = "shutdown budget exceeded"
    else:
        msg = "service failed"
    for i, name in enumerate(sorted(failed)):
        msg += f"{': ' if i == 0 else '; '}{name}: {failed[name]}"
    return _refusal(outcome, msg)


# ---------------------------------------------------------------------------
# Signals
# ---------------------------------------------------------------------------

SHUTDOWN_SIGNALS: tuple[signal.Signals, ...] = (signal.SIGINT, signal.SIGTERM)
"""The signals the supervisor listens for. SIGKILL is not catchable."""


class SignalController:
    """A signal handler pair for the graceful drain and its escalation.

    :attr:`cancel` fires on the first SIGINT/SIGTERM and :attr:`escalate`
    on a second of either kind. The first signal begins graceful
    shutdown; a second aborts the drain, so an operator can escalate
    without reaching for SIGKILL. :meth:`stop` restores the previous
    handlers and must be called, or they outlive the run.

    On a platform with no SIGTERM to catch, installation of that one is
    skipped and the process degrades to the single graceful path rather
    than inventing a different escalation — which is the one place the
    contract says a port may be genuinely unable to conform.
    """

    def __init__(self) -> None:
        self.cancel = asyncio.Event()
        self.escalate = asyncio.Event()
        self._count = 0
        self._loop = asyncio.get_running_loop()
        self._installed: list[signal.Signals] = []
        self._stopped = False

        for sig in SHUTDOWN_SIGNALS:
            try:
                self._loop.add_signal_handler(sig, self._on_signal)
            except (NotImplementedError, RuntimeError, ValueError, AttributeError):
                # No such signal on this platform, or a loop that cannot
                # install handlers. Degrade rather than refuse to serve.
                continue
            self._installed.append(sig)

    def _on_signal(self) -> None:
        self._count += 1
        if self._count == 1:
            self.cancel.set()
        else:
            self.escalate.set()

    def stop(self) -> None:
        """Remove the handlers this controller installed. Idempotent."""
        if self._stopped:
            return
        self._stopped = True
        for sig in self._installed:
            with contextlib.suppress(NotImplementedError, RuntimeError, ValueError):
                self._loop.remove_signal_handler(sig)
        self._installed = []


# ---------------------------------------------------------------------------
# Command wiring
# ---------------------------------------------------------------------------


@dataclass
class ServeOptions:
    """Everything :func:`register_serve` needs to mount the command."""

    registry: ServiceRegistry
    """The seam services were registered into."""

    configs: Mapping[str, ServiceConfig] = field(default_factory=dict)
    """Resolved ``services.<name>`` blocks, keyed by identifier."""

    config: SupervisorConfig = field(default_factory=SupervisorConfig)
    """The supervisor-scoped half of the ``services`` block."""

    policy: PolicyGate | None = None
    """The third validation gate. ``None`` passes every service."""

    publisher: _Publisher | None = None
    """A bus. Omitting one leaves the log as the only lifecycle sink."""

    logger: _ServeLogger | None = None
    """A structured logger. Omitting one leaves the bus as the only sink."""

    stdout: Any = None
    """Where ``--list`` writes. Defaults to :data:`sys.stdout`."""

    on_exit: Callable[[int, CLIError | None], None] | None = None
    """Called with the run's exit code instead of exiting the process.

    Tests supply one; production omits it and the command raises the
    Typer exit the root's own machinery turns into a status.
    """


def register_serve(app: Any, opts: ServeOptions) -> None:
    """Mount the kit-owned ``serve`` command on a Typer *app*.

    With no positional argument it is the supervisor over every
    configured and enabled service; with exactly one it is the selector,
    which overrides aggregate enablement. Two or more is a usage error at
    exit 2 — the arity refusal is owned here rather than left to Click,
    whose own excess-argument error exits 2 with a different envelope and
    would not carry the contract's message.

    The inspection form is the ``--list`` flag, not a ``list`` child:
    ``list`` is reserved selector vocabulary, so a ``serve list`` child
    would be indistinguishable from the selector form naming a service
    called ``list``.
    """
    import typer

    @app.command("serve")
    def _serve(
        # typer.Argument / typer.Option in a default is Typer's own idiom:
        # the call builds the parameter spec Typer introspects, and moving
        # it into the body would leave the command with no operand at all.
        services: list[str] | None = typer.Argument(  # noqa: B008
            None, help="Service to run; omit to run every enabled service"
        ),
        list_: bool = typer.Option(
            False, "--list", help="List registered services and their state"
        ),
    ) -> None:
        """Run configured services under one lifecycle."""
        if list_:
            _run_list(opts)
            return
        _run_serve(list(services or []), opts)


def _run_list(opts: ServeOptions) -> None:
    """Print the registered services with their state, in registration order.

    The columns are not contract — a port renders them through its own
    output layer — but the ordering is.
    """
    w = opts.stdout or sys.stdout
    for svc in opts.registry.list():
        cfg = opts.configs.get(svc.name)
        configured = cfg is not None
        enabled = cfg is not None and cfg.enabled
        w.write(f"{svc.name:<20} {configured!s:<11} {enabled!s:<8} {svc.ready()!s}\n")


def _run_serve(args: Sequence[str], opts: ServeOptions) -> None:
    """Resolve the invocation and run the resulting set."""
    outcome = resolve(opts.registry, args, configs=opts.configs, policy=opts.policy)
    if outcome.error is not None:
        _report(opts, exit_code_for(outcome.outcome or OUTCOME_CONFIG_INVALID), outcome.error)
        return

    result = asyncio.run(_serve_async(outcome.selected, opts))
    _report(opts, result.exit_code, result.error)


async def _serve_async(selected: Sequence[str], opts: ServeOptions) -> RunResult:
    """Own the signals for one run and drive the supervisor.

    The first signal begins the drain, a second aborts it. The controller
    is built inside the loop that will run the supervisor, because a
    signal handler belongs to the loop that installed it.
    """
    sig = SignalController()
    try:
        sup = Supervisor(
            opts.registry,
            config=opts.config,
            publisher=opts.publisher,
            logger=opts.logger,
            escalate=sig.escalate,
        )
        return await sup.run(sig.cancel, selected, opts.configs)
    finally:
        sig.stop()


def _report(opts: ServeOptions, code: int, error: CLIError | None) -> None:
    """Route the run's outcome onto the shared exit-code taxonomy."""
    if opts.on_exit is not None:
        opts.on_exit(code, error)
        return

    import typer

    if error is not None:
        sys.stderr.write(f"{error}\n")
        if error.suggested_fix:
            sys.stderr.write(f"Fix: {error.suggested_fix}\n")
    raise typer.Exit(code)
