# SDK parity inventory

Which Go package each SDK port covers, and how completely. This page
defines "ported" for contributors: a port task is done when its row here
reads `ported`, and a task that adds surface a row does not list changes
the row in the same branch.

## Who this is for

Contributors porting a Go package to Python, TypeScript, Rust or PHP, and
anyone deciding whether a gap is known. Adopters who want to know what an
SDK offers should read
[`capability-parity`](../adopters/reference/capability-parity.md) instead.

## How this page relates to capability parity

| Page | Unit | Answers |
|------|------|---------|
| This page | Go package | Which module in each port covers this package, and is the adopter surface complete? |
| [`capability-parity`](../adopters/reference/capability-parity.md) | Capability | Is the port's behaviour pinned to Go by a shared corpus (`done`) or only present (`unverified`)? |

The two pages answer different questions. A package can be `ported` here
and `unverified` there: the module covers the surface, but no corpus
checks it against Go yet.

## Status vocabulary

| Status | Meaning |
|--------|---------|
| ported | The port exports the Go package's adopter surface. Gaps are limited to Go-only wiring |
| partial | The port covers a subset. The Notes column names what is missing |
| absent | No module for the package in that port |
| n/a | The package has no counterpart by design. The Notes column says why |

A trailing `?` marks a row whose depth was not compared symbol by symbol:
the module exists and its header describes the same job, but nobody has
checked it against the Go exports. Treat `ported?` as "present, depth
unknown".

## What counts as adopter surface

A package is adopter surface when a tool built on kit imports it directly
or gets behaviour from it through the CLI factory. Everything else is
Go-internal and marked `n/a` below. Adapters for one backend (a single
LLM provider, one secret vault) belong to their parent package's row; the
Notes column names the backends a port lacks.

## How to read the module columns

Paths are relative to each port's source root:

| Port | Source root | Package |
|------|-------------|---------|
| Python | `sdk/py/hop_top_kit/` | `hop-top-kit` |
| TypeScript | `sdk/ts/src/` | `@hop-top/kit` |
| Rust | `sdk/rs/src/` | `hop-top-kit` crate, one Cargo feature per module |
| PHP | `sdk/experimental/php/src/` | `hop-top/kit`, namespace `HopTop\Kit` |

A bare name such as `bus` stands for the file or directory of that name
(`bus.py`, `bus.ts`, `bus/`). The inventory was read from the files in
each tree, not from the READMEs; where a README records a gap it was
confirmed against the code.

## `go/console/`

| Go package | Python | TypeScript | Rust | PHP | Notes |
|------------|--------|------------|------|-----|-------|
| `cli` | ported? `cli`, `delegation`, `gate`, `policy`, `safety` | ported? `cli`, `delegation`, `policy`, `safety` | partial `cli/` | partial `Cli/` | Rust ports the root flags, help renderer and delegation gate, not the full factory. PHP carries only the delegation flags; no global-flag layer |
| `cli/completion`, `cli/completionspec` | ported? `completion` | ported `completion*` | ported `cli/completion*` | absent | Corpus runners: Go, Rust, TypeScript |
| `cli/policy` (delegation policy) | partial `policy` | ported? `policy` | ported? `cli/policy` | partial `Cli/Delegation/` | Python carries `permissions:` rules but does not evaluate them |
| `cli/celpermission` | absent | absent | ported `cli/permission` | absent | Needs an expression evaluator |
| `cli/mcpserve`, `cli/rpcserve`, `cli/service`, `cli/peers`, `cli/router` | absent | absent | absent | absent | Go serve plane for the CLI factory |
| `alias` | ported `alias` | ported `alias` | ported `cli/alias` | absent | Corpus `alias-v1`: Go, Rust, TypeScript, Python |
| `output` | ported `output/` | ported `output/` | ported `output/` | ported `Output/` | Exit-code taxonomy pinned in all five |
| `output/envelope` | absent | absent | absent | absent | |
| `tui` | partial `tui/` | partial `tui/` | partial `tui/` | absent `Tui/` | Shared constants plus a few components; no Bubble Tea equivalent. The PHP `Tui` class is an empty placeholder |
| `wizard` | partial `wizard` | partial `wizard` | absent | absent | Headless step engine only. `wizard/wizardtui` is not ported |
| `progress` | ported? `progress` | ported? `progress` | ported `tui/progress` | absent | |
| `log` | ported? `log` | ported `log` | ported? `cli/log` | absent | TypeScript has a parity test |
| `uri` | ported? `uri` | ported? `uri` | ported? `uri` | ported? `Uri/` | Thin facades over the `cite` library; `cite` owns the behaviour |
| `serve` | ported `serve` | ported `serve` | ported `serve/` | ported `Serve/` | Thirteen behaviours pinned in all five by the serve contract |
| `markdown`, `hay`, `ps`, `stage` | absent | absent | absent | absent | |

