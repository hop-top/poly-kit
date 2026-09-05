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
| Policy                  | every invocation passes the surface and destructive gates |
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
rules above, and `enabled` defaults to `false`. Kit ships `socket` on
this seam; `api` predates it and keeps its own implementation.

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
- A refusal raised by the argument parser before the command runs — a
  positional count the command does not accept, an unknown or
  malformed flag, a flag missing its value or its required companion —
  is `USAGE`, exit `2`, rendered as the same envelope the command's own
  refusals use. This holds for every kit command and on every surface:
  the shell exit status, `result.exit_code` over the socket, and the
  REST status it maps to. The parser MUST NOT leave such a refusal as a
  bare exit `1`.

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

The two kit-shipped services own these:

| Key                            | Type   | Default                | Meaning                                                        |
|--------------------------------|--------|------------------------|----------------------------------------------------------------|
| `services.api.addr`            | string | `127.0.0.1:8080`       | HTTP listen address; loopback unless authenticated or opted in |
| `services.api.insecure_remote` | bool   | `false`                | serve the api unauthenticated on a non-loopback address        |
| `services.socket.path`         | string | runtime dir, see below | Unix socket path                                               |

`services.api.addr` defaults to a loopback address, and a non-loopback
value is refused at validation unless `APIConfig.Auth` is set or
`services.api.insecure_remote` is true — see [Security](#security).

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

### Provenance

`cmdsurface.Meta` carries the call's provenance across every
transport. A transport MUST populate the fields it has evidence for
and MUST leave the rest empty; the bridge grants nothing on the basis
of any of them.

| Field            | api service                                    | socket service                              |
|------------------|------------------------------------------------|---------------------------------------------|
| `Caller`         | principal from the `Auth` claims               | verified by `SocketConfig.Auth`, else the request's `caller` as a claim |
| `Tenant`         | tenant from the `Auth` claims                  | verified by `SocketConfig.Auth`, else the request's `tenant` as a claim |
| `Surface`        | `rest`, pinned                                 | `rpc`, pinned by the seam                   |
| `RequestID`      | `X-Request-ID`, issued when absent, echoed     | `request_id`, issued when absent            |
| `TraceID`        | `traceparent` trace-id, else `X-Trace-ID`      | `trace_id`                                  |
| `IdempotencyKey` | `Idempotency-Key`                              | `idempotency_key`                           |
| `RequestedAt`    | receipt time                                   | receipt time                                |
| `Extra`          | `remote_addr`, `scopes` (comma-joined claims)  | —                                           |

- Claims MUST be extractable without the transport importing the
  adopter's types: a value implementing `api.Identity`, an
  `api.Claims`, or a string-keyed map with `sub` and `tenant`. A
  claims value of another shape authenticates the call and leaves it
  unattributed.
- A caller-supplied identity on a transport without an authenticator
  is provenance, not a credential. The socket's `caller` and `tenant`
  are recorded as claimed; `SocketConfig.Auth` is what makes them
  verified, and its verdict replaces the claim.
- The request context MUST reach the bridge unchanged, so a client
  disconnect cancels the invocation on both transports. Whether the
  command stops is the runner's contract.
- The idempotency key MUST reach the leaf's `--idempotency-key` flag
  when the leaf registers one and the caller did not set it. Dedupe
  is the command's own middleware; the transport keeps no store.

### Permission

- `cmdsurface.Bridge.Invoke` applies its gates in this order:
  resolution, surface enablement, invocability (an interactive or
  self-hosting leaf is refused with `ErrNotInvocable` naming the
  reflector's reason, whatever the surface), the destructive ceiling
  (`Policy.Allowed`), then the permission gate (`PermissionFunc`).
  The invocability gate is the bridge's, so a transport that exposed
  the whole tree cannot admit an interactive leaf; the runner's own
  refusal is a backstop. Over REST such commands are withheld at
  mount, so the route is absent (`404`) and discovery carries the
  reason; over the socket the answer is `NOT_INVOCABLE`.
  Confirmation is not a bridge gate: it is the command's own flag and
  its own refusal, an exit code in the Result.
- The permission gate MUST run on every surface, inside the bridge,
  so no transport can bypass it. The default permits everything.
- `cli.WithPermission` installs the adopter's decision on the api
  and socket services. It composes after the tool's policy engine:
  a `--policy` that refuses a side-effect class refuses it for every
  caller, on every surface, before the adopter's decision is asked —
  the same `Engine.Authorize` the CLI runs.
- A refusal returns `ErrPermissionDenied` with a stable reason. It
  is `403 permission_denied` over REST and `DENIED` over the socket,
  distinct from the destructive ceiling's `403 destructive_blocked`
  and `BLOCKED`, because different people fix them.
- Discovery MUST keep a command invocable when the verdict depends
  on the caller; a per-caller answer cannot be pre-computed. A
  decision marked `CallerIndependent` MAY be reflected at mount with
  the reason `permission-denied`, owned by `cmdsurface`, beside the
  reflector's own vocabulary.

### Audit

- Every refusal — authentication (`ErrAuthRefused`, reported by the
  transport through `Bridge.Audit`), enablement, the destructive
  ceiling, permission — and every execution on a remote surface MUST
  reach the bridge's sinks with the Meta above, the command path, and
  the verdict: the refusing error, or the command's Result.
- `cli.WithAuditSinks` registers sinks on every kit-shipped transport
  service. Sinks are best-effort and MUST NOT change a verdict.
- CLI and in-process library invocations are not audited: the first
  is the operator's own act, the second has no caller to attribute.

## Execution

A transport service does not run commands; it hands an invocation to
the bridge, and the bridge hands it to a **runner**. The runner is
where a served invocation becomes a command execution, and this
section is the normative contract for that step: the shape of the
result, the streams, cancellation, what is isolated between
invocations, and the classes of command that never run this way.
Authority: [`go/transport/cmdsurface`](../../go/transport/cmdsurface/)
(`runner.go`, `exec.go`, `result.go`).

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
  [`go/console/output/error.go`](../../go/console/output/error.go)).
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

The kit-shipped `api` and `socket` services are request/reply and use
`Run`. The WebSocket, SSE, and RPC surfaces in `cmdsurface` use
`Stream`.

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
Stopping the service cancels every command in flight.

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
variable. A tree from `cli.New` is prepared for execution inside
`Root.Execute`, where the confirmation and policy gates are installed,
so a factory over `cli.New` yields an ungated tree today; a kit root
uses the shared-tree form.

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
  marks it: position under the depth-1 `serve` verb, which the
  hierarchy above gives to exactly one command; a declared
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

6. **Served invocations are isolated, and `data` is real.** The
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
