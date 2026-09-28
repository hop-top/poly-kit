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
| Registration   | unknown identifier    | `NOT_FOUND`           | 3    | `unknown service "x"; known: api, socket, mcp` + nearest-name fix |
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

### Transport services

A **transport service** is a service whose work is projecting the
tool's completed command tree onto one transport: the MCP channel, an
RPC listener, an SSE stream, a bus consumer, the built-in Unix socket.
They are all the same shape, and they differ only in how requests
arrive and how responses leave.

That common shape is centralized in
[`go/transport/transportsvc`](../../go/transport/transportsvc/). A
transport supplies three methods — `Bind`, `Serve`, `Close` — and
receives the whole of the lifecycle in return. A transport MUST NOT
re-implement any of the following:

| Centralized             | What it means for a transport                          |
|-------------------------|--------------------------------------------------------|
| Reflection              | the command tree is reflected once, at `Start`          |
| Policy                  | every invocation passes every invocation-plane gate     |
| Readiness               | ready is reported once, after `Bind` returns            |
| Address                 | `Bind`'s return value is surfaced via `Addressed`       |
| Stop                    | `Close` is called once, bounded, and is idempotent      |

Rules:

- Reflection happens at `Start`, never at construction. The command
  tree is complete only after every option has run and every
  subcommand is mounted; a transport that reflected at construction
  would serve whatever subset existed when `main` happened to build
  it.
- The transport's surface is **pinned** by the seam. A transport
  cannot invoke as a surface other than its own, so per-leaf
  enablement and the destructive ceiling cannot be sidestepped by a
  transport setting a different `Meta.Surface`.
- `Bind` MUST acquire everything that can fail deterministically. It
  is the acquisition readiness is about: ready is reported when `Bind`
  returns nil, and never before.
- `Close` MUST make `Serve` return. A `Close` that leaves the listener
  open leaves the process holding a port after `serve` has reported it
  stopped.
- A transport declares its configuration gate, its side-effect and
  network class, and its `DependsOn` list through the seam's options
  rather than by implementing `Validator`, `Classified`, or
  `Dependent` itself.

Registration, naming, and enablement are unchanged: a transport
service registers like any other, its identifier obeys the naming
rules above, and `enabled` defaults to `false`. Kit ships `socket`,
`mcp` and `rpc` on this seam; `api` predates it and keeps its own
implementation.

Adopters start from the task guides rather than this section: [serve
your CLI over a Unix socket](../adopters/guides/serve-cli-over-unix-socket.md)
to run the built-in service, and [build a transport
service](../adopters/guides/build-a-transport-service.md) to put your
own transport on the seam.

The seam lives under `go/transport/` rather than beside the contract
types in [`go/console/serve`](../../go/console/serve/) because the
command-tree half of it reaches `cmdsurface`, which reaches
`cmdreflect`, which reaches `go/console/cli`, which registers services
back into `serve`. Keeping the contract package free of the transport
stack is what keeps that acyclic.

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

### Readiness over HTTP

Every kit HTTP listener — the api service's, the mcp service's HTTP
transport, the rpc service's — surfaces readiness to orchestrators
and load balancers as two routes. They are answered in front of the
listener's router and end the request there: inside the request id, access log,
recovery, telemetry and security header layers, and before the Host
and Origin checks, the body limit and authentication, because
orchestrator probes address a pod by IP and carry no credentials. A
route the adopter registered at exactly a probe path wins over it.

- `GET /healthz` is liveness. It answers `200` whenever the process
  answers HTTP at all, and never consults readiness or dependencies.
- `GET /readyz` is readiness. It answers `200` only while the
  listener's service is ready AND, for the api service, every service
  it declares in `DependsOn` is ready in the current run; `503`
  otherwise — before the listener reports ready, once `Stop` begins,
  or while a dependency is starting, failed, stopped, or reports not
  ready.

