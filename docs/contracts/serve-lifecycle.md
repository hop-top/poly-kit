# Serve Hierarchy and Service Lifecycle

> `<tool> serve` supervises every configured and enabled service;
> `<tool> serve <service>` selects exactly one.
> Authority: [`go/console/serve`](../../go/console/serve/).

A kit-based tool exposes long-running work as **services**: an HTTP
API, a local socket, an MCP stdio channel, an RPC listener, a bus
consumer. This page is the normative contract for how those services
are named, registered, selected, started, reported ready, stopped, and
turned into a process exit code. It is transport-agnostic and
domain-agnostic: nothing here names an adopter's business objects.

The Go types that encode this contract live in
[`go/console/serve`](../../go/console/serve/). This document is the
authority for behavior; the package is the authority for signatures.

## Command hierarchy

| Invocation              | Role       | Runs                                                      |
|-------------------------|------------|-----------------------------------------------------------|
| `<tool> serve`          | supervisor | every service that is **configured** AND **enabled**       |
| `<tool> serve <service>`| selector   | exactly the named service, subject to validation           |
| `<tool> serve --list`   | inspection | registered services with configured / enabled / ready state|

Rules:

- `<tool> serve` MUST resolve its service set from configuration
  only. It MUST NOT take a positional service argument.
- `<tool> serve <service>` MUST accept exactly one positional
  argument. Two or more positional arguments is a usage error.
- The inspection form is the flag `--list`, not a `list` child.
  `list` is reserved selector vocabulary and cannot be registered as a
  service, so a `serve list` child would be indistinguishable from the
  selector form naming a service called `list` — the exact ambiguity
  the reservation exists to prevent.
- The two forms share one lifecycle implementation. A single service
  started by the selector observes the same readiness, shutdown, and
  exit semantics as the same service started by the supervisor.
- Global flags (`--config`, `--format`, `--log-level`, and the rest of
  the kit root flag set) are inherited unchanged by both forms.
  Per-service flags are registered by the service itself and are only
  valid under the selector form.

### The override rule

Explicit selection overrides aggregate enablement:

> `<tool> serve <service>` MUST start `<service>` even when
> `services.<service>.enabled` is `false`, PROVIDED the service is
> registered and its configuration and policy validate.

Enablement answers "does the supervisor start this by default"; it is
not an authorization decision. An operator naming a service on the
command line has already made the decision the flag exists to
automate. Configuration and policy are the two gates that survive the
override, because both encode correctness rather than intent.

**Validation** means all three of the following, evaluated in order:

1. **Registration** — the identifier resolves to a registered
   service in the tool's registry.
2. **Configuration** — the service's own `Validate` returns nil
   against the resolved config: required keys are present, addresses
   and paths parse, referenced files exist, no two enabled services
   claim the same listen address.
3. **Policy** — the service's declared side-effect and network class
   is permitted by the resolved policy table
   ([`go/ai/toolspec/policy`](../../go/ai/toolspec/policy/policy.go)).
   A policy `deny` for the service's class is a refusal, not a prompt.

Failure outcomes, in the order the gates are evaluated:

| Gate           | Failure               | Code                  | Exit | Message shape                                              |
|----------------|-----------------------|-----------------------|------|------------------------------------------------------------|
| Registration   | unknown identifier    | `NOT_FOUND`           | 3    | `unknown service "x"; known: api, socket` + nearest-name fix |
| Registration   | 2+ positional args    | `USAGE`               | 2    | `serve accepts at most one service name`                    |
| Configuration  | invalid or incomplete | `USAGE`               | 2    | `service "x": <field>: <reason>`                            |
| Policy         | denied for class      | `UNAUTHORIZED`        | 5    | `service "x" denied by policy (side_effect=…, network=…)`   |

Aggregate enablement never appears in this table: "not enabled" is not
a failure under the selector form, it is the condition the override
exists to lift. Under the supervisor form, a disabled service is
skipped silently — it is not an error, and it MUST NOT affect the exit
code.