## `go/core/`

| Go package | Python | TypeScript | Rust | PHP | Notes |
|------------|--------|------------|------|-----|-------|
| `id` | ported `id/` | ported `id/` | ported `id/` | ported `Id/` | TypeID corpus runs in all five |
| `xdg` | ported? `xdg` | ported? `xdg` | partial `xdg/` | absent | Rust lacks cache and runtime dirs, file helpers and the write guard |
| `config` | partial `config` | partial `config` | partial `config/` | absent | Python and TypeScript: YAML layers only, no Pkl, no hot reload. Rust: corpus-pinned, no Pkl wizard. `config-v1` covers Go and Rust |
| `upgrade` | partial `upgrade/` | partial `upgrade/` | partial `upgrade/` | partial `Upgrade/` | Python: release check, lenient semver, `Migration`, `Registry`, the document migrator and the `Migrator` with its driver protocol (no drivers yet). TypeScript: release check, lenient semver, `Migration`, `Registry` and the document migrator, no `Migrator` yet. PHP: the same without the release check. Rust: release check and migrations. None downloads or replaces the binary |
| `util` | absent | absent | partial `util/retry`, `timeutil/` | absent | Go has env, fingerprint, humanize, jsonl, must, ptr, retry, since, slug. Rust ports retry, since and until |
| `scope` | ported `scope` | ported `scope` | absent | absent | Default deny list shared through `scope-defaults.json` |
| `identity` | absent | absent | partial `identity/` | absent | Rust: keypair, JWT, encryption. `cli.WithIdentity` wiring not ported |
| `redact` | partial `telemetry/redact` | partial `telemetry/redact` | ported `redact/` | partial `Telemetry/Redactor` | Python, TypeScript and PHP carry a telemetry-only regex set that says it is not parity with Go. Corpus `redact-v1`: Go and Rust |
| `compliance` | ported `compliance` | ported `compliance` | partial `compliance/` | absent | Rust runs the static pass; the runtime pass is not ported |
| `consent` | partial `telemetry/consent` | partial `telemetry/consent` | ported `consent/` | partial `Telemetry/Consent` | Python, TypeScript and PHP read the persisted telemetry decision only. Corpus `consent-v1`: Go and Rust |
| `breaker` | absent | absent | partial `breaker/` | absent | Rust: circuit breaker. Bulkhead, timeout, fallback, `breaker.yaml`, registry and wrap helpers not ported |
| `netpolicy` | ported? `netpolicy` | ported? `netpolicy` | ported `netpolicy/` | ported? `Net/` | Corpus `netpolicy-v1`: Go and Rust |
| `projects`, `uxp`, `avatar`, `stage` | absent | absent | absent | absent | |

## `go/storage/`

| Go package | Python | TypeScript | Rust | PHP | Notes |
|------------|--------|------------|------|-----|-------|
| `sqldb` | absent | absent | ported? `sqldb` | absent | |
| `kv` | absent | absent | partial `kv` | absent | Rust: SQLite backend only. etcd, TiDB, Badger and memory backends absent by design. Corpus `kv-v1`: Go and Rust |
| `httpcache` | absent | absent | ported `httpcache/` | absent | Contract `httpcache-v1` |
| `blob` | absent | absent | ported `blob/` | absent | Local and S3 backends. Corpus `blob-v1`: Go and Rust |
| `sqlstore` | ported? `sqlstore` | ported? `sqlstore` | ported? `sqlstore/` | absent | Rust adds backup and encrypted store |
| `secret` | absent | absent | absent | absent | No vault backend in any port |

## `go/runtime/`

| Go package | Python | TypeScript | Rust | PHP | Notes |
|------------|--------|------------|------|-----|-------|
| `bus` | partial `bus`, `stream` | partial `bus`, `stream` | partial `bus/` | absent | Python and TypeScript: in-memory only, no network adapter. Rust: network, CloudEvents, sinks, async dispatch; no SQLite adapter. Corpus `bus-v1`: Go and Rust |
| `telemetry` | ported `telemetry/` | ported `telemetry/` | ported `telemetry/` | ported `Telemetry/` | Envelope harness runs all four SDKs |
| `sync` | absent | absent | partial `sync/` | absent | Rust: replicator, remotes, HTTP transport. No durable cursor, `BusPublisher`, SQLite repository. The engine clients under `engine/sdk/` are separate, see below |
| `notify` | absent | absent | ported? `notify/` | absent | Webhook, email, OS sinks. No shared corpus |
| `provenance` | partial `provenance` | partial `provenance` | absent | absent | `_meta` wrapper only. `provenance/wrap` (exec, http, sql) not ported |
| `domain` | absent | absent | partial `domain/` | absent | Rust: repository and state machine. `Service`, validator, audit repository not ported |
| `policy` | absent | absent | ported? `policy/` | absent | Event policy engine; `stage.yaml` pinned byte for byte to Go |
| `sideeffect` | partial `sideeffect` | partial `sideeffect` | partial `sideeffect/` | partial `Cli/SideEffect` | Annotation tiers are shared. Rust has not wired `--dry-run` or the per-command policy tiers |
| `job`, `peer` | absent | absent | absent | absent | |