A dependency's readiness is the supervisor's record, not only the
dependency's own `Ready`: a dependency that failed at runtime under
`isolate` is not ready even if its `Ready` still says true. The
supervisor exposes that record to every `Start` it calls, as a
`RunView` on the start context. A dependency the run did not start is
not consulted, the same rule [Ordering](#ordering) applies.

The `503` body names failing checks only on a loopback bind unless
`services.<svc>.health.detail` says otherwise, and never carries a
version, a path, or error text. The routes are not commands: they
appear in neither command discovery nor the OpenAPI document.

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

### Re-execution

A run is over when the supervisor returns; the root it ran on is not
consumed. Executing `serve` again on the same root — a test harness, a
tool that restarts its services in-process — MUST start a run that
observes only the new call's context and signals: it keeps serving
until that context is canceled, and it returns when it is. A run MUST
NOT inherit the previous run's context, whether that context is still
alive (the new run would ignore its own cancellation) or already
canceled (the new run would stop without serving).

## Exit behavior

Codes come from the kit taxonomy in
[`go/console/output/envelope`](../../go/console/output/envelope/exitcodes.go);
this contract allocates no new numbers.

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
- A refusal raised by the argument parser before the command runs — a
  positional count the command does not accept, an unknown or
  malformed flag, a flag missing its value or its required companion,
  or a word naming no known subcommand — is `USAGE`, exit `2`,
  rendered as the same envelope the command's own refusals use. This
  holds for every kit command, and wherever the arguments of a
  resolved command are parsed: the shell exit status, `result.exit_code`
  over the socket, and the REST status it maps to. The parser MUST NOT
  leave such a refusal as a bare exit `1`.
- On the command line, an unknown subcommand is `USAGE`, not
  `NOT_FOUND`, and stays `USAGE` no matter which command it was aimed
  at. A subcommand is part of the invocation's grammar; `NOT_FOUND` is
  for an operand naming a resource that does not exist, which is why
  an unknown *service* name above is exit `3`. The distinction is what
  the operator does next: re-read the usage, or go looking for the
  resource. A command that only groups subcommands and cannot run on
  its own is the case most easily missed — it has no argument
  validator of its own, so the refusal has to be raised before
  dispatch rather than by the command. Invoked bare, such a command
  still prints its help and exits `0`.
- The served surfaces address a command by path rather than by parsing
  one, so a path naming no exposed command is refused by the bridge
  before any invocation exists — a transport-level `unknown_command`,
  not a `Result` carrying exit `2`. The `USAGE` rule above governs the
  arguments of a command that *was* resolved.

## Configuration surface

A service is **configured** when a `services.<name>` block resolves,
and **enabled** when that block's `enabled` key resolves to true.
Resolution follows the standard kit precedence in
[`go/core/config`](../../go/core/config/README.md): flag, then env,
then config file, then default. A `-c key=value` override counts as a
flag and a `-c <path>` file as a config file.

`serve` layers every `services.*` key from these sources itself,
before any service reads one, whether or not the tool loads its own
configuration: the `services` block of the tool's system, user
(`~/.config/<tool>/config.yaml`) and project config files and of each
`-c <path>`, every `<TOOL>_SERVICES_*` variable, and every
`-c services.<key>=<value>`. Keys outside `services` resolve exactly
as the tool wired them. A service configured only by environment
variables is configured.

| Key                                | Type       | Default | Meaning                                     |
|------------------------------------|------------|---------|---------------------------------------------|
| `services.<name>.enabled`          | bool       | `false` | supervisor starts this service              |
| `services.<name>.ready_timeout`    | duration   | `30s`   | budget from `Start` to ready                |
| `services.<name>.stop_timeout`     | duration   | `30s`   | budget for one `Stop`                       |
| `services.failure_policy`          | enum       | `fail-fast` | `fail-fast` or `isolate`                |
| `services.shutdown_timeout`        | duration   | `60s`   | total shutdown budget across all services   |

Service-specific keys live under the same block and are owned by the
service: `services.api.addr`, `services.socket.path`, and so on. Kit
reserves names inside a service's own block only for the lifecycle
keys above and for the middleware block names in
[the block registry](#block-registry), which every service shares.
`services.all` holds middleware defaults for every service; see
[Middleware configuration](#middleware-configuration).

The kit-shipped services own these (the `mcp` service's keys are in
[The mcp service](#keys-and-flags), the `rpc` service's in
[The rpc service](#rpc-keys-and-flags)):

| Key                            | Type   | Default                | Meaning                                                        |
|--------------------------------|--------|------------------------|----------------------------------------------------------------|
| `services.api.addr`            | string | `127.0.0.1:8080`       | HTTP listen address; loopback unless authenticated or opted in |
| `services.api.insecure_remote` | bool   | `false`                | serve the api unauthenticated on a non-loopback address        |
| `services.api.insecure_no_policy` | bool | `false`             | beyond loopback with no `--policy`, serve with no policy rather than `kit-default` |
| `services.api.health.enabled`  | bool   | `true`                 | serve `/healthz` and `/readyz` ([Readiness over HTTP](#readiness-over-http)) |
| `services.api.health.path_prefix` | string | `""`              | mount both probe routes under an absolute path prefix          |
| `services.api.health.detail`   | bool   | `true` on loopback, else `false` | name failing checks in a `/readyz` `503`              |
| `services.socket.path`         | string | runtime dir, see below | Unix socket path                                               |

The mcp and rpc services read the same keys for their own listeners,
under `services.mcp.health.*` and `services.rpc.health.*`. Each may
also be set under `services.all.health.*`; the service's own key
wins, key by key, and any other key in either block is refused at
validation, exit `2`.

`services.api.addr` defaults to a loopback address, and a non-loopback
value is refused at validation unless `APIConfig.Auth` is set or
`services.api.insecure_remote` is true; with no `--policy` named it
enforces `kit-default` unless `services.api.insecure_no_policy` is
true — see [Security](#security).

The socket's default path is `<runtime dir>/<tool>/<tool>.sock`, where
the runtime dir is `$XDG_RUNTIME_DIR` when set, and otherwise the
platform's own location for ephemeral per-user files
(`~/Library/Application Support` on macOS, the temp directory when no
runtime base is available).

`--socket` overrides `services.socket.path` for one run, the way
`--addr` overrides `services.api.addr`. The socket path is resolved to
an absolute path, and a path longer than the platform's `sockaddr_un`
limit is a configuration failure at exit `2` rather than a kernel
`invalid argument` at start.

The socket is created with mode `0600`. On a Unix domain socket the
filesystem permission IS the access control — the socket has no port
and is not routable — so the service is loopback-only by construction
and grants nothing on the basis of a caller-supplied identity.

Environment variables follow the kit convention — `<TOOL>` prefix
(the tool name upper-cased, dashes to underscores), uppercase, dots to
underscores: `MYTOOL_SERVICES_API_ENABLED`, `MYTOOL_SERVICES_API_ADDR`.
Underscores inside a name are resolved longest match first — a
registered service (or `all`), then a middleware block, then a key —
so `MYTOOL_SERVICES_API_BODY_LIMIT_MAX_BYTES` is
`services.api.body_limit.max_bytes` and
`MYTOOL_SERVICES_API_INSECURE_REMOTE` is `services.api.insecure_remote`.

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
  The api service's `--addr`, `--no-auth`, and `--insecure-remote`
  are the documented exception: the first two predate the hierarchy,
  and refusing them under the supervisor form would break every
  adopter that has one HTTP surface and a script that starts it;
  the third qualifies the other two, and a qualifier valid in fewer
  places than the flags it qualifies would be a trap.
- `--enable` / `--disable` are refused under the selector form. The
  override rule already decides enablement there, and accepting both
  would let one invocation say two contradictory things.

Defaulting to `enabled: false` is deliberate. A service that starts
listening because a dependency upgrade added it to the registry is an
unrequested open port; enablement is an explicit act.

## Security

A transport service admits callers the tool did not start. This
section states what the kit-shipped services promise about who those
callers are, what they may do, and what is recorded. The Go types
that encode it are `cmdsurface.Meta`, `cmdsurface.PermissionFunc`,
and `cmdsurface.Bridge.Audit` in
[`go/transport/cmdsurface`](../../go/transport/cmdsurface/).

### Two planes, not one

Exposure lives on the HTTP router; provenance, permission, and audit
live in the bridge. The two MUST NOT be collapsed into one middleware
stack.

- Not every surface has HTTP. Socket is NDJSON over a Unix socket, MCP
  is stdio, and the in-process `lib`, cron, and bus surfaces have no
  request object at all. A concern that must hold for every caller of
  a command — authorization, quotas, confirmation, audit — cannot
  live in `func(http.Handler) http.Handler`. It has to sit where every
  surface already funnels through one call: `Invoker` at the seam,
  `Bridge.Invoke` behind it.
- Some concerns exist only on the wire. TLS, compression, security
  headers, Origin/Host checks, trusted-proxy IP, and body size limits
  are properties of an HTTP connection. Lifting them into the
  invocation plane would force a fake HTTP shape onto stdio and
  socket, or leave them as no-ops there.
- This is what keeps the transport contract in
  [transportsvc](../adopters/reference/transportsvc.md) true: a
  transport receives an `Invoker` with the surface pinned and the
  gate wired, and never reads an annotation or gates a command
  itself. Collapsing the planes would put transport-specific code
  back in the business of deciding policy, which is exactly the shape
  that let the socket and RPC surfaces drift onto different rules
  before this contract existed.

What the two planes still share: one identity model (the HTTP plane
authenticates and produces claims; the bridge only ever consumes
`Caller`/`Tenant`/scopes off `Meta` — see Provenance below); one
config surface (`services.<name>.*`, so an adopter never needs to know
which plane a knob lives in); one refusal vocabulary (bridge sentinel
errors map onto HTTP status, socket codes, and MCP `isError` alike —
see Permission below).

| Concern | Plane |
|---|---|
| authn: verify a presented credential, produce claims | edge: HTTP, or the socket and stdio equivalent |
| authn: enforce `kit/auth-required` | invocation (bridge) |
| authz, scopes, per-caller policy, quotas, rate limit (by principal, else client address) | invocation (bridge) |
| body limit, server timeouts, TLS, headers, Host/Origin, resolving the client address behind a proxy | HTTP |
| audit, redaction, idempotency replay, result-cache lookup | invocation (bridge) |
| compression, ETag and conditional responses | HTTP |

The test for where a new middleware goes: if it must hold for a
caller on MCP stdio, it belongs in the bridge. If it only makes sense
with a TCP connection, it belongs on the router. The order each plane
runs its middleware in, and the configuration and refusals they share,
are in [Middleware](#middleware).

### Exposure

- The api service MUST default to a loopback listen address
  (`127.0.0.1:8080`).
- The api service MUST refuse, at the configuration gate (exit `2`),
  a non-loopback address on which it would serve unauthenticated: no
  `APIConfig.Auth`, or `Auth` disabled by `--no-auth`. The message
  MUST name the three remedies — set `Auth`, listen on loopback, or
  opt in.
- The opt-in is `services.api.insecure_remote` (`--insecure-remote`,
  `APIConfig.InsecureRemote`), resolved flag, then config, then code.
  It permits unauthenticated serving on any address and changes
  nothing else. Its name is part of the contract: a configuration
  reviewer MUST be able to find every such deployment by that key.
- `--no-auth` MUST NOT widen exposure: it is accepted on a loopback
  address, or under the opt-in, and refused otherwise.
- Beyond loopback, a service with no `--policy` named MUST enforce
  `kit-default`, the policy kit ships (`policy.KitDefault`), rather
  than serve with a gate that bounds nothing. A tool that names no
  `--policy` would otherwise have a permission gate whose verdict is
  indistinguishable from having none: every side-effect class
  permitted for every caller, destructive included. Authentication
  answers who is calling; a policy answers what any caller may run,
  and a surface reachable from other hosts MUST have an answer to
  both. `kit-default`:
  - lets a caller the transport did not establish read only;
  - lets an established principal read and write;
  - lets an established principal run a destructive command only when
    the command declares `kit/permissions` — an explicit scope, which
    the scope check then requires the caller to hold — and never
    waives the command's own confirmation or the destructive ceiling;
  - treats a command declaring no `kit/side-effect` as a write;
  - names itself and the remedy in every refusal
    (`… (policy kit-default: …; name a --policy to choose otherwise)`).
  `--policy=kit-default` names it explicitly, on any address; the
  name is reserved and never read from a file.
- The opt-out is `services.api.insecure_no_policy`
  (`--insecure-no-policy`, `APIConfig.InsecureNoPolicy`), resolved
  flag, then config, then code: beyond loopback with no `--policy`,
  serve with no policy at all. Its name is part of the contract on
  the same terms as `insecure_remote`: a configuration reviewer MUST
  be able to find every unbounded remote surface by that key.
- The two opt-ins are independent and neither implies the other.
  `insecure_remote` waives authentication only; `insecure_no_policy`
  waives the policy default only. A tool that sets `Auth` still
  serves under `kit-default` without a `--policy`, because
  authenticating a caller says nothing about what that caller may
  run; under `insecure_remote` alone no caller is established, so
  `kit-default` lets every caller read only.
- `services.<svc>.auth.mode` authenticates for these rules on every
  kit HTTP listener, as `Auth` does, whichever mode it names (`mtls`,
  `jwt`, `jwks`, `oidc`); plain `tls` does not. See
  [TLS and client certificates](#tls-and-client-certificates) and
  [Bearer tokens](#bearer-tokens).
- Loopback keeps allow-by-default under both rules: no `--policy`
  there means no policy, and the socket service and stdio are
  loopback. It is the
  development path, and the caller is already on the machine.
- A tool that sets `Auth` keeps working on any address. `WithAPI`
  adopters whose address was the old default keep working on
  loopback; an adopter who set a non-loopback address and no `Auth`
  is refused with the message above until they choose.
- Loopback means the literal host is `127.0.0.0/8`, `::1`, or
  `localhost`. An empty host binds every interface and is not
  loopback.
- The socket service is loopback by construction: a Unix socket has
  no port and is not routable, and the socket file is created `0600`,
  so the filesystem permission is the access control. No address rule
  applies to it.
- The mcp service's HTTP transport MUST apply every rule above to its
  own listen address, with its own names: `mcpserve.Config.Auth` for
  authentication, `services.mcp.insecure_remote` and
  `services.mcp.insecure_no_policy` for the opt-ins, default address
  `127.0.0.1:8081`. The stdio transport has no address and no address
  rule; its trust model is in [The mcp service](#identity-and-trust).
- The rpc service MUST apply every rule above to its own listen
  address, with its own names: `rpcserve.Config.Auth` for
  authentication, `services.rpc.insecure_remote` and
  `services.rpc.insecure_no_policy` for the opt-ins, default address
  `127.0.0.1:8082`.

### Provenance

`cmdsurface.Meta` carries the call's provenance across every
transport. A transport MUST populate the fields it has evidence for
and MUST leave the rest empty; the bridge grants nothing on the basis
of any of them.

| Field            | api service                                    | socket service                              |
|------------------|------------------------------------------------|---------------------------------------------|
| `Caller`         | principal from the `Auth` claims               | verified by the socket's authenticator (`auth.mode: peer`, else `SocketConfig.Auth`), else the request's `caller` as a claim |
| `Tenant`         | tenant from the `Auth` claims                  | verified by the socket's authenticator (empty under `peer`), else the request's `tenant` as a claim |
| `Established`    | `verified` when `Auth` verified the request, else empty | `verified` with an authenticator, else `transport` (the `0600` file) |
| `Surface`        | `rest`, pinned                                 | `socket`, pinned by the seam                |
| `RequestID`      | `X-Request-ID`, issued when absent, echoed     | `request_id`, issued when absent            |
| `TraceID`        | `traceparent` trace-id, else `X-Trace-ID`      | `trace_id`                                  |
| `Traceparent`, `Tracestate` | W3C `traceparent` when well-formed, and `tracestate` beside it | —                  |
| `IdempotencyKey` | `Idempotency-Key`                              | `idempotency_key`                           |
| `RequestedAt`    | receipt time                                   | receipt time                                |
| `Extra`          | `remote_addr`, `peer_addr` (see [Client address](#client-address)), `scopes` (comma-joined claims)  | what the authenticator recorded (`peer_uid`, `peer_gid`, `peer_pid` under `peer`, on a refusal too), and `scopes` from its identity |

- Claims MUST be extractable without the transport importing the
  adopter's types: a value implementing `api.Identity`, an
  `api.Claims`, or a string-keyed map with `sub` and `tenant`. A
  claims value of another shape authenticates the call and leaves it
  unattributed.
- A caller-supplied identity on a transport without an authenticator
  is provenance, not a credential. The socket's `caller` and `tenant`
  are recorded as claimed; `SocketConfig.Auth` is what makes them
  verified, and its verdict replaces the claim.
- `Established` is how the transport established the caller:
  `verified` (a configured verifier accepted a credential) or
  `transport` (the transport proves the caller itself). It MUST be
  set only by the transport, from its own verdict, and MUST NOT be
  serialized, so no request body, frame or payload can set it. A
  claimed `Caller` without it is unauthenticated.
- The request context MUST reach the bridge unchanged, so a client
  disconnect cancels the invocation on both transports. Whether the
  command stops is the runner's contract.
- The idempotency key MUST reach the leaf's `--idempotency-key` flag
  when the leaf registers one and the caller did not set it. Replay
  is the bridge's, at slot 8 ([Idempotency](#idempotency)); a
  transport only carries the key.
  A command's own `--idempotency-key` middleware MUST scope a served
  invocation's key to its caller with the same scope the bridge uses
  (`cmdsurface.ScopeIdempotencyKey`, over `cmdsurface.IdempotencyScope`).
  The bridge carries the admitted `Meta` on the run's context, whatever
  the runner, and `cmdsurface.AdmittedMeta` is the one accessor a
  command reads it with; a runner that hands the call to a child
  process MUST pass the scope as `KIT_IDEMPOTENCY_SCOPE`.

### Permission

- `cmdsurface.Bridge.Invoke` applies its gates in this order:
  resolution, surface enablement, invocability (an interactive or
  self-hosting leaf is refused with `ErrNotInvocable` naming the
  reflector's reason, whatever the surface), the destructive ceiling
  (`Policy.Allowed`), then the permission gate (`PermissionFunc`).
  [Middleware](#gate-order-on-the-invocation-plane) places the gates
  middleware adds among these without reordering them.
  The invocability gate is the bridge's, so a transport that exposed
  the whole tree cannot admit an interactive leaf; the runner's own
  refusal is a backstop. The REST projection (the api service's
  `/v1/commands`, `cmdsurface.MountProjection`) withholds such
  commands at mount, so the route is absent (`404`) and discovery
  carries the reason. The deprecated `cmdsurface.MountREST` mounts
  every leaf the bridge admits, interactive ones included, and the
  bridge refuses the call (`404 not_invocable`); a self-hosting
  command is never a leaf, so it has no route there either. Over the
  socket the answer is `NOT_INVOCABLE`.
  The command's own confirmation is not a bridge gate: it is the
  command's own flag and its own refusal, an exit code in the Result.
  Confirmation a surface obtains from a person has its own slot,
  after every machine gate (see [Middleware](#middleware)).
- The permission gate MUST run on every surface, inside the bridge,
  so no transport can bypass it. A transport that streams admits the
  invocation with `Bridge.Admit` — the same gates, order, errors and
  audit as `Invoke` — before it opens the stream, and runs it with
  `Admission.Stream`; it MUST NOT call the runner directly. The
  default permits everything.
- `cli.WithPermission` installs the adopter's decision on the api,
  socket, mcp, and rpc services. It composes after the tool's policy engine:
  a `--policy` that refuses a side-effect class refuses it on every
  surface, before the adopter's decision is asked — the same
  `Engine.AuthorizeFor` the CLI runs, for the caller the transport
  established. The policy's `callers` section answers per caller: the
  first rule matching the established principal, tenant, or a scope
  its credential holds answers for the classes it declares, and the
  policy's own `allow` for the rest. An unestablished caller is
  answered by the policy's own rules alone. A transport-established
  caller (the owner-only socket, stdio) holds every scope, as at the
  scope check, and is matched by no name it claims; a caller a verifier
  established, peer credentials on the socket included, holds its
  credential's scopes only. The command runs
  under the same answer: the in-process run sees the admitted `Meta`
  (`cmdsurface.AdmittedMeta`), so the `--policy` check inside the
  command asks for the same caller. The policy's `permissions:` rules
  sit between the policy and the adopter's decision (see slot 6 under
  [Middleware](#gate-order-on-the-invocation-plane)).
- A `PermissionFunc` reads the invocation's args and flags from its
  context (`cmdsurface.InvocationFromContext`); a mount-time query
  carries none.
- A caller rule with `max_ops` gives each matching principal, per
  tenant, a budget of admitted write and destructive calls over fixed
  `window`s (default one hour, aligned to the epoch), counted on
  `storage/kv` so a restart resets nothing: `$XDG_STATE_HOME/<tool>/usage.db`
  by default, or the store `cli.WithUsageStore` names. The permission
  gate refuses a spent budget `permission_denied` at the policy's turn,
  naming when the window resets, and charges one call only after every
  slot-6 decider — rules and adopter included — has admitted it; a call
  any of them refuses spends nothing, and a discovery probe
  (`cmdsurface.ProbeContext`, `Bridge.Verdict`) reads the budget
  without charging it. A store that cannot be read refuses the call.
- A refusal returns `ErrPermissionDenied` with a stable reason. It
  is `403 permission_denied` over REST and `DENIED` over the socket,
  distinct from the destructive ceiling's `403 destructive_blocked`
  and `BLOCKED`, because different people fix them. Over RPC both
  are `permission_denied`, told apart by the message's sentinel.
- The shared listing — the one served to a request whose caller
  nobody established — MUST keep a command invocable when the verdict
  depends on the caller; it cannot know who will call. A decision
  marked `CallerIndependent` MAY be reflected at mount with the reason
  `permission-denied`, owned by `cmdsurface`, beside the reflector's
  own vocabulary. The policy engine marks a refusal caller-independent
  only when no caller rule could lift it.
- A listing requested by a caller the transport established MUST
  reflect slot 6 for that caller — the scope check, the policy, the
  `PermissionFunc` — asked as a probe (`cmdsurface.Bridge.Verdict`,
  `cmdsurface.IsProbe`) that runs, charges and audits nothing. Over
  REST a command it refuses is listed `invocable: false` with the
  reason `insufficient-scope` or `permission-denied`, and the listing
  is `Cache-Control: private`; the MCP tool list leaves it off. Routes
  are not affected: every call still meets every gate.

### Audit

- Every refusal — authentication (`ErrAuthRefused`, reported by the
  transport through `Bridge.Audit`), enablement, the destructive
  ceiling, permission — and every execution on a remote surface MUST
  reach the bridge's sinks with the Meta above, the command path, and
  the verdict: the refusing error, or the command's Result.
- `cli.WithAuditSinks` registers sinks on every kit-shipped transport
  service. Sinks are best-effort and MUST NOT change a verdict.
- `services.<svc>.audit.sinks` (then `services.all.audit.sinks`) adds
  sinks from configuration. `chain` appends to a hash-chained log
  (`go/security`); services in one process naming the same file MUST
  share one log, and a second process MUST be refused at start rather
  than fork the chain. An entry that does not parse is refused at
  validation.
- `<tool> audit verify`, mounted by `cli.WithAuditCommand`, is
  kit-reserved and so `management-only` on every served surface. A
  broken chain exits 71, `TAMPER_DETECTED`.
- CLI and in-process library invocations are not audited: the first
  is the operator's own act, the second has no caller to attribute.

## The mcp service

`mcpserve.With` (package
[`go/console/cli/mcpserve`](../../go/console/cli/mcpserve/)) registers
the third kit-shipped service, `mcp`: the
tool's command tree served as Model Context Protocol tools, one tool
per invocable leaf, named by its dotted path (`item.add`). It rides
the [transport seam](#transport-services) exactly as `socket` does, so
everything this page says about registration, reflection at `Start`,
readiness, stop, exit codes, the root factory, the permission gate,
and audit applies to it unchanged. Its surface is pinned to `mcp`
([`cmdsurface.SurfaceMCP`](../../go/transport/cmdsurface/doc.go)),
and the protocol layer is the official MCP Go SDK through
[`go/transport/mcpsdk`](../../go/transport/mcpsdk/README.md); kit
implements no MCP wire behavior of its own here.

Like every service that arrives through the registry, `mcp` is
`enabled: false` by default. `<tool> serve mcp` starts it.

The service lives in its own package so that only a tool serving MCP
links the MCP SDK: `go/console/cli` MUST NOT depend on it. It reaches
the Root through the same exported hooks any out-of-package transport
service can use — `cli.WithService`, `cli.ServeBridgeOptions` (or
`cli.ServeBridgeOptionsFor`, which states the service's exposure for
the rate limit's default), `cli.ValidateServeBridge`,
`cli.ServePolicyConfigured`, `cli.IsLoopbackAddr`, and for its
listener `cli.ServeHTTPHandler`, `cli.ValidateServeHTTPListener` and
`cli.ResolveServeHTTPListener` — and so meets exactly the gates and
the [HTTP-plane middleware](#middleware-order-on-the-http-plane) the
in-package services do.

### Transports

The service speaks one of two transports, chosen per run:

| `services.mcp.transport` | Reached by                                        | Address in `ready_reported`     |
|--------------------------|---------------------------------------------------|---------------------------------|
| `http` (default)         | an MCP client over streamable HTTP                | `http://<host>:<port><path>`    |
| `stdio`                  | the process that spawned `<tool> serve mcp --stdio` | none                          |

`--stdio` selects `stdio` for one run and wins over configuration. An
unknown transport value is a configuration failure at exit `2`.

The HTTP transport listens on its **own** listener. It does not mount
on the api service's router, for four reasons, each sufficient:

- **Independent lifecycle.** `serve mcp` MUST start without the api
  service — a tool may register `mcp` and not `api` at all — and
  stopping one MUST NOT take the other down. A shared router would
  make `mcp` a dependent of `api`.
- **Different transport shape.** Streamable HTTP holds a response open
  for server-to-client messages and for a confirmation round trip that
  waits on a human. The api server's write timeout and its
  `application/json` content-type middleware are correct for REST and
  wrong for that.
- **Separate exposure decisions.** An operator who opens the api to
  the network has not thereby opened the MCP surface, and the reverse.
  Each listener answers the [Exposure](#exposure) rules on its own
  address.
- **Separate audit and policy identity.** The two surfaces pin
  different `Meta.Surface` values and are named separately in
  `Policy.AllowDestructiveOn`; one listener per surface keeps a
  refusal attributable to the surface that produced it.

The cost is one more port. An adopter who needs one port mounts the
SDK surface on `APIConfig.Handlers` by hand and accepts that it then
lives inside the api service's lifecycle and exposure.

The HTTP endpoint serves every protocol revision the SDK supports.
A client that runs the `initialize` handshake (revisions through
`2025-11-25`) gets a stateful session: the SDK issues an
`Mcp-Session-Id`, and server-to-client requests travel on the open
response stream. A `2026-07-28` request, which carries its revision
per request and never calls `initialize`, is answered statelessly:
no session, no `Mcp-Session-Id`. The SDK serves `2026-07-28` only from
a stateless handler and keeps sessions only in a stateful one, so the
service holds one of each and routes each request by the markers of
the MCP guide's routing precedence: `initialize` and unmarked requests
to the session handler, a request carrying a `2026-07-28` marker to
the stateless one. Every response, error included, is the SDK's. The
stdio transport serves `2026-07-28` as well. Every response streams
through the middleware in front of the SDK handler, so each layer of
it keeps the response flushable.

The HTTP transport's listener carries the
[HTTP-plane middleware](#middleware-order-on-the-http-plane) every kit
listener does, configured by `services.mcp.*`: security headers, the
health probes, the Host and Origin checks, the metrics endpoint, the
body limit, compression. A refusal there is written as the MCP
transport expects one — the HTTP status and a JSON-RPC error with a
null id — and ends the connection. The SDK keeps its own copies of
two checks beneath, set from the same blocks so it never refuses
what the service's configuration allows: its request body cap is
`body_limit`'s, and its DNS-rebinding check, which admits loopback
names only, stays on unless `host_check` admits other names (an
`allow` list) or is switched off. The stdio transport has no HTTP
plane; the HTTP-plane blocks are read, and validated, only when the
service runs over HTTP.

### Keys and flags

| Key                                | Type   | Default          | Meaning                                                       |
|------------------------------------|--------|------------------|---------------------------------------------------------------|
| `services.mcp.transport`           | enum   | `http`           | `http` or `stdio`                                             |
| `services.mcp.addr`                | string | `127.0.0.1:8081` | HTTP listen address; loopback unless authenticated or opted in |
| `services.mcp.path`                | string | `/mcp`           | HTTP endpoint path; MUST begin with `/`                       |
| `services.mcp.insecure_remote`     | bool   | `false`          | serve HTTP unauthenticated on a non-loopback address          |
| `services.mcp.insecure_no_policy`  | bool   | `false`          | beyond loopback with no `--policy`, no policy rather than `kit-default` |

`mcpserve.Config` carries the code defaults under the same names; the
precedence is flag, then config, then `mcpserve.Config`, then the
default.

Flags, registered on `serve` like `--socket` and inert unless `mcp` is
the service running:

- `--stdio` selects the stdio transport.
- `--mcp-addr` overrides `services.mcp.addr`. Passing it together with
  the stdio transport is a configuration failure at exit `2`: it would
  name an address nothing listens on.

The two insecure opt-ins have **no flags**. The existing
`--insecure-remote` and `--insecure-no-policy` name the api service;
widening them to cover `mcp` would change what an existing script
opts into without its author choosing it. They are set by key or by
`mcpserve.Config`, which is also where a configuration reviewer finds them.

### The tool catalog

The tool list is what a model reads before it calls anything, so it
lists what may run and nothing else. Empty `mcpserve.Config.Expose` exposes
the whole tree, `Hide` carves exceptions after it, and then the
service withholds, the way the REST projection withholds at mount:

- every command the reflector judges non-invocable under the same
  reflection the REST projection uses — the Root's reserved verbs and
  no `Allow*` options — with the same reasons: `interactive`,
  `management-only` (kit's reserved verbs such as `status`),
  `self-hosting`;
- a **destructive** leaf `Policy` does not permit on `mcp`
  (`Policy.AllowDestructiveOn` must name `cmdsurface.SurfaceMCP`);
- a leaf the permission gate refuses **for every caller**
  (`CallerIndependent`);
- for a `tools/list` whose caller the transport established — over
  HTTP one `Config.Auth` verified, over stdio the spawning peer — a
  leaf the permission gate refuses **that caller**, asked as the
  [Permission](#permission) listing rule says.

The two catalogs therefore agree: a command REST discovery marks
`invocable: false` is not an MCP tool, and every command REST mounts
is one. Withholding is advisory, never
the gate: every call still passes `Bridge.Invoke`, and a call naming a
withheld tool is refused as an unknown tool.

### Identity and trust

The service's rule for a leaf declaring `kit/auth-required` follows
from what each transport can prove about its caller:

- **HTTP.** An auth-required leaf runs only when `mcpserve.Config.Auth`
  verified the request. A bare `Authorization` header is not
  authentication, and a loopback TCP listener is reachable by every
  local user, so loopback does not carry the socket's argument below.
  Without `Auth`, auth-required leaves are refused on HTTP. With it,
  every request is authenticated before the SDK sees it, and a refusal
  is `401` plus an `ErrAuthRefused` audit record, exactly as on the
  api service. `services.mcp.auth.mode` supplies the verifier from
  configuration; under a bearer mode that names an issuer and a URL
  audience, the `401` names the protected resource metadata (see
  [Bearer tokens](#bearer-tokens)).
- **stdio.** An auth-required leaf runs. The peer is the process that
  spawned the service: it holds the only ends of the service's stdin
  and stdout, and the service runs with that process's user, because
  spawning a process cannot raise its privilege. Whoever can speak on
  the channel could therefore already run `<tool>` directly with the
  same credentials the command would use. That is the socket's
  argument — an owner-only `0600` file admits only callers who already
  hold the owner's authority — applied to a pair of pipes. The service
  records the transport and the peer's process id; it does not invent
  a principal.

A refusal of an auth-required leaf is an `isError` tool result reading
`authentication required`, audited with `ErrAuthRefused`.

Provenance, by the same rule as the table in
[Provenance](#provenance):

| Field            | mcp over HTTP                                   | mcp over stdio                   |
|------------------|-------------------------------------------------|----------------------------------|
| `Caller`         | principal from the `Auth` claims                | —                                |
| `Tenant`         | tenant from the `Auth` claims                   | —                                |
| `Established`    | `verified` when `Auth` verified the request     | `transport` (the spawn)          |
| `Surface`        | `mcp`, pinned by the seam                       | `mcp`, pinned by the seam        |
| `RequestID`      | `X-Request-ID`, issued when absent              | issued per call                  |
| `TraceID`        | `traceparent` trace-id, else `X-Trace-ID`       | —                                |
| `IdempotencyKey` | `Idempotency-Key`                               | —                                |
| `RequestedAt`    | receipt time                                    | receipt time                     |
| `Extra`          | `mcp_transport=http`, `remote_addr`, `peer_addr` (see [Client address](#client-address)), `scopes`, `mcp_client` | `mcp_transport=stdio`, `peer_pid`, `mcp_client` |

`mcp_client` is the client's self-reported name from the MCP
handshake: provenance, never a credential.

### Confirmation

Two confirmations can apply to one call, answered by different people
and never merged.

**The command's own gate** is unchanged from every other surface: a
destructive command, or one a `--policy` marks `require_confirm`, runs
only when the call carries its own `confirm` argument (and
`confirm-token` for a typed-token command). It is reachable at all
only once the adopter names `mcp` in `Policy.AllowDestructiveOn`.
Over MCP the argument is chosen by the model, exactly as it is chosen
by the program on the other end of REST or the socket.

**The MCP confirmation gate** applies to a leaf that declares
`kit/requires-confirmation`, and it is the one that puts a person in
the loop. A person is asked only after every machine gate has admitted
the call — resolution, enablement, invocability, the auth-required
gate above, the destructive ceiling, and the permission gate, in that
order (`Bridge.Admit`) — so a caller a machine gate refuses is
refused without a prompt. It is satisfied per call, and only by one
of:

1. **An elicitation the client's user accepts.** When the client has
   declared form elicitation, the service asks it, before running
   anything, `Approve execution of "<tool>"?`, with no form fields —
   the answer is the elicitation's action. `accept` runs the call once.
   `decline` or `cancel` refuses it (`confirmation declined`). A client
   on protocol `2026-07-28` (HTTP or stdio) receives the question as an
   `input_required` result and retries with the answer and the echoed
   `requestState`; the state is bound by HMAC to the tool, the digest
   of the arguments, and the verified caller, and expires after five
   minutes. A client on an earlier protocol — every HTTP session —
   receives a server-initiated `elicitation/create` on its session and
   the SDK resumes the call. A `requestState` that fails verification
   is never honored: it is audited, and the question is asked again.
2. **An `X-Confirm-Token` request header**, over HTTP only, as on every
   other kit MCP surface. It is the HTTP client's own per-request act.

A client that offers neither — every stdio client without
elicitation — is refused with `confirmation required`, naming both
remedies. The service never answers the question on anyone's behalf,
and no configuration makes it do so. Confirmation refusals are
audited.

An accepted elicitation satisfies only this gate. It does not supply a
destructive command's `confirm` argument, and it lifts nothing: the
destructive ceiling and the permission gate have already answered
before the question, and what runs is the admission they granted
(`Admission.Run`). A retry that carries the answer is a new call and
is admitted afresh.

### stdio stream discipline

Over stdio, standard output **is** the protocol. For as long as the
service serves stdio:

- The SDK transport holds the process's original standard input and
  output. Every other reader and writer in the process is pointed
  away from them: `os.Stdout` resolves to standard error and
  `os.Stdin` to an empty reader, so a stray print in a command, a hook,
  or a library lands in the operator's log instead of corrupting a
  frame, and nothing but the SDK consumes a request byte.
- The lifecycle trace, kit logging, and every hint or notice go to
  standard error.
- A served command's own output is captured into its `Result` by the
  runner, per [Execution](#execution), and reaches the client inside
  the tool result.

End of input — the host closing the session — ends the service
cleanly: `Start` returns nil, and `serve mcp --stdio` exits `0`. Every
request read before end of input is answered first, so a host may
write its requests and close its end without waiting. A call still
waiting on the host at that point — a confirmation question — can
never be answered and is abandoned; that is still a clean stop. A
signal is a clean stop as on every service.

### Readiness, class, and stop

- Ready is reported when the HTTP listener is bound, or when the stdio
  streams are acquired — every acquisition that can fail
  deterministically.
- The service's policy class is `write-shared` with network `listen`
  over HTTP and `none` over stdio, so a `--policy` that forbids
  listeners still admits a stdio server.
- `Stop` closes every MCP session, which cancels the calls in flight,
  and drains the HTTP server within the stop budget.

### Streaming

A call carrying an MCP progress token streams each output line as a
progress notification. It is admitted by `Bridge.Admit`, which applies
the gates `Bridge.Invoke` does, in the same order, and audits the same
verdicts, and then runs through `Admission.Stream`; streaming is a way
of observing a call, never a way around its gates.

## The rpc service

`rpcserve.With` (package
[`go/console/cli/rpcserve`](../../go/console/cli/rpcserve/)) registers
the fourth kit-shipped service, `rpc`: the tool's command tree served
as the published `cmdsurface.v1.Commands` service
([`contracts/proto/cmdsurface/v1/commands.proto`](../../contracts/proto/cmdsurface/v1/commands.proto))
— `Invoke`, unary, and `InvokeStream`, server-streaming. It rides the
[transport seam](#transport-services) as `socket` and `mcp` do, so
everything this page says about registration, reflection at `Start`,
readiness, stop, exit codes, the root factory, the permission gate,
and audit applies to it unchanged. Its surface is pinned to `rpc`
([`cmdsurface.SurfaceRPC`](../../go/transport/cmdsurface/doc.go)),
and the handler is `cmdsurface.MountRPC` over the generated Connect
handler; kit implements no wire behavior of its own here.

Like every service that arrives through the registry, `rpc` is
`enabled: false` by default. `<tool> serve rpc` starts it.

The service lives in its own package for the reason `mcp` does:
`go/transport/rpc` registers protobuf types at init, which the linker
cannot drop, so a CLI that never serves RPC MUST NOT link it.
`go/console/cli` MUST NOT depend on `go/transport/rpc`. The service
reaches the Root through the exported hooks listed under
[The mcp service](#the-mcp-service).

### Wire

One listener serves every protocol connect-go speaks: Connect (binary
proto and JSON), gRPC, and gRPC-Web, over HTTP/1.1 and unencrypted
HTTP/2 with prior knowledge (h2c) on the same port. Native gRPC needs
HTTP/2, and gets it without TLS. With `services.rpc.tls` on, HTTP/2 is
negotiated by ALPN instead and h2c is off
([TLS and client certificates](#tls-and-client-certificates)). The
`ready_reported` address is the base URL, `http://<host>:<port>`, or
`https://` under TLS; procedures live under
`/cmdsurface.v1.Commands/`. The listener is the service's own, for the
reasons the mcp service gives.

The listener carries the
[HTTP-plane middleware](#middleware-order-on-the-http-plane) every kit
listener does, configured by `services.rpc.*`. A refusal there is a
Connect error in the protocol the client spoke —
`permission_denied` for the Host and Origin checks,
`resource_exhausted` for the body limit — and ends the connection.

A request larger than the body limit — `services.rpc.body_limit`,
else `rpcserve.Config.MaxBodyBytes`, else 4 MiB, the gRPC
implementations' receive default — is refused with
`resource_exhausted` before it is decoded, on every protocol and for
streams alike. Connect's own read limit is set to the same number, so
no message is admitted by one layer and refused by the other beyond
its 5-byte envelope. Compression is Connect's, per message:
`services.rpc.compression` sets it, and the HTTP compressor passes
Connect, gRPC and gRPC-Web through, so nothing is compressed twice.
Off, the default, no response is compressed and a compressed request
is still read.

The server's write deadline (`timeouts.write`, 10s by default) is
sized for request/reply.
`InvokeStream` responses MUST be exempt from it: a stream outlives it
by design, and a stream cut at the deadline would end without its
terminal `done` event. Every other call keeps it. Stopping the service
ends every open stream, so a stream with no end of its own cannot hold
the drain for the whole stop budget.

### RPC keys and flags

| Key                                | Type   | Default          | Meaning                                                  |
|------------------------------------|--------|------------------|----------------------------------------------------------|
| `services.rpc.addr`                | string | `127.0.0.1:8082` | listen address; loopback unless authenticated or opted in |
| `services.rpc.insecure_remote`     | bool   | `false`          | serve unauthenticated on a non-loopback address          |
| `services.rpc.insecure_no_policy`  | bool   | `false`          | beyond loopback with no `--policy`, no policy rather than `kit-default` |

`rpcserve.Config` carries the code defaults under the same names. The
flag `--rpc-addr`, registered on `serve` like `--mcp-addr`, overrides
`services.rpc.addr` for one run. The insecure opt-ins have no flags,
for the reason the mcp service's have none.

### Identity and gates

`rpcserve.Config.Auth` is an `api.AuthFunc`, the type the api and mcp
services take, applied as a Connect interceptor to every procedure,
unary and streaming alike, before the handler runs. A refusal is
`unauthenticated` in the protocol the client spoke, plus an
`ErrAuthRefused` audit record naming the procedure.

Identity comes from what `Auth` verified, never from the request body.
The body's `meta.caller`, `meta.tenant` and `meta.extra` are claims: the
service drops them, so a client cannot name itself a principal or
write a `scopes` entry the permission gate would read as a
credential's. A leaf declaring `kit/auth-required` runs only when
`Auth` verified the call; a bare `Authorization` header is not
authentication, so without `Auth` such leaves are refused.

| Field            | rpc service                                                      |
|------------------|------------------------------------------------------------------|
| `Caller`         | principal from the `Auth` claims                                 |
| `Tenant`         | tenant from the `Auth` claims                                    |
| `Established`    | `verified` when `Auth` verified the call, else empty             |
| `Surface`        | `rpc`, pinned by the seam                                        |
| `RequestID`      | `X-Request-ID`, else the body's `request_id`, else issued        |
| `TraceID`        | `traceparent` trace-id, else `X-Trace-ID`, else the body's       |
| `IdempotencyKey` | `Idempotency-Key`, else the body's                               |
| `RequestedAt`    | receipt time                                                     |
| `Extra`          | `rpc_protocol` (`connect`, `grpc`, `grpcweb`), `remote_addr`, `peer_addr` (see [Client address](#client-address)), `scopes` |

What never runs remotely is withheld as the REST projection withholds
it: every command the reflector judges non-invocable under the Root's
reserved verbs — `interactive`, `management-only`, `self-hosting` — is
hidden from the surface and answers `not_found`. The other refusals
map onto Connect codes:

| Refusal                                               | Code                  |
|-------------------------------------------------------|-----------------------|
| unknown, withheld, or not exposed on `rpc`            | `not_found`           |
| `kit/auth-required` without verified `Auth`, or `Auth` refused | `unauthenticated` |
| `kit/requires-confirmation` without `X-Confirm-Token` | `failed_precondition` |
| destructive ceiling, permission gate                  | `permission_denied`   |
| oversized message                                     | `resource_exhausted`  |

A command that ran and exited non-zero is a success response carrying
its `exit_code`: the call reached the command, and the command
answered.

`rpcserve.Config.Interceptors` are the adopter's Connect interceptors.
They MUST run inside every gate above: a call `Auth`, the
`kit/auth-required` or confirmation gate, exposure, the destructive
ceiling or the permission gate refuses never reaches them. They wrap
the run of an admitted call, the first outermost. A call one of them
refuses does not run, answers with their error, and is audited with
it. The request they see is the body as sent; identity is read from
the verified claims, never from `meta`.

The service installs no CORS handling: a browser client on another
origin reaches it only through a proxy that answers CORS.
## Middleware

Middleware is kit-shipped, configuration-driven behavior wrapped around
a served invocation: authentication, limits, hardening, observability,
replay. Every middleware sits on one of the [two planes](#two-planes-not-one),
or declares one half on each. This section fixes, for all of them, the
order they run in, the configuration shape, the refusal vocabulary, and
what is on by default. Each middleware's own keys and behavior are
specified with it; nothing it specifies may contradict this section.

Authority: [`go/transport/cmdsurface`](../../go/transport/cmdsurface/)
for the invocation plane; [`go/transport/api`](../../go/transport/api/)
and [`go/transport/rpc`](../../go/transport/rpc/) for the HTTP plane.

### Declaring a middleware

A kit middleware MUST declare the fields below, and the change that
ships it MUST add its row to [the block registry](#block-registry):

| Field      | Declares                                                                   |
|------------|----------------------------------------------------------------------------|
| Block      | its configuration block name, `snake_case`, unique across kit              |
| Plane      | `http`, `invocation`, or both — a middleware with two halves declares each |
| Slot       | the position of each half in the order tables below                        |
| Reach      | the surfaces (invocation plane) or listeners (HTTP plane) it applies to    |
| Defaults   | on or off, and its values, on loopback and beyond loopback                 |
| Refusals   | the refusal codes it may produce, from [Refusals](#refusals)               |

Rules:

- A middleware runs at its slot and nowhere else. Adopters cannot
  reorder kit middleware, and a middleware MUST NOT be reachable at a
  second position through another mount path.
- Invocation-plane middleware applies to every remote surface — every
  surface except `cli` and `lib` — unless its row narrows the reach and
  says why. It never applies to `cli` or `lib`: the first is the
  operator's own act, the second has no caller.
- HTTP-plane middleware applies to every kit HTTP listener: the api
  service's router, the RPC server, and the mcp service's HTTP
  listener. Socket and stdio have no HTTP plane; what they need from
  it they get from their own transport (the socket's line bound and
  `0600` file, the stdio spawn).
- A transport MUST NOT re-implement an invocation-plane middleware. It
  receives every gate with its `Invoker`, as it already receives the
  destructive ceiling and the permission gate.
- A streaming invocation MUST pass the same gates, in the same order,
  before the runner streams. Streaming is a way of observing a call,
  never a way around its gates.
- Adopter code extends at two points only. On the HTTP plane, adopter
  routes and per-route middleware run inside the route, after kit
  authentication, so no adopter middleware sits in front of kit's
  checks. On the invocation plane, the adopter's `PermissionFunc` runs
  at the permission slot, and a wrapped `Runner` runs innermost, after
  every gate.
- Every gate that keys on the caller — rate limit, idempotency, the
  result cache, quota — scopes an established caller the same way,
  decided in one place in `cmdsurface`: a verified principal
  (`EstablishedVerified`) by its tenant and principal, a caller the
  transport vouches for (`EstablishedTransport`) as
  `transport/<surface>`, never by the name or tenant it claims. They
  differ only in what an unestablished call shares, each for its own
  reason: the rate limit and quota key it on the client address, then
  on the surface, so a claim never splits a bucket (`bus` and `cron`
  invocations therefore share one bucket per surface); idempotency
  isolates its records by the claim; the cache answers it from the
  anonymous entry.

### Gate order on the invocation plane

Before the bridge, the transport's **edge** decodes the request,
verifies a credential if one was presented, and fills `Meta`. A
presented credential that fails verification is refused there as
`unauthenticated` and audited with `ErrAuthRefused`, whatever the leaf.

Inside the bridge, in this order. `Bridge.Invoke` is `Bridge.Admit`
followed by `Admission.Run`; a transport that streams calls the same
two halves, running with `Admission.Stream`:

| #  | Gate                    | Refuses with                                          | Block          |
|----|-------------------------|-------------------------------------------------------|----------------|
| 1  | Resolution              | `unknown_command`                                     | —              |
| 2  | Enablement              | `not_enabled`                                         | —              |
| 3  | Invocability            | `not_invocable`                                       | —              |
| 4  | Authentication required | `unauthenticated`                                     | `auth`         |
| 5  | Destructive ceiling     | `destructive_blocked`                                 | —              |
| 6  | Permission              | `insufficient_scope`, `permission_denied`             | —              |
| 7  | Rate limit              | `rate_limited`                                        | `rate_limit`   |
| 8  | Replay                  | `idempotency_conflict`, `idempotency_key_reused`      | `idempotency`, `cache` |
| 9  | Quota                   | `quota_exceeded`                                      | `quota`        |
| 10 | Confirmation by a person | `confirmation_required`                               | —              |
| 11 | Capacity                | `overloaded`                                          | `concurrency`  |
| 12 | Run                     | `deadline_exceeded`; the command's own exit codes     | `timeouts`     |
| 13 | Record                  | never refuses                                         | `idempotency`, `quota`, `audit` |

Slots 1–9 are the machine gates, and run in `Bridge.Admit`. Slot 10
belongs to the surface, between `Admit` and the run: the mcp service
asks its person there ([Confirmation](#confirmation)), and a surface
that asks nobody passes through it. Slots 11–13 run in
`Admission.Run` or `Admission.Stream`; the `Admission` value `Admit`
returns carries a call through them.

What each slot does:

- **4.** A leaf declaring `kit/auth-required` runs only when `Meta`
  carries an identity the transport *established*: verified by a
  configured verifier, or proven by the transport itself (the socket's
  owner-only file, the stdio spawn). `Meta` MUST distinguish an
  established identity from a claimed one, and this gate reads only
  the former: `cmdsurface.Meta.Established`, `verified` or
  `transport` (see [Provenance](#provenance)). The gate applies on
  every remote surface, whatever mount serves it, deprecated ones
  included; a transport with no verifier configured establishes
  nobody, so such a leaf is refused there. The refusal returns `ErrAuthRefused`, the sentinel the
  edge already uses, so one class has one sentinel.
- **6.** Up to four deciders, each able only to narrow, and none asked
  about a call an earlier one refused: the built-in scope check (a
  leaf's `kit/permissions` against the caller's scopes, refusing
  `insufficient_scope`), then the `--policy` engine's `allow` lists,
  then the policy's `permissions:` rules, then the adopter's
  `PermissionFunc`. The rules are CEL, the rule language of
  `runtime/policy`, evaluated by the evaluator it uses
  (`celpermission.With()`); they read the caller, tenant, verified
  scopes, establishment, command path, side-effect tier, surface,
  client address, args and flags. Every rule MUST compile when the
  service starts — one that does not, or rules with no evaluator
  wired, refuse the start as a usage error (exit 2) naming the rule.
  Across rules deny overrides, and a rule that fails to evaluate
  denies. A rule refusal is `permission_denied` with the reason
  `permission rule "<name>": <message>`, which the audit record
  carries. The scope check applies on remote
  surfaces to a leaf that declares `kit/permissions`, and requires
  every scope it lists, matched exactly. The caller's scopes are
  those of an identity `Meta.Established` marks `verified`, carried as
  `Meta.Extra["scopes"]`; an unestablished caller holds none, whatever
  its request claimed. A `transport`-established caller holds the
  owner's authority and is not asked: whoever can speak on an
  owner-only socket or a spawned process's pipes could run the
  command from the CLI, where no scope is asked for. The `--policy`
  engine answers for the established caller and checks a caller
  rule's `max_ops` budget; the budget is charged only once the whole
  slot, adopter included, admits the call (see
  [Permission](#permission)).
- **8.** A hit answers from a store and runs nothing: an idempotency
  replay for a call carrying a key the store has seen from the same
  principal, or a read-tier cache hit for a leaf declaring
  `kit/cache-ttl`. Idempotency is consulted first
  ([Idempotency](#idempotency)). A miss continues. The cache keys the
  identity the transport established, scoped as idempotency scopes it
  (`cmdsurface.IdempotencyScope`) plus a verified caller's scopes: a
  caller the transport vouches for is keyed as `transport/<surface>`,
  so a name it claims never reads that principal's entry. A claimed
  caller, tenant or scopes is keyed as anonymous, so a command whose
  output depends on who calls requires an established identity
  (`kit/auth-required`) or declares no `kit/cache-ttl`.
- **9.** The `quota` block: calls (`ops`) and output bytes (`bytes`:
  stdout, stderr and structured data) per caller per fixed `window`
  (default `1h`, aligned to the epoch, so `24h` is a UTC day), counted
  `per` principal (with its tenant) or tenant; the caller is resolved
  as the rate limit resolves it. The block is on when a limit is set,
  unless `enabled: false`; `enabled: true` with no limit, a window
  under a second, a negative limit or an unknown `per` is refused at
  validation, exit `2`. A call is refused when the window has already
  counted its limit; slot 13 counts a run that completed without
  error, so the call that crosses a limit completes and a replay or a
  cache hit counts nothing. Counts live in the usage store
  (`$XDG_STATE_HOME/<tool>/usage.db`, or `cli.WithUsageStore`), per
  service, so a restart resets nothing. `quota show` and
  `quota reset`, mounted by `cli.WithQuotaCommand`, are kit-reserved
  and so `management-only` on every served surface. A store that
  cannot be read refuses the call.
- **10.** The confirmation a surface obtains from a person: an MCP
  elicitation, an `X-Confirm-Token`. The command's own `--confirm`
  gate is unchanged; it runs inside the command, at 12, and answers
  with an exit code in the `Result`.
- **11.** An in-flight slot, or a place in a bounded queue (see
  [Capacity](#capacity)). The per-command deadline (`kit/timeout`,
  else `timeouts.command`) is armed here and covers queue wait and
  execution.
- **13.** On success, the idempotency record and quota usage are
  written. Every verdict of 1–12 reaches the audit sinks, redacted
  before any sink sees it.

Why this order:

- **Identity before authority (4 before 5 and 6).** An unauthenticated
  caller gets `401`, never a `403` that describes the deployment's
  policy. Slots 1–3 disclose nothing discovery does not already list.
- **Caller-independent before caller-dependent (5 before 6).** The
  ceiling is static and free; the permission gate may consult and
  charge per-caller state (`max_ops` budgets, per-caller policy). A
  call the ceiling refuses must not charge a budget. This keeps the
  shipped order.
- **Decide before counting (6 before 7 and 9).** Only a call policy
  would admit consumes a rate token or quota. Flooding before
  authentication is the HTTP plane's to bound — timeouts, body limit,
  connection bounds — and a verifier SHOULD bound its own per-request
  cost.
- **Cheap before persisted (7 before 9).** The rate limiter is memory;
  quota is storage. A burst is turned away before it reaches storage.
- **Replay before spending (8 before 9–12).** A replay does no work,
  so it consumes no quota, asks no person, and holds no slot. It still
  passes permission — a revoked caller cannot replay — and still
  counts as a request against the rate limit.
- **Machines before people (10 after 1–9).** A person is never asked
  to approve a call a machine gate would refuse, and the rate limit
  precedes the question, so no caller can flood a person with
  prompts.
- **Slots only for work that will run (11 after 10).** A call waiting
  on a person holds no concurrency slot, and the deadline does not
  run while a person decides; the surface bounds that wait itself.

### Middleware order on the HTTP plane

Outermost first, on every kit HTTP listener:

| #  | Middleware                   | Refuses with                         | Block              |
|----|------------------------------|--------------------------------------|--------------------|
| 1  | Request id                   | —                                    | —                  |
| 2  | Client address               | —                                    | `trusted_proxies`  |
| 3  | Access log                   | —                                    | —                  |
| 4  | Recovery                     | —                                    | —                  |
| 5  | Tracing and metrics          | —                                    | `tracing`, `metrics` |
| 6  | Security headers             | —                                    | `security_headers` |
| 7  | Health endpoints             | — (terminal)                         | `health`           |
| 8  | Host and Origin checks, then the metrics endpoint | `host_rejected`, `origin_rejected`; the endpoint is terminal | `host_check`, `origin_check`; `metrics` |
| 9  | CORS                         | —                                    | `cors`             |
| 10 | Body limit                   | `body_too_large`                     | `body_limit`       |
| 11 | Compression                  | —                                    | `compression`      |
| 12 | Authentication (edge)        | `unauthenticated`                    | `auth`             |
| 13 | Route: adopter routes, projection, per-route middleware → `Invoker` | invocation plane | — |

Slots 8–12 wrap the listener's router as a whole, never route by
route. Every request past slot 7 passes them, whatever it addresses: a
projected or adopter route, a route a library registers on the mux
directly (the OpenAPI document, the docs UI, schemas, capabilities),
or a path that matches nothing. The one exception is a scrape, which
the metrics endpoint answers once slot 8's checks have passed, ahead
of 9–12. With `auth` configured, then, the OpenAPI document and the
discovery listing require credentials like any other route; the only
unauthenticated answers are the health probes and the scrape. A
deployment that publishes its OpenAPI document does so by having its
verifier admit that path. Per-route middleware exists only at 13.

Server settings sit outside the chain and have no slot: `timeouts`
(read header, read, write, idle — stream routes exempt from the write
deadline; see [Timeouts](#timeouts)) and `tls`.

Why this order:

- The request id and the real client address come first, so every
  log line, metric, audit record, and refusal below carries both.
- Telemetry wraps every refusal, so refused requests are measured by
  code. Security headers wrap them too, so a refusal is not the one
  response without `nosniff`.
- Health endpoints answer before the Host check because orchestrator
  probes address a pod by IP, not by name, and a health answer
  discloses nothing a Host allowlist protects. They bypass everything
  from 8 on, and are never audited or rate limited.
- The metrics endpoint (`metrics.scrape`) answers after the Host and
  Origin checks, not beside the probes: it discloses command names,
  surfaces and refusal counts, so a DNS-rebinding page MUST NOT read
  it, and a scraper already sends a host the check allows — on a
  loopback bind the derived hosts are the ones a local scraper uses,
  and a wildcard bind derives no restriction (with `host_check.allow`
  set, the scraper's target has to be on that list, like any caller's). It still
  answers before CORS, the body limit, compression and
  authentication, because a scraper carries no credentials. For what
  it discloses, it is off by default, and on a non-loopback bind it
  MUST be refused at validation, exit `2`, unless
  `metrics.scrape.allow_remote` is true — authentication does not
  waive this, because the endpoint never sees it. An adopter route at
  exactly its path wins, as for the probes.
- The Host and Origin checks run before CORS and authentication: a
  rebinding or cross-origin request is refused before anything else
  reads it. CORS answers preflights before authentication, because a
  preflight carries no credentials.
- The body limit precedes authentication because it costs nothing
  and reveals nothing; authentication reads headers, never the body.

The api service's router, the rpc server and the mcp service's HTTP
listener build this chain with one constructor, each from its own
`services.<svc>.*` blocks. Slots 1–11 are the same middleware on all
three; slot 12 is each listener's own — an HTTP middleware on the api
and mcp listeners, a Connect interceptor on the RPC server. Where a
protocol layer keeps a copy of a slot, the copy is set from the same
block, so the two never disagree: on the RPC server, Connect's read
limit (`body_limit`) and per-message compression (`compression`,
which the HTTP compressor leaves to Connect); on the mcp listener, the
SDK's body cap (`body_limit`) and its loopback DNS-rebinding check,
which stays beneath kit's Host check and steps aside when
`host_check` admits other names or is switched off. A refusal decided
on this plane is written in the listener's protocol (see
[Refusals](#refusals)).

### Middleware configuration

Each block lives in the service's own block, and MAY be set once for
every service under `services.all`:

```yaml
services:
  all:                      # defaults for every service
    rate_limit:
      enabled: true
  api:
    addr: 0.0.0.0:8443
    body_limit:
      max_bytes: 4194304    # this service only
    rate_limit:
      enabled: false        # overrides services.all for api only
```

Rules:

- `services.<svc>.<block>.<key>` MUST work on its own. `services.all`
  is a convenience, never a requirement.
- `all` is already a reserved service name, so `services.all` can
  never collide with a service. Only middleware blocks are read under
  it. A lifecycle key or service-owned key there (`enabled`, `addr`,
  `path`, the insecure opt-ins) MUST be refused at validation, exit
  `2`, rather than silently ignored.
- Resolution is **per key, specificity before source**: the service's
  key from any source (flag, env, file), then the `services.all` key,
  then the code option, then the kit default for the exposure (below).
  An operator who scoped a value to one service meant that service,
  even when a shared value arrives from a higher-precedence source.
- Blocks merge key by key. Setting one key under `services.api.rate_limit`
  keeps the others from `services.all.rate_limit`. A list value
  replaces, never concatenates: a merged allowlist is one nobody
  wrote.
- Every block that can be switched off has an `enabled` key. A
  security floor — redaction of secret flags, the exposure rules
  above — has no off switch, and its block carries no `enabled` key.
- An unknown key inside a registered block is a configuration failure
  at exit `2`. A misspelled key that silently leaves a limit off is
  the failure this prevents.
- A list-of-entries key (`audit.sinks`) is checked for shape the same
  way: each entry is a bare type or a map of the keys an entry
  accepts, and anything else — a map where the list belongs, an
  unknown entry key — is refused at exit `2`.
- The block names in the registry are reserved inside **every**
  `services.<svc>` block, the adopter's services included, because
  invocation-plane middleware reaches every transport service.
- Environment variables follow the kit convention:
  `MYTOOL_SERVICES_API_BODY_LIMIT_MAX_BYTES`,
  `MYTOOL_SERVICES_ALL_RATE_LIMIT_ENABLED`.
- Middleware keys have no flags unless a block's row names one. A
  limit is a deployment decision; configuration is where a reviewer
  finds it, and per-service flags are refused under the supervisor
  form anyway.

There is no `middleware` segment in the path. The api service's `tls`
block and its `addr` key are equally "how this service is served", an
operator never needs to know which one kit implements as middleware,
and the segment would lengthen every key and variable for nothing.

### Block registry

"Loopback" is the literal-host rule of [Exposure](#exposure). The
socket service and stdio take the loopback column.

| Block              | Plane · slot                           | Reach                                  | Loopback default                    | Beyond loopback default                        | Refusals |
|--------------------|----------------------------------------|----------------------------------------|-------------------------------------|------------------------------------------------|----------|
| `auth`             | http 12, socket/stdio edge; invocation 4 | every remote surface                  | no verifier; socket and stdio identity satisfy slot 4 | required, per [Exposure](#exposure)            | `unauthenticated` |
| `tls`              | http server                            | HTTP listeners                         | off                                 | off                                            | — |
| `timeouts`         | http server; invocation 11–12          | HTTP listeners; every remote surface   | read header 5s, read 5s, write 10s; no command deadline | same                                           | `deadline_exceeded` |
| `trusted_proxies`  | http 2                                 | HTTP listeners                         | empty: no forwarded header trusted  | empty                                          | — |
| `tracing`          | http 5; invocation (propagation)       | HTTP listeners; every remote surface   | propagate, export nothing           | same                                           | — |
| `metrics`          | http 5; endpoint at the inner end of http 8 | HTTP listeners; every remote surface; endpoint (`metrics.scrape`) on HTTP listeners | off; endpoint off               | off; endpoint off, and refused without `scrape.allow_remote` | — |
| `security_headers` | http 6                                 | HTTP listeners                         | on; HSTS only over TLS: `tls`, or https from a trusted proxy | on                                             | — |
| `health`           | http 7                                 | HTTP listeners                         | on                                  | on                                             | — |
| `host_check`       | http 8                                 | HTTP listeners                         | on, allowlist from the bound host   | on; a wildcard bind derives no restriction     | `host_rejected` |
| `origin_check`     | http 8                                 | HTTP listeners                         | on, same-origin                     | on, same-origin                                | `origin_rejected` |
| `cors`             | http 9                                 | HTTP listeners                         | off                                 | off                                            | — |
| `body_limit`       | http 10                                | HTTP listeners                         | on, 1 MiB; rpc 4 MiB                | on, 1 MiB; rpc 4 MiB                           | `body_too_large` |
| `compression`      | http 11                                | HTTP listeners; never `text/event-stream`; per message on the RPC server | off                   | off                                            | — |
| `rate_limit`       | invocation 7                           | every remote surface                   | off                                 | on, per-tier limits documented with the block  | `rate_limited` |
| `idempotency`      | invocation 8, 13                       | every remote surface                   | on; inert without a key             | on                                             | `idempotency_conflict`, `idempotency_key_reused` |
| `cache`            | invocation 8; HTTP renders ETag, `304`, `Cache-Control` | `rest`, read tier only   | inert without `kit/cache-ttl`       | same                                           | — |
| `quota`            | invocation 9, 13                       | every remote surface                   | off                                 | off                                            | `quota_exceeded` |
| `concurrency`      | invocation 11                          | every remote surface                   | on, bounded queue (32 in flight, 64 queued) | on, bounded queue (32 in flight, 64 queued) | `overloaded` |
| `audit`            | invocation 13                          | every remote surface                   | the registered sinks; redaction always | same                                        | — |

"HTTP listeners" are the api service's router, the mcp service's HTTP
transport and the rpc server; each applies every block in its row
under its own `services.<svc>`. A block whose reach is HTTP listeners
alone — `security_headers`, `health`, `host_check`, `origin_check`,
`body_limit`, `compression`, `trusted_proxies`, `metrics.scrape`,
`tls`, `tls.acme`, `auth.mtls`, `auth.jwt`, `auth.jwks`,
`auth.oidc` and `auth.apikey` — set for a kit-shipped service with no HTTP listener
(the socket service) would act on nothing, so it is refused at validation, exit `2`, rather than ignored. Under
`services.all` it is a default, and a service it does not reach
simply does not read it.

The same rule holds for a key or a value that only some services
apply, as the table below states: under the socket, the server keys
of `timeouts` and the HTTP credential modes of `auth.mode` (`mtls`,
`jwt`, `jwks`, `oidc`, `apikey`); under the HTTP listeners,
`auth.mode: peer`; under every service but `api`, the `cache` block,
and under every service but `socket`, the `auth.peer` block, the
adopter's services included. Each is refused
at validation, exit `2`, and each stays a default under
`services.all`.

Which kit-shipped services apply each configurable block. "Bridge
services" are `api`, `mcp`, `rpc`, `socket`, and an adopter's service
built with `cli.ServeBridgeOptions`. The mcp service over stdio
reads no HTTP-listener key.

| Block                                   | Applied by                                     | Refused under            |
|-----------------------------------------|------------------------------------------------|--------------------------|
| `auth` (`mode`)                         | `mtls`, `jwt`, `jwks`, `oidc`, `apikey`: api, mcp over HTTP, rpc; `peer`: socket | those modes: socket; `peer`: api, mcp over HTTP, rpc |
| `auth.peer`                             | socket                                         | every other service      |
| `auth.mtls`, `auth.jwt`, `auth.jwks`, `auth.oidc`, `auth.apikey`, `tls`, `tls.acme` | api, mcp over HTTP, rpc | socket |
| `timeouts` `read_header`, `read`, `write`, `idle` | api, mcp over HTTP, rpc              | socket                   |
| `timeouts` `command`                    | bridge services                                | —                        |
| `tracing`, `metrics`                    | HTTP half: api, mcp over HTTP, rpc; invocation half: bridge services | —  |
| `metrics.scrape`, `security_headers`, `health`, `host_check`, `origin_check`, `body_limit`, `compression`, `trusted_proxies` | api, mcp over HTTP, rpc | socket |
| `rate_limit`                            | bridge services                                | —                        |
| `idempotency`                           | bridge services                                | —                        |
| `cache`                                 | api                                            | every other service      |
| `quota`                                 | bridge services                                | —                        |
| `concurrency`                           | bridge services                                | —                        |
| `audit`, `audit.redact`                 | bridge services                                | —                        |

A block in the registry with no row here has no configuration keys
yet.

The defaults follow one rule. On loopback the caller is already on
the machine, so what is on protects the machine from browsers and
from unbounded work — Host and Origin checks, headers, the body limit,
a bounded queue — and nothing polices the caller. Beyond loopback,
the rate limit joins them, on top of the authentication and policy
the exposure rules already demand. Nothing exports telemetry by
default, anywhere.

Plain `tls` does not satisfy the authentication requirement beyond
loopback: it proves the server to the client, not the client to the
server. `auth.mode: mtls` does.

Permission and confirmation have no block. Permission is configured
by `--policy` and `cli.WithPermission`; confirmation belongs to the
surface that asks the person.

### Client address

Slot 2 decides who the client is. Without configuration it is the
immediate peer, and forwarding headers are the caller's say-so,
never read. Behind a reverse proxy every caller is the proxy until
the operator names it:

```yaml
services:
  all:
    trusted_proxies: [10.0.0.0/8, "2001:db8::/32"]
  api:
    trusted_proxies: [127.0.0.1]   # replaces the shared list for api
```

`trusted_proxies` is a list, not a block of keys: CIDRs and single
addresses, IPv4 or IPv6. The service's list replaces the
`services.all` list; a string is one entry or several, comma or space
separated (`MYTOOL_SERVICES_ALL_TRUSTED_PROXIES="10.0.0.0/8 ::1"`).
An entry that is neither, or a map where the list belongs, is refused
at validation, exit `2`, naming the key. Empty trusts no proxy.

- Forwarding headers MUST be read only when the immediate peer lies
  in the list. From any other peer they are ignored, whatever they
  say.
- From a trusted peer, the header is walked right to left — each
  entry was appended by the hop to its right — past every trusted
  address; the first untrusted address is the client. When every
  entry is trusted, the leftmost is.
- The headers are `Forwarded` (RFC 7239, its `for=`),
  `X-Forwarded-For`, and `X-Real-IP` only when neither of the others
  is present. Nodes may carry a port; IPv6 nodes are bracketed in
  `Forwarded` and may be bare in `X-Forwarded-For`; an IPv4-mapped
  IPv6 address is its IPv4 address.
- A header is believed whole or not at all. An entry the walk reaches
  that is not an address (`unknown`, an obfuscated node, garbage), a
  `Forwarded` element without `for=`, or two `X-Real-IP` values leave
  the peer as the client. So do `Forwarded` and `X-Forwarded-For`
  naming different clients: a proxy that sets one and passes the
  other through from the client would otherwise let the client choose
  its address. Configure the proxy to set one and strip the other.
- The scheme the client used is taken only from a trusted peer: the
  `proto=` of the `Forwarded` element naming the client, else
  `X-Forwarded-Proto` — a single value, or the value aligned with the
  client's `X-Forwarded-For` entry. It decides HSTS: `security_headers`
  sends it when the request arrived over TLS on the listener or a
  trusted proxy forwarded `https`.
- The resolved client is the request's address for everything below
  slot 2: the access log, the span's client address, the rate
  limiter's key and every audit record, as `Extra["remote_addr"]` —
  the bare IP for a forwarded client, `ip:port` for a peer. When a
  trusted proxy forwarded the client, `Extra["peer_addr"]` records
  the proxy's own address. Headers are left in place for adopter
  routes.
- The Host check still reads `Host`: kit does not honor
  `X-Forwarded-Host`. A proxy that rewrites `Host` has its upstream
  name listed in `host_check.allow`.

### TLS and client certificates

`tls` and `auth.mode: mtls` configure the kit HTTP listeners — the api
service, the rpc service, the mcp service's HTTP transport — under
`services.<svc>` or `services.all`, resolved like any block. Under
`services.socket` they are refused at validation, exit `2`, as every
block that acts on an HTTP listener alone is; the mcp service's stdio
transport does not read them. `cli.ResolveServeTLS` is the one
resolver, and `ServeTLS.Serve` the one place a listener starts
serving.

| Key                            | Type   | Default                         | Meaning |
|--------------------------------|--------|---------------------------------|---------|
| `tls.enabled`                  | bool   | on when a certificate source is set | serve TLS only |
| `tls.cert_file`, `tls.key_file` | string | —                              | PEM certificate chain and key; one certificate source |
| `tls.min_version`              | string | `1.2`                           | `1.2` or `1.3`; nothing lower is accepted |
| `tls.acme.enabled`             | bool   | on when `domains` is set        | obtain and renew certificates by ACME; the other source |
| `tls.acme.domains`             | list   | —                               | names certificates are issued for; no other name gets one |
| `tls.acme.cache_dir`           | string | `<state dir>/<tool>/acme`       | account key and certificates |
| `tls.acme.email`               | string | —                               | contact the CA may use |
| `tls.acme.directory_url`       | string | Let's Encrypt production        | another ACME directory (a staging one, a private CA) |
| `auth.mode`                    | string | unset                           | `mtls`: the client certificate is the credential; `jwt`, `jwks`, `oidc`: a bearer token is (see [Bearer tokens](#bearer-tokens)); `apikey`: a kit-issued API key is (see [API keys](#api-keys)); `peer` is the socket's ([Socket peer credentials](#socket-peer-credentials)) |
| `auth.mtls.ca_file`            | string | — (required under `mtls`)       | PEM bundle client certificates must chain to |
| `auth.mtls.principal`          | string | `san`                           | `san` (first URI, else DNS, else email SAN), `san_uri`, `san_dns`, `san_email`, `cn` |
| `auth.mtls.tenant_oid`         | string | —                               | dotted OID of a subject attribute, else of an extension holding a string |
| `auth.mtls.tenant_san_pattern` | string | —                               | RE2 matched against the SANs; the first capture group of the first match, else the whole match |

- With TLS on, the listener offers HTTP/2 and HTTP/1.1 by ALPN and
  nothing in plaintext; the rpc service's h2c is off. The readiness
  address carries `https`. `security_headers` sends HSTS on every
  response to a request that arrived over it.
- ACME answers the TLS-ALPN-01 challenge on the listener itself, so
  the CA must reach it on port 443 under every name in `domains`.
  Certificates from files load at start; a replaced file takes effect
  on the next start.
- `auth.mode: mtls` needs TLS on and `ca_file`. The listener asks every
  client for a certificate, and a certificate that does not chain to
  the bundle ends the handshake. A request with no certificate reaches
  the HTTP plane and is refused at slot 12 as `unauthenticated`,
  audited like a missing token, so health probes still answer without
  one. A certificate with no principal where `principal` says is
  refused the same way.
- The certificate's principal and tenant are the call's `Caller` and
  `Tenant`: an established identity for slot 4 and for the exposure
  rules. The mode selects the verifier: under `mtls` the listener's
  code `Auth` (`APIConfig.Auth`, `rpcserve.Config.Auth`,
  `mcpserve.Config.Auth`) is not consulted, and the api service's
  `--no-auth` disables it as it disables `Auth`.
- A handshake that fails is logged by the server and not audited: no
  request exists to attribute.
- Each of these is refused at validation, exit `2`, naming the key:
  `enabled: true` with no certificate source, both sources, one of
  `cert_file` and `key_file`, a pair that does not load, an unsupported
  `min_version`, ACME with no domain, an unknown `auth.mode`, `mtls`
  without TLS or without `ca_file`, a bundle with no certificate, an
  unknown `principal`, both tenant sources, an OID or pattern that does
  not parse, and an `auth.mtls` key under another mode.

### Bearer tokens

`auth.mode: jwt`, `jwks` or `oidc` makes a bearer token the credential
on the kit HTTP listeners: `Authorization: Bearer <JWT>` on every
request, verified by `go/transport/authn`. Each mode reads the block of
its name under `services.<svc>.auth` or `services.all.auth`:

| Key                        | Modes            | Default                       | Meaning |
|----------------------------|------------------|-------------------------------|---------|
| `auth.jwt.public_key_files` | `jwt`           | —                             | PEM public keys (Ed25519, RSA, ECDSA) trusted beside the tool's identity key |
| `auth.jwks.url`            | `jwks`           | — (required)                  | the JSON Web Key Set; `https`, or `http` to a loopback host |
| `auth.oidc.issuer`         | `oidc`           | — (required)                  | the provider; discovery at `<issuer>/.well-known/openid-configuration` yields its `jwks_uri` |
| `<block>.issuer`           | `jwt`, `jwks`    | —                             | the token's `iss` must equal it (under `oidc`, the issuer always is checked) |
| `<block>.audience`         | all              | — (required under `jwks`, `oidc`) | the token's `aud` must include one of these |
| `<block>.clock_skew`       | all              | `1m`                          | tolerance on `exp`, `nbf`, `iat`; `0` tolerates none |
| `<block>.refresh`          | `jwks`, `oidc`   | `1h`                          | how long a fetched key set is used before it is fetched again |
| `<block>.tenant_claim`     | all              | `tenant`                      | the claim the tenant is read from |

- `jwt` trusts the tool's own identity keypair (`cli.WithIdentity`) and
  every key in `public_key_files`; with neither it is refused. A
  token's `kid` selects the key; a key left in `public_key_files` after
  rotation keeps verifying what it signed.
- `token create` (mounted with `WithAPI` and `WithIdentity`) signs a
  token `jwt` accepts: EdDSA, the keypair's `kid` in the header, `sub`,
  `exp`, and optionally `tenant`, `aud`, `iss` and the scopes, written
  as `scope` (space-delimited) and `scopes` (a list). `token verify
  <token>` checks one as `--service` (default `api`) would: exit `0`
  valid, `5` refused, `6` key set unreachable, `2` nothing to verify
  with. The `token` command is kit-reserved and never served.
- `jwks` and `oidc` fetch nothing at start. The key set is fetched on
  the first request that needs it, again once older than `refresh`,
  and again — at most once a minute — when a token names a `kid` the
  cached set lacks. A refetch that fails keeps the last good set; a
  set never fetched answers `unauthenticated`. The discovery document
  must name the configured issuer exactly.
- `audience` is required for `jwks` and `oidc`: a third-party issuer
  mints tokens for every application it serves, and without it a
  token minted for any of them would be accepted.
- A token is accepted when it is signed under an asymmetric algorithm
  (never `none`, never HMAC) by a trusted key, carries `exp` and `sub`,
  is inside `exp`/`nbf`/`iat` with the skew, and matches `issuer` and
  `audience` where set. Anything else is refused at slot 12 as
  `unauthenticated`, audited like any refusal, with the
  `WWW-Authenticate: Bearer` challenge.
- The claims are the call's identity: `sub` is `Caller`, the tenant
  claim `Tenant`, and the scopes — `scopes` (a list), else `scope`
  (space-delimited, RFC 8693), else `scp` — `Meta.Extra["scopes"]`.
  `Established` is `verified`.
- The mode selects the verifier. Under any mode the listener's code
  `Auth` (`APIConfig.Auth`, `rpcserve.Config.Auth`,
  `mcpserve.Config.Auth`) is not consulted; with `auth.mode` unset it
  applies as before. The api service's `--no-auth` disables either.
- A service names one mode. A key of another mode's block under the
  service is refused at validation, exit `2`; one under
  `services.all` is refused too while the mode in force is the shared
  one, and is a default the service does not use once the service
  names its own mode.
- Each of these is refused at validation, exit `2`, naming the key:
  `jwt` with no key, a key file that does not load, `jwks` without a
  URL, `oidc` without an issuer, a URL that is neither `https` nor
  loopback `http`, `jwks` or `oidc` without an audience, a skew or
  refresh that does not parse, a negative skew, and a refresh that is
  not positive.
- A bearer token sent over plain HTTP beyond loopback can be replayed
  by anyone who sees it; serve such a listener with `tls`.
- **Protected resource metadata (RFC 9728).** Under `jwt`, `jwks` or
  `oidc`, when the block names an authorization server (`oidc`'s
  `issuer`, or the block's `issuer`) and an `audience` that is an
  absolute URL, the api service and the mcp service's HTTP transport
  describe themselves as an OAuth protected resource: the resource is
  the first such audience, which every token must already carry; the
  document `{resource, authorization_servers, bearer_methods_supported}`
  answers at `/.well-known/oauth-protected-resource` followed by the
  resource's path, without a token, ahead of slot 12, CORS-open; and
  every `401` carries `WWW-Authenticate: Bearer
  resource_metadata="<origin>/.well-known/oauth-protected-resource<path>"`.
  This is the MCP authorization flow at the edge; a client refused
  there finds the authorization server. Without an issuer or a URL
  audience there is no document and the challenge stays `Bearer`.
  `mcpsdk.WithProtectedResource` and `cmdsurface.WithMCPProtectedResource`
  give a hand-mounted surface the same answers, pinned by the wire
  fixture `go/transport/api/testdata/protected-resource-wire.json`.
- An audience compares exactly except for a trailing slash, as RFC 8707
  resource indicators and the `aud` issuers mint from them disagree on
  it routinely.

### API keys

`auth.mode: apikey` makes a kit-issued API key the credential on the
kit HTTP listeners, sent as `X-API-Key: <key>` or
`Authorization: Bearer <key>` (`X-API-Key` wins when both are sent).

| Key                  | Default                        | Meaning |
|----------------------|--------------------------------|---------|
| `auth.apikey.backend` | `APIKeysConfig.Backend`, else `sqlite` | the kv backend holding the keys: `sqlite` or `badger`; its driver must be imported |
| `auth.apikey.path`    | `APIKeysConfig.Path`, else `<data dir>/<tool>/apikeys.db` | the store's file (sqlite) or directory (badger) |

- A key is `kit_<id>_<secret>`: a 64-bit id and a 256-bit secret,
  hex. The store keeps, per id, the principal, tenant, scopes,
  creation, expiry, revocation, and a SHA-256 of the domain-separated
  id and secret, compared in constant time. The secret is never
  stored and is shown once, at issue. No slow KDF: a 256-bit random
  secret cannot be guessed, and a KDF would only cost every request.
- `cli.WithAPIKeys` mounts `token key create --sub [--tenant]
  [--scopes] [--expires]`, `token key list` and `token key revoke
  <id>` (exit `3` for an unknown id); each opens the store of
  `--service` (default `api`). Revocation is immediate: the service
  reads the store on every request. A revoked key stays listed.
- The key's principal, tenant and scopes are the call's `Caller`,
  `Tenant` and `Meta.Extra["scopes"]`; `Established` is `verified`.
  An unknown, malformed, revoked or expired key is `unauthenticated`
  at slot 12, audited. A store that cannot be read refuses the
  request without judging the key.
- The store is opened on the first request and closed when the
  listener stops serving; `sqlite` is shared with the `token key`
  verbs of another process, `badger` locks it, so run them while the
  service is stopped.
- Refused at validation, exit `2`, naming the key: a backend other
  than `sqlite` or `badger`, a backend whose driver is not imported,
  and an `auth.apikey` key under another mode.

### Socket peer credentials

`services.socket.auth.mode: peer` makes the kernel's account of the
connection the socket caller's identity, through
`socket.NewPeerAuthenticator`. The socket service resolves it, and
`auth.peer`, from `services.socket` and `services.all`.

| Key                          | Type | Default | Meaning |
|------------------------------|------|---------|---------|
| `auth.mode`                  | string | unset | `peer`: the peer's credentials are the credential |
| `auth.peer.require_same_uid` | bool | `false` | refuse a peer whose uid is not the server process's |
| `auth.peer.resolve_names`    | bool | `false` | the principal is the user name, not `uid:<n>` |
| `auth.peer.scopes`           | list | unset | the scopes every admitted peer holds |

- The principal is `uid:<n>`, the peer's effective uid as of connect
  (`SO_PEERCRED` on Linux, `LOCAL_PEERCRED` on macOS and FreeBSD), or
  the user name under `resolve_names`, `uid:<n>` when the uid has no
  user entry. The tenant is empty. `Established` is `verified`, so a
  `kit/auth-required` leaf runs.
- `Extra` carries `peer_uid`, `peer_gid` and, where the platform
  reports it, `peer_pid`, on an admitted and a refused request alike.
  Audit redaction treats them as provenance.
- An admitted peer's scopes are `auth.peer.scopes` when set, else
  what `SocketConfig.PeerScopes` returns for its credentials, else
  none. A set list replaces the function, never joins it, as a
  configured list replaces a code value throughout `services.*`; an
  empty list grants none. They reach the permission gate as
  `Meta.Extra["scopes"]`, as a verified token's do, beside the peer
  keys; a scope-less peer is refused a `kit/permissions` leaf as
  insufficient scope.
- A peer the kernel cannot describe, and under `require_same_uid` a
  peer of another uid, is refused `UNAUTHENTICATED` and audited as
  `ErrAuthRefused`.
- The mode selects the verifier: under `peer`, `SocketConfig.Auth` is
  not consulted.
- Refused at validation, exit `2`, naming the key: `peer` on a
  platform without peer credentials, an HTTP credential mode (`mtls`,
  `jwt`, `jwks`, `oidc`, `apikey`) or an unknown mode under
  `services.socket`, an `auth.peer` key under another mode, a value
  that is not a bool, `scopes` that is not a list of names, and `peer`
  or an `auth.peer` key under any other service. An HTTP credential
  mode under `services.all.auth.mode` is the HTTP listeners' default
  and the socket does not read it; `services.all.auth.mode: peer` is
  the socket's, and an HTTP listener does not read it.

### Timeouts

The `timeouts` block holds the HTTP listener's server timeouts and
the per-command deadline:

| Key                                  | Default     | Meaning                                                        |
|--------------------------------------|-------------|----------------------------------------------------------------|
| `services.<svc>.timeouts.read_header` | `5s`        | reading a request's headers                                    |
| `services.<svc>.timeouts.read`        | `5s`        | reading a whole request, body included                         |
| `services.<svc>.timeouts.write`       | `10s`       | writing a response; stream responses exempt                    |
| `services.<svc>.timeouts.idle`        | `read`      | a keep-alive connection waiting for its next request           |
| `services.<svc>.timeouts.command`     | none        | the deadline of a command that declares no `kit/timeout`       |

Rules:

- Every value is a duration; `0` means none, except that a zero
  `read_header` or `idle` falls back to `read`. A negative value, a
  bare number, or an unknown key MUST be refused at validation, exit
  `2`, naming the key.
- The four server keys bind every kit HTTP listener: `api`, `rpc`,
  `mcp`. A stream response MUST be exempt from `write`: the api
  service's stream routes, the rpc service's `InvokeStream`, and the
  mcp service's endpoint as a whole, which is a streamable-HTTP stream
  route. The socket service reads only `command`.
- The per-command deadline is the command's `kit/timeout` annotation
  (a positive duration), else `timeouts.command`. The annotation wins,
  and a malformed one MUST be refused at validation, exit `2`.
- The deadline is armed at slot 11, when the admitted call is about
  to run, in `Admission.Run` and `Admission.Stream` alike — a
  result-cache miss included, which, cut short, stores nothing — and
  covers queue wait and execution. A caller whose context already carries an
  earlier deadline keeps it: a call can shorten the bound, never
  lengthen it.
- When it passes, the runner cancels the command as
  [Cancellation](#cancellation) describes — cooperatively in process,
  the process group in a subprocess — and the bridge returns
  `ErrDeadlineExceeded` with the partial `Result`. A command that
  completes despite it is a completion. Under `Stream`, the `done`
  event is delivered first.

A request/reply response still ends at `write`, whatever the
deadline: a command meant to run longer is called on a stream.

### Capacity

The `concurrency` block bounds how many invocations a service runs at
once:

| Key                                        | Default | Meaning                                               |
|--------------------------------------------|---------|-------------------------------------------------------|
| `services.<svc>.concurrency.enabled`       | `true`  | the gate is installed, on loopback and beyond it      |
| `services.<svc>.concurrency.max_inflight`  | `32`    | invocations running at once, at least `1`             |
| `services.<svc>.concurrency.max_queue`     | `64`    | invocations waiting for a slot; `0` is no queue       |

Rules:

- At slot 11 an admitted call on a remote surface takes an in-flight
  slot, or a place in a first-come-first-served queue. With every slot
  taken and the queue full it is refused as `overloaded`, and
  audited. A slot frees when the run ends and passes to the head of
  the queue. The CLI and library surfaces are not counted; an
  idempotency replay, a result-cache hit, and a call waiting on an
  identical call's run take no slot.
- The per-command deadline is armed before the call queues, so the
  wait counts against it; a call that outwaits it is
  `deadline_exceeded` without having run. A caller that goes away
  while queued gives its place up at once.
- `max_inflight` is an upper bound. A runner over one shared tree
  runs one invocation at a time: over it the gate admits one and
  queues the rest, so a waiting caller is bounded, counted and
  cancelable rather than blocked on the runner's lock. With a root
  factory (`cli.WithRootFactory`) up to `max_inflight` run in
  parallel.
- A transport that commits its response before running — an SSE or
  projection stream — takes the call's place before it commits
  (`Admission.Reserve`), so an overload is answered with `503` and
  `Retry-After`, not inside an open stream. The wait itself happens
  in `Run` or `Stream`, under the deadline.
- The retry hint is how long the queue ahead would take to drain,
  estimated from recent run times: at least 1s, at most 30s.
- Slots and queue live in memory, per service. The in-flight count is
  `kit.serve.requests.active` and the queue's is
  `kit.serve.requests.queued`, both by service and surface.
- A `max_inflight` below `1`, a `max_queue` below `0`, a value that is
  not a whole number, or an unknown key is refused at validation,
  exit `2`, naming the key.

### Refusals

Each refusal class has one stable code, the same string on every
surface, carried wherever the surface's protocol puts machine-readable
detail. The classes below are added by middleware; `unknown_command`,
`not_enabled`, `not_invocable`, `destructive_blocked`,
`permission_denied`, and `confirmation_required` keep the mappings
given in [Permission](#permission) and by each surface.

| Code                     | Slot     | HTTP                                  | Connect             | MCP                        | Socket              | Class (exit)       |
|--------------------------|----------|---------------------------------------|---------------------|----------------------------|---------------------|--------------------|
| `unauthenticated`        | edge, 4  | `401` + `WWW-Authenticate`            | `Unauthenticated`   | edge: `401`; 4: `isError`  | `UNAUTHENTICATED`   | `UNAUTHORIZED` (5) |
| `insufficient_scope`     | 6        | `403` + `WWW-Authenticate: Bearer error="insufficient_scope", scope="…"` | `PermissionDenied` | `isError`      | `DENIED`            | `UNAUTHORIZED` (5) |
| `rate_limited`           | 7        | `429` + `Retry-After`                 | `ResourceExhausted` | `isError`                  | `RATE_LIMITED`      | `RATE_LIMITED` (64)|
| `quota_exceeded`         | 9        | `429` + `Retry-After` (window reset)  | `ResourceExhausted` | `isError`                  | `QUOTA_EXCEEDED`    | `RATE_LIMITED` (64)|
| `overloaded`             | 11       | `503` + `Retry-After`                 | `Unavailable`       | `isError`                  | `OVERLOADED`        | `TRANSIENT` (6)    |
| `deadline_exceeded`      | 12       | `504`                                 | `DeadlineExceeded`  | `isError`                  | `DEADLINE_EXCEEDED` | `TRANSIENT` (6)    |
| `idempotency_conflict`   | 8        | `409`                                 | `Aborted`           | `isError`                  | `CONFLICT`          | `CONFLICT` (4)     |
| `idempotency_key_reused` | 8        | `422`                                 | `InvalidArgument`   | `isError`                  | `CONFLICT`          | `USAGE` (2)        |
| `body_too_large`         | http 10  | `413`                                 | `ResourceExhausted` | `413`, JSON-RPC `-32600`   | — (line bound)      | `USAGE` (2)        |
| `host_rejected`          | http 8   | `403`                                 | `PermissionDenied`  | `403`                      | —                   | `UNAUTHORIZED` (5) |
| `origin_rejected`        | http 8   | `403`                                 | `PermissionDenied`  | `403`                      | —                   | `UNAUTHORIZED` (5) |

Per surface:

- **HTTP.** The body is the `api.APIError` shape; `code` is the class
  code. The Connect codes are chosen so Connect's own HTTP mapping
  yields the same status as REST for every class except
  `idempotency_key_reused`, which Connect reports as `400`.
- **Connect.** A retryable class carries `Retry-After` in the error's
  metadata.
- **MCP.** A refusal decided on the HTTP plane, before the protocol
  layer reads the request, is an HTTP status with a JSON-RPC error
  body where one applies (`id` null when the id is unknown). A refusal
  decided on the invocation plane for a known tool is a `tools/call`
  result with `isError: true`, its text starting with the code, and a
  result `_meta` entry `hop.top/refusal` of `{"code", "retry_after_ms"}`.
  An unknown tool stays a JSON-RPC invalid-params error. Over HTTP,
  `insufficient_scope` SHOULD instead be answered `403` with the scope
  challenge once MCP authorization is in force, because that is the
  step-up signal the MCP authorization specification defines.
- **Socket.** `Error.code` is the socket code; a retryable class
  carries `retry_after_ms`. The socket has no body limit class: its
  1 MiB line bound ends the connection instead.
- **Class** is the exit-code class a client reports when it turns a
  refusal into a process exit. No new numbers are allocated.

Sentinels, in `cmdsurface`: slot 4 returns `ErrAuthRefused`; the others
are `ErrInsufficientScope`, `ErrRateLimited`, `ErrQuotaExceeded`,
`ErrOverloaded`, `ErrDeadlineExceeded`, `ErrIdempotencyConflict`, and
`ErrIdempotencyKeyReused`. A new sentinel MUST wrap its nearest
existing class — `ErrInsufficientScope` wraps `ErrPermissionDenied`,
`ErrQuotaExceeded` wraps `ErrRateLimited`, `ErrDeadlineExceeded` wraps
`context.DeadlineExceeded` — so a transport that predates a class
degrades to the nearest existing answer rather than to `internal`.

Every invocation-plane refusal reaches the audit sinks, as today.
HTTP-plane refusals MUST be counted by code in metrics and logged,
and SHOULD reach the sinks through `Bridge.Audit` when the request
addresses a projected command, as authentication refusals already do.

### Idempotency

The `idempotency` block (invocation 8 and 13). A remote call that
carries an idempotency key and repeats a call its principal already
completed is answered with that call's recorded `Result`, and nothing
runs. Authority:
[`go/transport/cmdsurface`](../../go/transport/cmdsurface/)
(`idempotency.go`).

The key, per surface:

| Surface | Carries the key in | Marks a replay with |
|---|---|---|
| REST (api service) | `Idempotency-Key` request header | `Idempotent-Replayed: true` response header, on unary and stream routes |
| Connect (rpc service) | `Invocation.meta.idempotency_key`, else the `Idempotency-Key` request header | `Idempotent-Replayed: true` response header |
| MCP | `params._meta["hop.top/idempotency-key"]`, else the `Idempotency-Key` header of an HTTP request; a task-augmented call carries none (its task id is the retry handle) | result `_meta["hop.top/idempotent-replayed"]: true` |
| Socket | the request's `idempotency_key` | the response's `"replayed": true` |

Rules:

- **Scope.** `cmdsurface.IdempotencyScope` is the one scope, for this
  ledger, the read-tier result cache and a command's own
  `--idempotency-key` middleware alike.
  A caller a verifier established (`EstablishedVerified`) is scoped to
  its tenant and principal, so its key answers it on every surface. A
  caller the transport itself vouches for (`EstablishedTransport`: the
  owner-only socket, stdio, cron, an IAM-signed Lambda call) is the
  server's owner, scoped to that transport whatever name it claims.
  Any other call is scoped to its tenant, claimed caller and surface;
  one without a caller to its client host. One principal's key never
  answers another's call, and no claimed name, on any transport,
  reaches a verified caller's records.
- **Same call.** A key is bound to the invocation it was first used
  for: the command path, its arguments and its flags, as the runner
  renders them, less the key. A call reusing a key for a different
  invocation is refused `idempotency_key_reused`, whether the first
  call is running or recorded.
- **Still running.** A call whose key names a call from the same scope
  that has not finished is refused `idempotency_conflict`. A caller
  retries after the first call ends and receives its answer.
- **Record.** At 13, a run that returned no error and exit code `0` is
  recorded under its key. A failed run is not: the caller's retry
  runs again. A stream is recorded like a run, from the `Result` its
  done event carries, and a replayed stream sends the recorded output
  line by line, then the result.
- **Replay.** A replay runs nothing: it consumes no quota, is never
  put to a person (slot 10), and holds no concurrency slot. It has
  passed slots 1–7 like any call and is audited, the record's
  `Extra` carrying `idempotent_replayed: "true"`.
- **Order within 8.** Idempotency replay answers first; the read-tier
  result cache is consulted only for a call it let through.
- **Lifetime.** A record replays for `ttl` after it was recorded
  (default `24h`); an older one is ignored and the key is free again.
  A reservation for a call admitted and never run is released when
  its request ends, or sooner when the surface abandons the
  admission.
- **Store.** Records live in `serve-idempotency.db` in the tool's
  state directory, separate from the CLI's own `--idempotency-key`
  store, opened on the first keyed call. `cli.WithServeIdempotencyStore`
  replaces it (a shared store for replicas, a memory store in
  tests). Calls in flight are tracked per process: two processes
  sharing a store replay each other's records, but do not see each
  other's running calls.
- **Store failure.** A keyed call whose store cannot be opened or read
  fails as an internal error rather than run unprotected. A record
  that cannot be written is dropped; the caller still receives the
  run's answer.

| Key | Default | Meaning |
|---|---|---|
| `services.<svc>.idempotency.enabled` | `true` | `false` runs every call, key or not |
| `services.<svc>.idempotency.ttl` | `24h` | how long a record replays; a positive duration |

Both keys may be set under `services.all`.

## Execution

A transport service does not run commands; it hands an invocation to
the bridge, and the bridge hands it to a **runner**. The runner is
where a served invocation becomes a command execution, and this
section is the normative contract for that step: the shape of the
result, the streams, cancellation, what is isolated between
invocations, and the classes of command that never run this way.
Authority: [`go/transport/cmdsurface`](../../go/transport/cmdsurface/)
(`runner.go`, `exec.go`, `result.go`).

### Arguments

An invocation's `Args` are positional arguments on every surface. A
value that begins with `-` (`-x`, `--help`) is an argument, never a
flag; flags travel only in `Flags`.

- The runner MUST end the options before the arguments. The command
  runs with the argv `<path...> <--flag=value...> -- <args...>`; the
  `--` is omitted when there are no arguments.
- A command that parses its own argv (cobra `DisableFlagParsing`, such
  as a plugin that forwards to another binary) receives its arguments
  verbatim, with no `--`: kit parses none of them, and the marker would
  reach the forwarded program as an argument of its own. The bridge
  records this on the admitted invocation, so a subprocess runner,
  which holds no tree, applies the same rule.
- The marker is visible to a command that inspects its argv:
  `cmd.ArgsLenAtDash()` is `0`, and a subprocess's `os.Args` carries the
  `--`, for every served invocation with arguments.

### Result

Every invocation, whichever transport carried it, produces one
`Result`:

| Field       | Meaning                                                                  |
|-------------|--------------------------------------------------------------------------|
| `exit_code` | the command's exit status in the kit taxonomy; `0` on success            |
| `stdout`    | what the command wrote to its standard output, as text                   |
| `stderr`    | what the command wrote to its standard error, as text                    |
| `data`      | the command's declared structured output, decoded; absent when none      |

Rules:

- `exit_code` is the code a kit structured error carries (`USAGE` is
  `2`, `UNAUTHORIZED` is `5`, and so on per
  [`go/console/output/envelope/exitcodes.go`](../../go/console/output/envelope/exitcodes.go)).
  A bare error with no code is `1`. An invocation the command's parser
  refuses — wrong positional count, unknown or malformed flag, missing
  required flag — is `USAGE`, `2`, with the parser's message in
  `stderr`. A command that succeeds is `0` regardless of what it wrote
  to `stderr`.
- `stdout` and `stderr` are what the command wrote through
  `cmd.OutOrStdout()` and `cmd.ErrOrStderr()`. A command that writes
  to `os.Stdout` directly bypasses capture; such output reaches the
  serving process's own streams and is not in the result. Cobra's
  usage and error text is silenced. When a command fails without
  writing to `stderr`, the error's message is placed there so a
  failure is never silent.
- `data` is populated by **decoding**, never by scraping a human
  rendering. The rule is in the next section.

### Format selection and structured output

An invocation asks for a rendering the way a shell does: through the
command's own `--format` flag, carried in the invocation's flags under
the key `format`. There is no separate field.

The runner then applies one rule, and applies it identically over
every transport:

1. **The command declares an output schema, can take `--format`, and
   the invocation names no format.** The runner runs the command with
   `--format=json` — the structured rendering the output pipeline
   dispatches — decodes its standard output into `data`, and leaves
   `stdout` empty. The caller asked for no rendering; the data is the
   output, and the JSON text would only repeat it.
2. **The invocation names `json`.** The command runs as asked;
   `stdout` carries the JSON text as the CLI would print it, and, if
   the command declares a schema, `data` carries it decoded.
3. **The invocation names any other format.** The command runs as
   asked and `stdout` carries that rendering. `data` is absent: the
   caller asked for text, and the runner does not scrape it.
4. **The command declares no schema.** Its streams are whatever it
   produces under the named or default format, exactly as today, and
   `data` is absent even under `json`: the decoder only runs for
   output a command has declared.

`data` is decoded only when standard output is exactly one JSON
document. A command that writes anything after its document — a hint,
a trailing line — gets `data` absent and its streams intact, so a
consumer never receives a payload the runner guessed at. Numbers keep
their exact text: a Go consumer sees `json.Number`, and a transport
re-encoding `data` emits the digits the command wrote, integers above
2^53 included.

A declared schema is the command's statement that its output is data,
and rule 1 is its consequence. The alternative — the human rendering
on `stdout` and the decoded payload in `data` from one run — would
need the output pipeline to hand the typed value to the runner before
rendering it, and no such seam exists. Running the command twice to
get both is not an option: a write command must run once. So the
runner selects the structured rendering when the caller expressed no
preference, and never renders twice.

Over REST, `format` is a root flag rather than one the command
declares, so the projection does not accept it: rule 1 governs every
schema-declaring command there, and every other command answers in
its default rendering.

### Streams

`Runner.Run` returns the `Result` when the command finishes.
`Runner.Stream` delivers the same execution incrementally: one event
per line of `stdout` and of `stderr` as it is written, then a `done`
event carrying the `Result` — `data` included — that `Run` would have
returned. Run and Stream are one execution observed two ways; a
command does not behave differently under either.

The kit-shipped `socket` service is request/reply and uses `Run`. The
`api` service uses `Run` for its request/reply routes and `Stream`
for their streaming twins (`<route>/stream`), which it admits through
the same gates before opening the stream. The `mcp` service uses
`Run`, and `Stream` for a call carrying a progress token, admitted the
same way (`Bridge.Admit`, then `Admission.Stream`). The `rpc` service
uses `Run` for `Invoke` and `Stream` for `InvokeStream`, admitted the
same way. The WebSocket and SSE surfaces in `cmdsurface` use `Stream`.

### Cancellation

The invocation's context is the command's context: what the command
reads as `cmd.Context()` is the caller's, and canceling it cancels
the command.

- **In process**, cancellation is cooperative. A command that selects
  on its context returns when the context is done; one that never
  reads it runs to completion. This is the same contract the command
  has on the CLI under a signal.
- **In a subprocess**, the child's whole process group is killed on
  Unix, so a helper the command spawned does not outlive it; on
  Windows the child itself is killed and grandchildren are best
  effort.
- A command that fails while its context is done is reported to the
  transport as a **cancellation** — the runner's error wraps
  `context.Canceled` or `context.DeadlineExceeded` — alongside the
  partial `Result`, so a caller can tell an abort it asked for from a
  failure of the command's own. A command that completes despite the
  cancellation is a completion.
- Under `Stream`, the `done` event is still delivered, and the
  cancellation is reported after it.

Which context a transport hands the runner is the transport's
contract, and both kit-shipped services pass the caller's: the `api`
service the HTTP request's context, the `socket` service a
per-connection context, so a client that disconnects mid-command
cancels it on either transport (see [Security](#provenance)).

Stopping a service ends what is in flight per transport:

- The `socket` service closes every connection, which cancels the
  command each one carries.
- The `api` service ends every open stream first: its command is
  canceled and the stream's terminal frame is an `error` with status
  `503` and code `shutting_down`. Request/reply calls then drain:
  the service waits up to the stop budget for them to complete and
  does not cancel them.
- On every HTTP listener (`api`, `rpc`, `mcp`), a client stalled
  mid-header or mid-body MUST NOT hold the stop: when stopping
  begins, a connection with no complete request is closed, and a body
  read still waiting on the client is ended, so its handler returns.
  A request whose body has been read is untouched.
- Independently of stopping, a handler that returns with its body
  unread — a refusal answered before the body, such as
  `host_rejected` or `body_too_large` — MUST NOT leave the connection
  waiting on the rest of the body for the read timeout: what has
  already arrived is consumed, and a body still on the wire ends the
  connection after the response (`Connection: close`).

### Isolation between invocations

Cobra and pflag keep everything about an execution on the command
tree itself: argv, the output writers, the command's context, and
every parsed flag value with its `Changed` bit. A tree executed twice
is not a fresh tree the second time. The runner is responsible for
making it one.

With the default **shared-tree** runner (`InProcessRunner(root)`):

- Invocations are serialized: one at a time per bridge. The tree is
  one object, and a second invocation parsing flags while the first
  runs would race on them.
- Before each invocation, every flag the leaf's parse will touch —
  its own and every ancestor's persistent set — is returned to the
  **baseline**: the value and `Changed` bit it had when the runner was
  built. For a kit root, that is the state the operator's own command
  line left, so `mytool --no-color serve socket` serves invocations
  that see `--no-color`, and each invocation adds only the flags it
  carries. After the invocation, the baseline is restored, so the
  tree is clean between invocations too.
- A slice flag the invocation carries is emptied before the parse,
  not reset to its baseline: pflag appends to a slice on every set
  after the first, and would otherwise stack the invocation's values
  on the baseline's.
- The leaf's context is set to the invocation's explicitly, every
  time. Cobra copies the root's context onto a leaf only when the
  leaf has none, so a leaf executed once would otherwise keep that
  first context — canceled, or carrying another caller's values —
  forever.
- Standard input is empty. A command that prompts reads EOF; one that
  checks for a terminal finds none. The serving process's own stdin
  is never read on a caller's behalf.
- Output writers, argv, the silence bits, and stdin are restored on
  return.

What is **not** isolated, and cannot be from the runner: process-wide
effects a command has through its own code or the tree's hooks. The
working directory (`--chdir`), environment variables, package-level
variables, and anything a `cobra.OnInitialize` hook reads into
process state are the process's. A transport reachable by callers who
are not the operator MUST restrict the flags it forwards; the REST
projection does so by accepting only the flags a command declares.

With a **root factory** (`InProcessRunner(nil,
WithRootFactory(build))`), every invocation gets a tree of its own:
nothing is reset, nothing is serialized, and invocations run in
parallel. The factory MUST return a tree that shares no mutable state
with the trees it returned before — no flag bound to a package-level
variable, no closure over a struct another invocation writes.

A kit root supplies the factory through `cli.WithRootFactory(build)`,
where `build` is the tool's own construction — `cli.New` plus every
command it mounts, the function `main` already has. The kit-shipped
`api`, `socket`, `mcp`, and `rpc` services then hand their bridge the factory runner
instead of the shared-tree one, and for every tree the factory
returns:

- `Root.Prepare` installs what `Root.Execute` installs before it
  parses — the RunE middleware carrying the confirmation, policy,
  idempotency, and error-envelope gates, the kit-managed flags, and
  validation — without executing, and without touching cobra's
  process-global initializer list. A served invocation meets the same
  gates on a factory tree as on the CLI: an unconfirmed destructive
  command is refused with `UNAUTHORIZED`, a typed-token command needs
  its token, and the permission and invocability gates answer in the
  bridge before any tree is built.
- The persistent root flags the operator set on the serving command
  line are replayed onto it, `Changed` bit included, so
  `mytool --no-color -c key=val serve socket` serves invocations that
  see both. This is the factory form's counterpart of the shared
  runner's baseline.
- The factory is exercised once when the service validates. One that
  returns nothing, returns the serving root, or builds a tree that
  fails validation is a usage error at exit `2`, before anything
  binds.

The shared-tree form is the **default** and the factory form is
**opt-in**. Only the adopter can rebuild the adopter's tree: commands
are mounted after `cli.New` returns, and kit holds no description of
that step. Opting in also accepts a cost — the construction runs once
per served invocation, so anything expensive or stateful in it (a
store opened, a keypair loaded) belongs outside the factory and must
be safe for concurrent use — and a responsibility kit cannot check:
trees that share state through package-level variables are isolated
in name only.

The throughput consequence is stated plainly: **a shared-tree bridge
runs one command at a time.** A tool that needs concurrent in-process
execution supplies a root factory; one that needs process isolation
supplies `SubprocessRunner(binary)`, which spawns a process per
invocation and has nothing to share.

### Interactive, self-hosting, and management commands

Three classes of command are described by discovery and withheld from
execution. The reasons are the reflector's
([`go/ai/cmdreflect`](../../go/ai/cmdreflect/README.md)), and every
transport surfaces them unchanged.

| Class        | Reason            | Which commands                                                              | Lifted by     |
|--------------|-------------------|-----------------------------------------------------------------------------|---------------|
| interactive  | `interactive`     | `kit/side-effect: interactive` — a shell, a TUI, anything needing a terminal | the CLI only  |
| self-hosting | `self-hosting`    | `serve` and everything under it; `kit/network: ingress`; `kit/self-hosting: true` | nothing       |
| management   | `management-only` | kit's reserved verbs (`spec`, `status`, …) and their children               | `AllowReserved` |

Rules:

- An **interactive** command is never invocable through a projected
  surface. It needs a terminal and a human; a runner captures the
  streams and supplies an empty stdin, so it has neither. The bridge
  refuses it at `Invoke` with `ErrNotInvocable`, naming the reason,
  before the destructive ceiling — even when it admitted the command
  as a leaf — and the runner refuses it again as a backstop.
- A **self-hosting** command is never invocable through a projected
  surface, and no reflection option lifts the reason. Running it
  inside a served invocation would start a server inside the server,
  or replace the binary that is serving. Any one of three signals
  marks it: the name `serve`, or a position under one, at any depth
  — the depth-1 verb the hierarchy above gives to exactly one
  command, and any nested `serve` that starts a server of its own; a
  declared
  `kit/network: ingress`, because accepting connections is what a
  server does; or the explicit `kit/self-hosting: true`, which is how
  kit marks its own self-modifying commands and how an adopter marks
  theirs. None of the three consults the reserved-verb list, so a
  bare cobra tree withholds its server too. A bridge never discovers
  such a command as a leaf, so a call answers "unknown command" and
  discovery answers `self-hosting`; the runner refuses it as well.
- A **management-only** command keeps its reason. It exists for the
  tool's own introspection surface and is withheld from projection by
  default; a consumer that has a use for it reflects with
  `AllowReserved`, as the socket service's bridge does, and the policy
  gate decides from there.
- Kit's own `serve` is both reserved and self-hosting. It reports
  `self-hosting`, the answer that says why calling it through a
  transport can never work rather than merely who owns the word.

Forced remote execution of an interactive or self-hosting command is
out of scope: there is no override.

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

Eight differences are visible to an adopter who upgrades without
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

5. **The api service listens on loopback, and refuses to serve
   unauthenticated anywhere else.** `services.api.addr` defaults to
   `127.0.0.1:8080` rather than `:8080`. An adopter who set no
   address is unaffected on the same machine and no longer reachable
   from others. An adopter who set a non-loopback address — `:8080`
   included — and no `Auth` now gets exit `2` at `serve` with a
   message naming the fix; setting `services.api.insecure_remote:
   true` restores the previous behavior verbatim, by name. An adopter
   with `Auth` is unaffected on any address. See
   [Security](#security) and
   [secure-remote-serving.md](../adopters/guides/secure-remote-serving.md).
6. **The api service refuses to serve beyond loopback with no
   delegation policy.** An adopter serving on a non-loopback address
   who names no `--policy` now gets exit `2` at `serve` with a
   message naming the fix, whether or not they authenticate. Without
   a policy the permission gate permits every command for every
   caller, so this closes a surface that admitted callers and then
   bounded nothing. Naming a `--policy` is the intended remedy;
   `services.api.insecure_no_policy: true` restores the previous
   behavior verbatim, by name. An adopter on loopback is unaffected.
   The socket service is unaffected: it is loopback by construction.

7. **Served invocations are isolated, and `data` is real.** The
   in-process runner behind every transport now applies the
   [Execution](#execution) contract. Five differences are visible:

   - A command that declares an output schema answers a call that
     names no format with `data` populated and `stdout` empty.
     Before, `data` was never set — the guides' claim that it carried
     the structured output was not true — and `stdout` carried the
     command's default table rendering.
   - A flag set by one invocation no longer persists into the next.
     Before, a `--verbose` or `--all` sent once stayed set on the
     shared tree for every later call that did not mention it.
   - A served command reads an empty stdin. Before, it read the
     serving process's own stdin, and a destructive command with no
     `confirm` flag could prompt on the operator's terminal.
   - A command that fails while its caller's context is done is
     reported as a cancellation rather than as a command failure.
   - `serve`, any command declaring `kit/network: ingress`, and any
     command annotated `kit/self-hosting` are withheld from every
     transport with the reason `self-hosting`. Before, kit's `serve`
     was reachable over the socket, and would have started a second
     supervisor inside the first. Interactive commands, which the
     socket's bridge admits as leaves, are now refused by the bridge
     (`NOT_INVOCABLE`), with the runner as a backstop, rather than
     run without a terminal.

8. **Arguments stay arguments.** Every runner ends the options before
   an invocation's `Args` ([Arguments](#arguments)). Before, an
   argument beginning with `-` was parsed as a flag: `-x` failed as an
   unknown flag, and `--help` answered with help text and exit 0
   without running the command. A command that reads
   `cmd.ArgsLenAtDash()` or `os.Args` now sees the `--`; a command
   that parses its own argv receives its arguments as before.
9. **Beyond loopback, no `--policy` means `kit-default`, not a
   refusal.** The api, rpc and mcp services used to refuse, at exit
   `2`, a non-loopback address with no `--policy`. They now serve
   under `kit-default`: unestablished callers read only, established
   principals read and write, destructive commands need a declared
   `kit/permissions` scope. An adopter who set
   `insecure_no_policy: true` is unaffected. One who named a
   `--policy` is unaffected. One who relied on the refusal to keep a
   misconfigured deployment from starting now gets a bounded server;
   one who wants the old unbounded surface sets
   `services.<svc>.insecure_no_policy: true`, and one who wants their
   own rules names a `--policy`.
10. **A policy class entry governs its expanded tiers.** `write: []`
   refuses `write-local` and `write-shared` commands, and
   `destructive: []` the `-local` and `-shared` destructive ones; they
   used to pass. A policy that meant to allow them names them
   (`write-shared: ["*"]`). The `serve` command itself is not gated by
   the `--policy` it carries.

Migrating from a hand-written leaf `serve` command, or from a tree
mounted on REST and MCP through `cmdsurface` by hand, is a mechanical
diff walked in
[migrate-to-served-commands.md](../adopters/guides/migrate-to-served-commands.md):
what to delete, where `--addr` and the startup line go, how custom
routes and a bridge policy carry over, and the refusal each skipped
step produces. A project generated by `kit init --from cli-go` starts
in the migrated state, and
[`examples/served`](../../examples/served/README.md) pins every claim
above through the real execute path.

A deprecation of `WithAPI`, if any, is announced through the standard
kit deprecation surface
([`go/console/cli/deprecation.go`](../../go/console/cli/deprecation.go))
with a release's notice before removal — never silently.

## Cross-language parity

Go is the reference implementation, not the definition of the
contract. This section says which parts of everything above every kit
SDK CLI — TypeScript, Python, Rust, PHP — MUST mirror, and which parts
are Go-only affordances that the other ports are free to omit.

The distinction matters because "Go does it" is not by itself an
argument for parity. Kit's `help <topic>` form is the established
precedent: cobra hands Go a command-path help operand for nothing,
every other language would hand-roll it, and no operator's script
breaks without it, so
[cli-parity-guide.md](../adopters/guides/cli-parity-guide.md) places it
outside the parity contract deliberately. The same judgement is applied
below. A behavior is in the contract when a person or a program
observing the CLI from the outside would be wrong about the tool if a
port answered differently; it is out when it exists because of the
machinery one language happens to have.

Where a port has shipped a `serve` at all, everything under
[Required](#required-of-every-sdk) is a conformance obligation.
A port with no `serve` is not in violation of this section — it has
simply not implemented the surface yet, and
[`contracts/parity/serve.json`](../../contracts/parity/serve.json)
records that as a pending gap rather than a failure.

Every port has a mount point for the command half: Go through the
root's `WithService` option, TypeScript and Python on any commander or
Typer root, PHP through `KitCommand` on Symfony Console, and Rust
through `serve::command::mount` on any clap root (feature `serve-cli`)
— the kit-owned Rust root factory, when it lands, mounts that same
command.

### Required of every SDK

#### The hierarchy and the override rule

`<tool> serve` MUST be the supervisor over every configured and
enabled service, `<tool> serve <service>` MUST be the selector over
exactly one, and both MUST share one lifecycle implementation. Two or
more positional arguments is `USAGE`, exit `2`.

The override rule is required verbatim: a named service MUST start
even when `services.<name>.enabled` is `false`, provided registration,
configuration, and policy validate, in that order. Under the
supervisor form a disabled service is skipped silently and MUST NOT
affect the exit code, and a supervisor invocation resolving to zero
services MUST exit `2` rather than `0`.

This is the load-bearing part of the whole page. An operator's
`systemd` unit, container entrypoint, or CI script is written against
the hierarchy and the override rule and against nothing else; a port
that made `serve <service>` respect `enabled` would silently do
nothing where the reference starts a server.

`--list` is required as a **flag**, not a `list` child, for the reason
given in [Command hierarchy](#command-hierarchy): `list` is reserved
selector vocabulary, so `serve list` would be ambiguous with the
selector form. The *columns* it prints are not contract — a port
renders them through its own output layer.

#### The registration seam

Every port MUST expose a registry that both kit-owned and
adopter-owned services register into before the root command runs, and
a registration MUST carry the same four capabilities: a name, a start
that blocks until cancelled or failed, a readiness report, and a stop.

The *shape* of the seam is the port's own. Go uses a four-method
interface because that is how Go expresses one; a port whose idiom is
a plain object with four function-valued properties, or a class, or a
callback record, satisfies this by supplying the same four
capabilities. What is fixed is the capability set and the behavior of
each, not a method table.

Also required:

- The name grammar `^[a-z][a-z0-9-]*$`, because a service identifier
  is a CLI word, a config key segment, and an event payload value in
  every language at once.
- The reserved names `all`, `none`, `list`.
- Duplicate registration is a construction-time failure with an
  explicit replace/override escape hatch. Last-writer-wins is
  forbidden: it turns a wiring bug into a service silently not
  running. The *mechanism* is the port's — Go panics; a port may throw,
  raise, or abort — but it MUST NOT be a warning that execution
  survives.
- Listing order is registration order.

The optional declarations — a config validator, a dependency list, an
address, a side-effect/network class — are required as *concepts* a
registration MAY carry, and required to have the effects described
above where a service does carry them. How a port lets a registration
opt in (Go's optional interfaces, an optional field, a subclass hook)
is not contract.

#### Readiness, and its event and log shape

Ready means every acquisition that can fail deterministically has
succeeded. A service MUST report ready at most once per start, the
aggregate is ready when every started service is ready, and a service
that has not reported ready inside `services.<name>.ready_timeout` is
a start failure.

Six lifecycle transitions MUST be surfaced. Which sink carries them is
conditional, but **at least one MUST**, and the vocabulary below is
fixed whichever does.

**A port that has an event bus MUST publish them under exactly the
topic strings in the table.** A subscriber is written against the
string and does not know which language published it, so the strings
are not negotiable for a port that publishes at all. Not every SDK
has a bus — PHP has none today — and this section does not require one
to be built before `serve` can ship. A port without a bus satisfies
the requirement through the log alone.

Fixed for either sink:

| Transition                            | Surfaced when                                 |
|---------------------------------------|-----------------------------------------------|
| `kit.serve.service.started`           | a service has been asked to start             |
| `kit.serve.service.ready_reported`    | a service reported ready                      |
| `kit.serve.service.failed`            | a service failed                              |
| `kit.serve.service.stopped`           | a service finished stopping                   |
| `kit.serve.supervisor.ready_reported` | every started service is ready                |
| `kit.serve.supervisor.stopped`        | the supervisor finished its shutdown sequence |

- The service identifier travels in the payload under `service`, never
  in the topic, so a subscriber is not forced to re-bind when a tool
  gains a service.
- A failure reason travels in the payload under `error`, never in the
  topic.
- A resolved `address` is carried on `ready_reported` when the service
  has one. It is the single most useful thing in a startup trace, and
  for a wildcard port it is not knowable from configuration.
- The action is `ready_reported`, not a bare `ready`. The bare form
  fails the past-tense validation in
  [event-topics.md](event-topics.md), so a port emitting it would
  publish a topic Go subscribers reject.

A port whose sink is the log emits `started` / `ready_reported` /
`stopped` at `INFO` and `failed` at `ERROR`, carrying the service
identifier and the address as **structured fields** rather than
interpolated into the message text. That is what makes a startup trace
greppable across a fleet whose tools are not all the same language.
A port with neither a bus nor a structured logger emits the same four
transitions with the same field names through whatever it writes to
stderr; what it MUST NOT do is stay silent about a service that
started, became ready, failed, or stopped. Which ports those are is
not recorded here — it changes as ports grow a bus or a logger, and a
port that has one is bound by the corresponding rule above.

What is *not* contract: the payload's `elapsed_ms`, the `Qualifiers`
envelope Go embeds, and any key beyond `service`, `error`, and
`address`. A port SHOULD carry elapsed time where that is cheap;
nothing downstream is specified to read it.

#### Ordered shutdown on the same signals

Every port MUST listen for `SIGINT` and `SIGTERM`, treat the first as
the start of a graceful drain, and treat a second of either kind as an
escalation that abandons the drain and exits with the crash code. The
same defaults apply — `30s` per-service stop timeout, `60s` total
shutdown budget — and a stop that exceeds its per-service budget MUST
be abandoned in favor of the next service rather than block the whole
shutdown.

The ordering rules are contract: cancel once so every service observes
cancellation at the same instant, then stop in the exact reverse of
the order services actually started, one at a time.

`services.failure_policy` with its two values `fail-fast` (default)
and `isolate` is contract, because it changes whether a process
survives — an operator's health check reads the difference.

Signals are the one place a port may be genuinely unable to conform:
on Windows there is no `SIGTERM` to catch, and a port targeting it
maps the platform's own shutdown notification onto the same drain. A
port that cannot observe a *second* signal degrades to the single
graceful path rather than inventing a different escalation.

#### The exit-code taxonomy

The table in [Exit behavior](#exit-behavior) is contract in full,
because it is the only part of a `serve` run a supervising process
reads programmatically:

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

Including the two that are easy to get wrong: a signal-initiated stop
exits `0`, and start failure and runtime crash share exit `1`. The
`TRANSIENT` propagation rule holds too — a failure wrapping a
transient error keeps exit `6` — so an agent's retry branch behaves
the same whichever language the tool it is driving was written in.

Every port already has this taxonomy; it is not new surface. The
obligation is to route serve's outcomes onto it rather than onto
hand-rolled numbers.

#### The `services.*` config keys

The key names are contract, because a single YAML file is often read
by a fleet of tools that are not all the same language:

| Key                             | Type     | Default     |
|---------------------------------|----------|-------------|
| `services.<name>.enabled`       | bool     | `false`     |
| `services.<name>.ready_timeout` | duration | `30s`       |
| `services.<name>.stop_timeout`  | duration | `30s`       |
| `services.failure_policy`       | enum     | `fail-fast` |
| `services.shutdown_timeout`     | duration | `60s`       |

`enabled` defaulting to `false` is contract for the reason given in
[Configuration surface](#configuration-surface) — an unrequested open
port is the risk the default guards against. Service-specific keys
live in the same block and are owned by the service.

What is **not** contract: how a port resolves those keys. Go layers
them through viper with flag → env → file → default precedence per
key. A port whose config layer merges whole documents rather than
resolving dotted keys satisfies this section by reading the same key
*names* out of whatever it does resolve; it is not obliged to grow a
precedence engine first. The environment-variable spelling
(`<TOOL>_SERVICES_API_ENABLED`) is contract only for a port that has
env resolution at all.

#### The `--enable` / `--disable` and timeout flags

`--enable <name>` and `--disable <name>` MUST be accepted on `serve`
as repeatable flags, and their rules from
[Configuration surface](#configuration-surface) are contract in full:

- They apply to the supervisor form only, and override
  `services.<name>.enabled` for that one run.
- `--enable` implies configured: a service with no `services.<name>`
  block at all becomes configured and enabled when named, so the flag
  is the aggregate equivalent of the selector's override rule and is
  subject to the same configuration and policy validation.
- `--disable` on an enabled service skips it silently, exactly as a
  configured-but-disabled service is skipped, and MUST NOT affect the
  exit code. A supervisor invocation that resolves to zero services
  after the overrides still exits `2`.
- `--enable` wins over `--disable` for the same name: the affirmative
  act is the more specific one.
- Either flag combined with the selector form is `USAGE`, exit `2`.
  The override rule already decides enablement there, and accepting
  both would let one invocation say two contradictory things.

`--ready-timeout`, `--stop-timeout`, and `--shutdown-timeout` MUST be
accepted by exactly those names, under both forms. The first two apply
one budget to every resolved service, overriding
`services.<name>.ready_timeout` and `services.<name>.stop_timeout`;
the third overrides `services.shutdown_timeout` for the run. The
duration spelling a port accepts is not contract.

`contracts/parity/serve.json` records these as the
`enable_disable_flags` and `timeout_flags` rows, so the fixture and
this text list the same obligations.

### Explicitly not required

Each of these is Go-only. None is forbidden — a port that grows one is
welcome to — but none is a conformance obligation, and a port MUST NOT
be described as non-conformant for lacking one.

| Not required | Why |
|--------------|-----|
| The REST / OpenAPI projection of the command tree | It exists because `cmdreflect` can walk a cobra tree and describe it. It is a projection of Go's command model, not of this contract, and no other SDK has a reflector to project from. |
| The Unix socket service and `transportsvc` seam | Same reason, plus a platform floor: a socket service is not portable to every runtime a kit SDK targets. |
| The built-in `mcp` service | It is a transport service on the Go seam over a reflected cobra tree. Ports serve MCP through their own surfaces; the protocol parity they owe is the MCP surface contract, not this service's wiring. |
| The built-in `rpc` service | Same reason. The wire contract a port owes is `contracts/proto/cmdsurface/v1/commands.proto`, not this service's wiring. |
| `cmdreflect`-driven discovery, and the `invocable: false` reason vocabulary | The reasons (`interactive`, `self-hosting`, `management-only`) are properties of a reflected Go command tree. A port with no reflector has nothing to attach them to. |
| The permission gate (`PermissionFunc`), provenance (`Meta`), audit sinks, and [Middleware](#middleware) | These are the [Security](#security) contract of the *transport services*. A port that serves nothing over a transport has no caller to authenticate, attribute, or audit. They become obligations for a port the day it ships a transport service, not before. |
| The whole [Execution](#execution) section | Result shape, format selection, stream events, cancellation semantics, and tree isolation all describe what happens when a *transport* hands an invocation to a *runner*. Both ends are Go-only today. The flag-baseline and root-factory rules in particular exist because cobra and pflag keep parse state on the command tree; a port whose parser does not is not solving that problem. |
| The `toolspec/policy` table implementation | The policy **gate** is required — a port MUST refuse a service whose declared class its policy denies, at `UNAUTHORIZED` exit `5`. What is not required is Go's YAML-driven `side_effect × network` table. A port satisfies the gate with a two-argument predicate; a port that has wired no policy at all passes every service, exactly as Go does with a nil gate. |
| The api service's `/healthz` and `/readyz` routes ([Readiness over HTTP](#readiness-over-http)) | They belong to the Go api service, which no port ships. A port that ships an HTTP service SHOULD answer the same two routes with the same semantics; the readiness obligation itself — ready once per start, aggregate ready — is required either way. |
| `WithAPI` compatibility, and everything in [Compatibility](#compatibility) | It is a migration path for existing Go adopters of a Go-only option. Nothing to mirror. |
| Nearest-name suggestion on an unknown service | The refusal is contract — `NOT_FOUND`, exit `3`, naming the known services. The Levenshtein suggestion appended to it is a courtesy, and a port that omits it is still correct. |
| Panic-on-dependency-cycle, and topological start ordering | Ordering is required *where a port supports dependency declarations at all*. A port whose registration seam has no `DependsOn` starts in registration order and stops in reverse, which is the same thing with an empty dependency graph. |

Two Go behaviors are worth naming as implementation detail rather than
contract, because they read like rules:

- **Serial start.** Go starts services one at a time, waiting for each
  to report ready before starting the next, because that is what makes
  `DependsOn` mean anything. A port with no dependency declarations
  MAY start concurrently; what it MUST NOT do is report the aggregate
  ready before every service has.
- **Re-execution on the same root.** The rule that a second `serve`
  run must not inherit the first run's context describes a hazard
  cobra creates by storing a context on the command tree. A port whose
  command objects hold no context has nothing to get wrong here; the
  observable requirement — a second run serves until *its own*
  cancellation — still holds.

### Conformance

`contracts/parity/serve.json` records, per language, which of the
required behaviors above a port has shipped. A language that has not
implemented `serve` is marked `PENDING` there rather than `FAIL`: the
harness's job is to make the gap visible, not to fail a build over
work that has not started.

## See also

- [`go/console/serve`](../../go/console/serve/) — Go types for this
  contract.
- [event-topics.md](event-topics.md) — 4-segment topic convention.
- [`go/console/output/envelope/exitcodes.go`](../../go/console/output/envelope/exitcodes.go)
  — exit-code taxonomy.
- [`go/transport/api/server.go`](../../go/transport/api/server.go) —
  the HTTP listen/shutdown primitive services build on.
- [`go/transport/cmdsurface`](../../go/transport/cmdsurface/doc.go) —
  the surface bridge a service projects commands onto.
- [`go/core/config`](../../go/core/config/README.md) — config
  precedence and key resolution.
- [cli-parity-guide.md](../adopters/guides/cli-parity-guide.md) — the
  cross-language CLI contract this page's parity section extends.
- [`contracts/parity`](../../contracts/parity/README.md) — where the
  per-language conformance record lives.