A supervisor invocation that resolves to **zero** services is a
configuration error, not a clean exit: it exits `2` (`USAGE`) with
`no services configured and enabled; enable one under services.* or
name one explicitly`. A process that exits 0 without listening is
indistinguishable from a successful start to a supervisor such as
systemd or a container runtime.

## Service registration

Services register into a **registry** — a per-tool seam that both kit
and the adopter write to. Kit-owned services (the HTTP API, the MCP
channel) register from kit; adopter-owned services register from the
adopter's `main` before the root command executes.

A registration MUST provide, at minimum:

| Field      | Purpose                                                            |
|------------|--------------------------------------------------------------------|
| `Name`     | stable service identifier (see naming rules)                        |
| `Start`    | begins serving; blocks until its context is canceled or it fails   |
| `Ready`    | reports whether the service is accepting work                       |
| `Stop`     | drains and releases resources; bounded by the stop timeout          |

A registration MAY additionally declare a config `Validate` hook, a
side-effect/network class for the policy gate, per-service flags, and
a `DependsOn` list.

### Naming rules

- Identifiers MUST match `^[a-z][a-z0-9-]*$` — lowercase ASCII,
  digits, and internal hyphens. The identifier is a CLI word, a config
  key segment, and a bus topic segment at once.
- Identifiers MUST be stable across releases. Renaming one is a
  breaking change to the command surface, the config file, and any
  subscriber filtering on the topic.
- Identifiers are **unique per registry**. Registering a name twice
  panics at construction time with
  `serve: service "x" already registered`, matching the panic-on-
  duplicate contract of
  [`output.Registry`](../../go/console/output/registry.go). A
  collision is a wiring bug in `main`, discoverable on the first run
  rather than at the first `serve`; there is no last-writer-wins path.
- An adopter deliberately replacing a kit-shipped service calls
  `Override` instead. This is the documented escape hatch, and the
  only way a duplicate name is accepted.

Reserved names: `all`, `none`, and `list` MUST NOT be registered. They
are reserved for future selector vocabulary and would be ambiguous
with a service of the same name.

### Ordering

- `List` MUST return services in registration order, so `--list` and
  log output are stable and mirror the adopter's wiring.
- Start order follows `DependsOn` (topological, ties broken by
  registration order). A dependency cycle is a construction-time
  panic, in the same class as a name collision.
- Stop order is the exact reverse of the order in which services
  actually started.

## Readiness

**Ready** means the service has completed every acquisition that can
fail deterministically and is now accepting work: a listener bound to
its address, a socket file created with its final permissions, a
subscriber attached to its topics. Readiness is not liveness — a ready
service may still be idle, and it may later fail.

- A service MUST report ready at most once per start.
- The aggregate is ready when **every started service** is ready. A
  skipped (disabled) service does not participate.