## `go/transport/`

| Go package | Python | TypeScript | Rust | PHP | Notes |
|------------|--------|------------|------|-----|-------|
| `api` | absent | partial `api` | ported? `api_server/`, `api` | partial `Api/` | TypeScript and PHP: client only. Rust: server toolkit plus guarded client. Corpus `api.json`: Go and Rust |
| `rpc` | absent | partial `rpc` | partial `rpc/` | absent | TypeScript: client. Rust: server, no typed client or command-tree service |
| `cmdsurface` (MCP leg) | ported `mcp/` | ported `mcp/` | ported `mcp/` | ported `Mcp/` | Wire corpus `mcp-wire.json` runs in all five |
| `cmdsurface` (REST, RPC, CLI legs) | absent | absent | partial `api_server/commands` | absent | Rust projects the command tree over REST |
| `mcpsdk` | n/a | n/a | n/a | n/a | Go wrapper over the Go MCP SDK. Ports own the wire directly through the MCP leg above |
| `authn` | absent | absent | ported `authn/` | absent | Corpus `authn-v1`: Go and Rust |
| `webhook` | absent | absent | ported `api_server/webhook` | absent | Corpus `webhook-v1`: Go and Rust |
| `socket` | absent | absent | ported `socket/` | absent | |
| `transportsvc`, `observability` | absent | absent | absent | absent | Rust has request metrics inside `api_server` only |

## `go/ai/`

| Go package | Python | TypeScript | Rust | PHP | Notes |
|------------|--------|------------|------|-----|-------|
| `llm` | partial `llm*` | partial `llm*` | partial `llm/` | absent | Corpus `llm-v1`: Go, Rust, Python, TypeScript. Rust has Anthropic, OpenAI, Google, Ollama and Triton adapters; aim catalog routing not ported. Provider coverage in Python and TypeScript not compared (`?`) |
| `llm/router`, `llm/routellm` | ported? `routellm*` | ported? `router/`, `routellm` | partial `llm/routellm`, `llm/router` | absent | |
| `toolspec` | ported? `toolspec` | ported `toolspec*` | absent | absent | TypeScript pinned by manifest and spec fixtures |
| `cmdreflect`, `ext` | absent | absent | absent | absent | |

## Absent everywhere

`go/bridge`, `go/integrations/repohost`, `go/security`, and the Go
`runtime/job`, `runtime/peer` and `storage/secret` packages have no module
in any port. They are adopter surface; porting them is open work, not a
decision to leave them out.

## Go-internal

| Go area | Why not applicable |
|---------|--------------------|
| `go/conformance`, `go/console/cli/conformance` | Go test tooling for adopter conformance suites. The SDKs run the shared contract corpora instead |
| `go/tools` (`provenancelint`) | A `go/analysis` linter; operates on Go source |
| `cmd/kit`, `cmd/routellm-server` | Binaries, not libraries. The SDKs reach `kit serve` through the engine clients |
| `templates/` | Scaffold sources rendered by `kit init` |
| `incubator/` | Unpromoted Go packages |
| `extensions/` | Independent Go modules released on their own line |

## Engine clients

The engine clients under `engine/sdk/` wrap the `kit serve` HTTP and
WebSocket protocol. They are separate from the library ports above.

| Surface | Python | TypeScript | Rust | PHP |
|---------|--------|------------|------|-----|
| Engine spawn, connect, stop | ported `kit_engine/` | ported `ts-kit-engine/` | absent | absent |
| Collections, events, sync, peers, identity | ported | ported | absent | absent |

The engine parity test runs Go, TypeScript and Python against one
`kit serve`. Rust and PHP clients do not exist yet; the
[engine SDK parity policy](architecture/engine-sdk-parity.md) proposes them.

## Keeping this page true

- A change that adds or removes a port module updates its row in the same
  branch.
- A `?` row stays until someone compares the module's exports with Go and
  promotes or demotes it.
- When a shared corpus starts covering a row, record it in
  [`capability-parity`](../adopters/reference/capability-parity.md) and in
  the Notes column here.