- A service that has not reported ready within its readiness timeout
  (default 30s, `services.<name>.ready_timeout`) is treated as a
  start failure — see [Exit behavior](#exit-behavior).

### Surfaced events

Readiness and lifecycle transitions publish to the bus using the
4-segment past-tense convention in
[event-topics.md](event-topics.md). The object segment is the literal
`service`; the service identifier travels in the payload, not the
topic, so subscribers are not forced to re-bind when a tool gains a
service.

| Topic                                 | Emitted when                                  |
|---------------------------------------|-----------------------------------------------|
| `kit.serve.service.started`           | `Start` has been invoked for a service        |
| `kit.serve.service.ready_reported`    | a service reported ready                      |
| `kit.serve.service.failed`            | a service returned a non-nil error            |
| `kit.serve.service.stopped`           | a service's `Stop` returned                   |
| `kit.serve.supervisor.ready_reported` | every started service is ready                |
| `kit.serve.supervisor.stopped`        | the supervisor finished its shutdown sequence |

A service that has an address — a listener, a socket path — MAY
declare it, and the supervisor carries it in the `ready_reported`
payload and its log counterpart under `address`. This is the one
startup detail an operator always wants and configuration cannot
always supply: for a wildcard port (`:0`) the bound port is not
knowable until the bind succeeds.

Every action above already passes
[`bus.ValidateTopic`](../../go/runtime/bus/topics.go) without touching
the whitelist: `started`, `failed`, and `stopped` are listed, and
`ready_reported` is a snake_case multi-word action that satisfies the
`"ed"` heuristic on the whole segment — the same shape as
`pre_transitioned`. A bare `ready` does **not** validate; do not use
it. Extend `pastTenseWhitelist` before introducing any further verb
here.

The `kit.serve` prefix is rebrandable per the usual
`WithTopicPrefix` recipe. Payloads embed `bus.Qualifiers`; the reason
a service failed belongs in the payload, never in the topic.

Every event has a log counterpart at `INFO` (`started`,
`ready_reported`, `stopped`) or `ERROR` (`failed`) through
[`go/console/log`](../../go/console/log/), so a tool with no bus wired
still produces an operator-legible startup trace.

## Shutdown

### Signals

The supervisor installs a `signal.NotifyContext` for `SIGINT` and
`SIGTERM`, matching
[`api.ListenAndServeWithSignals`](../../go/transport/api/server.go).

- The first signal begins graceful shutdown.
- A second signal of either kind during shutdown aborts the drain and
  exits immediately with the crash code. Operators MUST be able to
  escalate without reaching for `SIGKILL`.
- `SIGKILL` and `SIGSTOP` are not catchable and are out of contract.

### Ordered stop

1. The shared context is canceled. Every service observes
   cancellation at the same instant; nothing is queued behind another
   service's drain.
2. `Stop` is invoked in reverse start order, one service at a time, so
   a dependent is always fully stopped before its dependency.
3. Each `Stop` is bounded by the per-service stop timeout (default
   `30s`, `services.<name>.stop_timeout`), which matches the existing
   default in [`api.ListenAndServe`](../../go/transport/api/server.go).
   A `Stop` that exceeds its budget is abandoned: the supervisor logs
   it, emits `kit.serve.service.failed`, and proceeds to the next
   service rather than blocking the whole shutdown on one straggler.
4. Draining is the service's own responsibility inside `Stop`:
   in-flight requests finish, buffers flush, sockets unlink. A service
   MUST NOT accept new work after `Stop` is entered.

The supervisor's total shutdown budget is
`services.shutdown_timeout` (default `60s`), an upper bound across all
services. Exceeding it exits with the crash code.

### One service fails while others run

**Default: fail-fast.** A running service that returns a non-nil error
causes the supervisor to shut every service down in order and exit
non-zero. A tool whose transports front the same command tree and the
same state is degraded, not healthy, when one of them is gone — and a
process that keeps its liveness check green while silently serving
fewer surfaces is the failure mode that costs the most to diagnose.

**Override:** `services.failure_policy: isolate` keeps the remaining
services running, marks the failed one `failed`, and lets the process
continue. Under `isolate` the aggregate readiness event is not
re-emitted, and the process still exits with the crash code when the
last running service stops. `isolate` is appropriate when the services
are genuinely independent and partial availability beats none —
supervise it accordingly, because the process stays alive with less
than it was asked to run.

Both policies apply identically to the selector form, where they are
indistinguishable: one service failing is the only service failing.

## Exit behavior

Codes come from the kit taxonomy in
[`go/console/output`](../../go/console/output/error.go); this contract
allocates no new numbers.

| Situation                                  | Code            | Exit |
|--------------------------------------------|-----------------|------|
| Clean stop after a signal                  | `OK`            | 0    |
| Invalid selection (2+ args, reserved name) | `USAGE`         | 2    |
| Config validation failure                  | `USAGE`         | 2    |
| Zero services resolved (supervisor)        | `USAGE`         | 2    |
| Unknown service name                       | `NOT_FOUND`     | 3    |
| Policy validation failure                  | `UNAUTHORIZED`  | 5    |
| One service failed to start                | `GENERIC`       | 1    |
| One service crashed at runtime             | `GENERIC`       | 1    |
| Shutdown budget exceeded                   | `GENERIC`       | 1    |

Notes:

- A signal-initiated stop is a **clean** stop. `SIGTERM` is how a
  supervisor asks for an orderly exit; answering it with a non-zero
  code makes every rolling restart look like a crash.
- Start failure and runtime crash share exit `1` deliberately. They
  differ in *when*, not in *what an operator does next*, and the
  distinguishing detail (which service, which error) belongs in the
  message and the `failed` event, not in a second numeric code.
- Under `isolate`, the exit code reflects the worst outcome observed
  across the whole run, not the last one.
- Failures are classified `TransiencePermanent` unless the underlying
  error is already a kit transient error, in which case exit `6`
  (`TRANSIENT`) is propagated unchanged so agents and retry wrappers
  keep their existing branch.

## Configuration surface

A service is **configured** when a `services.<name>` block resolves,
and **enabled** when that block's `enabled` key resolves to true.
Resolution follows the standard kit precedence in
[`go/core/config`](../../go/core/config/README.md): flag, then env,
then config file, then default.

| Key                                | Type       | Default | Meaning                                     |
|------------------------------------|------------|---------|---------------------------------------------|
| `services.<name>.enabled`          | bool       | `false` | supervisor starts this service              |
| `services.<name>.ready_timeout`    | duration   | `30s`   | budget from `Start` to ready                |
| `services.<name>.stop_timeout`     | duration   | `30s`   | budget for one `Stop`                       |
| `services.failure_policy`          | enum       | `fail-fast` | `fail-fast` or `isolate`                |
| `services.shutdown_timeout`        | duration   | `60s`   | total shutdown budget across all services   |

Service-specific keys live under the same block and are owned by the
service: `services.api.addr`, `services.socket.path`, and so on. Kit
does not reserve names inside a service's own block beyond the four
lifecycle keys above.

Environment variables follow the kit convention — `<TOOL>` prefix,
uppercase, dots to underscores:
`MYTOOL_SERVICES_API_ENABLED`, `MYTOOL_SERVICES_API_ADDR`.

Flags:

- `--enable <name>` / `--disable <name>` (repeatable) override
  `enabled` for the supervisor form only. `--enable` is the aggregate
  equivalent of the selector's override rule and is subject to the
  same configuration and policy validation.
- `--ready-timeout`, `--stop-timeout`, `--shutdown-timeout` map to the
  keys above.
- Per-service flags registered by a service are only accepted under
  `<tool> serve <service>`; passing one to the supervisor form is a
  usage error, because it would silently apply to one member of a set.
  The api service's `--addr` and `--no-auth` are the documented
  exception: they predate the hierarchy, and refusing them under the
  supervisor form would break every adopter that has one HTTP surface
  and a script that starts it.
- `--enable` / `--disable` are refused under the selector form. The
  override rule already decides enablement there, and accepting both
  would let one invocation say two contradictory things.

Defaulting to `enabled: false` is deliberate. A service that starts
listening because a dependency upgrade added it to the registry is an
unrequested open port; enablement is an explicit act.

## Compatibility

Adopters calling [`cli.WithAPI(...)`](../../go/console/cli/api.go)
keep working. `WithAPI` registers the HTTP API as the `api` service
under the kit-owned `serve` parent rather than mounting a
single-purpose leaf `serve` command. For a tool whose only service is
the API, `<tool> serve` starts the same server, with the same `--addr`
and `--no-auth` flags, and exits the same way.

- `WithAPI` remains supported for the whole of the current major
  version, and continues to be the shortest path to one HTTP surface.
- The registry-backed supervisor is how a tool runs more than one
  service, and how an adopter contributes a service of its own.
- An adopter MUST NOT be required to write mounting code to gain the
  supervisor. Migration is opt-in and mechanical: move `--addr` into
  `services.api.addr` and add services as siblings.
- Exactly one command owns the `serve` word, whichever option mounts
  it first. The two MUST NOT both own it.
- An adopter replacing the built-in API with its own implementation
  registers it under the same name through `WithServiceOverride`.
- The api service projects the tool's own command tree onto REST and
  OpenAPI automatically. This is additive: `APIConfig.Handlers` and
  `APIConfig.Resources` are mounted first and keep working unchanged,
  and every projected route sits under a versioned prefix
  (`/v1/commands`) that no adopter route occupied before. See
  [`go/transport/api`](../../go/transport/api/README.md) for the route
  shape and the mapping tables.

### What changed observably

Three differences are visible to an adopter who upgrades without
changing a line:

1. **`serve` gained children and flags.** It accepts an optional
   service name and the `--list`, `--enable`, `--disable`, and timeout
   flags. `<tool> serve` with no argument is unchanged for a
   single-API tool.
2. **The startup line moved into the lifecycle trace.** The leaf
   command printed `Listening on <addr>` to stderr. The api service
   reports readiness through `kit.serve.service.ready_reported` and
   its log counterpart, which carries the same resolved address under
   a structured `address` key. Anything scraping the literal string
   must read the structured field instead.
3. **`services.api.enabled` defaults to true for `WithAPI`.**
   Enablement defaults to `false` for a service that arrives through
   the registry, because an unrequested open port is the risk that
   default guards against. `WithAPI` is not that case: calling it is
   itself the request to serve the API. An explicit
   `services.api.enabled: false` still wins.

4. **The api service serves `/v1/commands`.** Registering the api
   service — including via `WithAPI` — now also mounts a REST
   projection of the command tree and describes it in the OpenAPI
   document. An adopter writes no mounting code to get it.

   Three consequences are worth stating plainly:

   - **New routes exist that did not before.** They are confined to
     the `/v1/commands` prefix and to `/openapi.json` (the latter only
     when the adopter had not configured `WithOpenAPI`, in which case
     nothing was served there at all).
   - **They are behind the adopter's existing auth.** The projection
     installs no auth of its own; `APIConfig.Auth` gates the projected
     routes and the discovery endpoint exactly as it gates the
     adopter's own.
   - **Not every command is reachable.** Interactive commands and
     destructive commands the policy does not permit on this surface
     are *not* mounted. They remain visible in the discovery listing
     with `invocable: false` and a stable reason, so an operator can
     tell "no such command" from "withheld here". Forced remote
     execution of either class is out of scope.

   Execution runs through the same `cmdsurface` policy gate as every
   other surface, so a command's safety level, permissions and
   confirmation requirements mean the same thing over HTTP as they do
   on the CLI. To permit destructive commands over REST, or to
   withhold a command from it, see
   [expose-cli-over-rest.md](../adopters/guides/expose-cli-over-rest.md).

A deprecation of `WithAPI`, if any, is announced through the standard
kit deprecation surface
([`go/console/cli/deprecation.go`](../../go/console/cli/deprecation.go))
with a release's notice before removal — never silently.

## See also

- [`go/console/serve`](../../go/console/serve/) — Go types for this
  contract.
- [event-topics.md](event-topics.md) — 4-segment topic convention.
- [`go/console/output/error.go`](../../go/console/output/error.go) —
  exit-code taxonomy.
- [`go/transport/api/server.go`](../../go/transport/api/server.go) —
  the HTTP listen/shutdown primitive services build on.
- [`go/transport/cmdsurface`](../../go/transport/cmdsurface/doc.go) —
  the surface bridge a service projects commands onto.
- [`go/core/config`](../../go/core/config/README.md) — config
  precedence and key resolution.
