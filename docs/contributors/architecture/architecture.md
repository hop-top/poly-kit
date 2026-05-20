# How kit is structured

Concept page. Explains how the kit codebase is organised, what
each domain is responsible for, and how the pieces fit together.

## Who this is for

Contributors evaluating where new code belongs. Adopters who want
a mental model before browsing packages.

> Looking for build / release / CI workflow? See
> [`contributors/contributing.md`](../contributing.md) and
> [`contributors/releasing.md`](../releasing.md).

## Purpose

`hop.top/kit` is the polyglot foundation library for the hop-top
ecosystem. It provides a shared substrate of CLI, configuration,
identity, storage, event bus, sync, transport, consent, and
opt-in telemetry primitives so that every tool in the family
looks, behaves, and integrates the same way across Go,
TypeScript, Python, Rust, and PHP.

## Adopter context

kit is consumed by every active hop-top tool that has reached
adopter status: tlc, aps, wsm, rux, ctxt, usp, uhp. Adopters
delegate CLI factory, config layering, output rendering, identity,
and bus to kit and keep only their domain-specific logic.

The companion library
[`hop.top/upgrade`](https://github.com/hop-top/upgrade/blob/main/docs/architecture.md)
predates kit; kit was spun off from upgrade once the shared
patterns stabilised.

## Containers

kit ships as multiple deployable units under one repo:

| Container | Path | Purpose |
|---|---|---|
| Go module `hop.top/kit` | `go/` | Library imported by Go consumers |
| TS package `@hop-top/kit` | `sdk/ts/` | Library for Node + browser consumers |
| Python package `hop-top-kit` | `sdk/py/` | Library for Python consumers |
| Rust crate `kit-rs` *(experimental)* | `sdk/experimental/rs/` | Library for Rust consumers; telemetry parity |
| PHP package `hop-top/kit` *(experimental)* | `sdk/experimental/php/` | Library for PHP consumers; telemetry parity |
| `kit` sidecar binary | `cmd/kit/` | Engine + consent/telemetry subcommand tree |
| `routellm-server` binary | `cmd/routellm-server/` | Standalone LLM router |

## Components

The Go module groups packages into seven role-based domains. Each
domain has a clear charter; cross-domain imports are constrained
to keep boundaries clean.

### AI primitives — `go/ai/` and `incubator/`

Stable packages live under `go/ai/`; experimental packages live
under `incubator/` until promoted.

| Package | Role |
|---|---|
| `go/ai/llm` | Provider-agnostic LLM (Anthropic, OpenAI, Google, Ollama, RouteL2M, Triton); multimodal; fallback; hooks |
| `go/ai/toolspec` | Structured CLI tool knowledge base; registry + source adapters |
| `incubator/qmochi` *(incubator)* | Terminal charting (bar, column, line, sparkline, heatmap, Braille) |
| `incubator/ash` *(incubator)* | AI session history storage and replay |

### `go/console/` — CLI and terminal UX

| Package | Role |
|---|---|
| `console/cli` | Fang+Cobra+Viper root command factory + Theme contract |
| `console/tui` | Pre-themed Bubble Tea v2 components (spinner, progress, dialog, list) |
| `console/wizard` | Interactive command-driven form builder |
| `console/output` | table/json/yaml renderer; owns `--format` flag |
| `console/markdown` | Glamour v2 terminal renderer |
| `console/log` | Viper-configured charm.land/log/v2 wrapper |
| `console/alias` | Git-style command alias bridge to Click/Typer |
| `console/hay` | Breadcrumb / trace protocol for UI hints |
| `console/ps` | Process status utilities |

### `go/core/` — Identity, config, projects, consent, compliance

| Package | Role |
|---|---|
| `core/config` | Layered loader: system → user → project → env; Pkl support; SIGHUP/signal-driven hot reload via `Reloadable[T]` + `reload:"true"` partition |
| `core/consent` | Telemetry consent state machine + persisted decision (`<XDG_CONFIG_HOME>/kit/config.yaml` under `kit.telemetry.consent`; legacy `kit/telemetry.yaml` read as fallback); `DO_NOT_TRACK` / env / flag / prompt precedence |
| `core/identity` | Local-first Ed25519 identity; JWT; symmetric encryption |
| `core/upgrade` | Self-upgrade check, download, replace + Badge |
| `core/util` | Stdlib-only helpers (env, fingerprint, humanize, jsonl, must, ptr, retry, since, slug) |
| `core/uxp` | AI CLI detection, project registry, diagnostics |
| `core/xdg` | XDG Base Directory path resolution |
| `core/projects` | Project registry and metadata lookup |
| `core/compliance` | 13-factor adopter contract; static + runtime sub-checks; F13 `ConsentingTelemetry` |

### `go/runtime/` — Bus, domain, jobs, peers, sync, telemetry

| Package | Role |
|---|---|
| `runtime/bus` | Event-driven pub/sub; memory + SQLite + network transports; `TopicOf` builder + `ParseTopic` + `Qualifiers` payload convention; env-driven sink selection for telemetry routing |
| `runtime/domain` | Generic DDD building blocks (Entity, Repository, StateMachine, Service) |
| `runtime/domain/version` | Append-only version DAG for entity history |
| `runtime/domain/sqlite` | SQLite repository implementations |
| `runtime/job` | Job scheduler interface + Temporal, Restate, Hatchet, DurableTask adapters |
| `runtime/peer` | Decentralised peer discovery; trust mesh; TOFU |
| `runtime/sync` | Local-first multi-remote entity replication; HLC `Clock` + `WallClock` interface (`SystemWallClock` / `FixedClock` / `MockWallClock`) for deterministic tests |
| `runtime/telemetry` | Opt-in, redact-before-egress CLI telemetry; `Mode` gate (off / anon / full), anonymous `install_id`, `ConsentHook` seam, batched HTTPS sink with on-disk spool; cross-language wire-format mirrored by the four SDKs |

### `go/storage/` — Pluggable storage abstractions

| Package | Role |
|---|---|
| `storage/blob` | Abstract blob interface; local + S3 adapters |
| `storage/kv` | Abstract KV with SQLite, etcd, Badger, TiDB |
| `storage/secret` | Vault: env, file, encrypted file, keyring, 1Password, OpenBao, Infisical |
| `storage/sqldb` | SQL database (PostgreSQL/MySQL via Go stdlib) |
| `storage/sqlstore` | Generic SQLite key-value store with TTL |

### `go/transport/` — HTTP and RPC

| Package | Role |
|---|---|
| `transport/api` | HTTP toolkit: router, middleware, resources, OpenAPI 3.1, WebSocket, Huma |
| `transport/rpc` | ConnectRPC unified CRUD over gRPC; generic proto, no per-entity codegen |

### `contracts/` — Shared schemas

Protobuf definitions for cross-language CRUD (`v1`) and RouteL2M
(`v1`) plus TUI parity constants.

### Polyglot SDKs

| SDK | Mirrors |
|---|---|
| `@hop-top/kit/cli` | `console/cli` (Commander.js) |
| `@hop-top/kit/output` | `console/output` |
| `@hop-top/kit/xdg` | `core/xdg` |
| `@hop-top/kit/sqlstore` | `storage/sqlstore` (async) |
| `@hop-top/kit/telemetry` | `runtime/telemetry` |
| `hop_top_kit.cli` | `console/cli` (Typer) |
| `hop_top_kit.output` | `console/output` |
| `hop_top_kit.xdg` | `core/xdg` |
| `hop_top_kit.config` | `core/config` |
| `hop_top_kit.telemetry` | `runtime/telemetry` |
| `kit_rs::telemetry` *(experimental)* | `runtime/telemetry` |
| `HopTop\Kit\Telemetry` *(experimental)* | `runtime/telemetry` |

Parity is enforced via `make test-parity` against shared contract
fixtures. Telemetry envelopes additionally pass byte-identical
through the cross-language harness at
[`sdk/tests/cross-lang/`](../../../sdk/tests/cross-lang/).

## Public surfaces

### Go module API

Consumers import packages directly under `hop.top/kit/go/...`. The
most commonly imported packages, by adopter:

| Adopter | Common imports |
|---|---|
| tlc | cli, output, log, bus, domain, sqlstore, ext, llm, tui |
| aps | cli, output, log, bus, identity (via cxr) |
| wsm | upgrade (only) |
| rux | cli, api, bus, util, projects |
| ctxt | (via c12n) cli, output, viper |
| usp | uxp, output, cli |
| uhp | (cobra+viper directly + kit at v0.2.0-alpha.1) |

### Sidecar `kit` binary

`cmd/kit/main.go` runs alongside consumer binaries to provide
shared services (engine SDK clients, sync coordination). See
[`cmd/kit/README.md`](../../../cmd/kit/README.md).

### `routellm-server`

Standalone LLM router with provider fallback; consumed by agents
that want centralised LLM routing.

### Config schema

`core/config` resolves in layered order. Each layer is loaded via
Viper and merged; later layers override earlier ones:

1. System defaults (compiled-in)
2. System config (`/etc/<tool>/config.yaml`)
3. User config (`~/.config/<tool>/config.yaml`)
4. Project config (`<cwd>/.<tool>/config.yaml` walking up)
5. Environment variables (`<TOOL>_<KEY>`)

Pkl is supported as an alternative to YAML.

### Configuration tiers — user config vs kit options

Two distinct surfaces. Conflating them is the cause of most
"why didn't my change take effect?" questions.

| Tier | Owned by | Mutable at runtime? | Lives in | Examples |
|---|---|---|---|---|
| **User config** | Operator running the binary | Yes | `<XDG_CONFIG_HOME>/<tool>/config.yaml` (Viper-layered per above) + env vars | `kit.telemetry.consent.state`, `kit.bus.enforce`, `kit.log.level` |
| **Kit options** | Adopter building the binary | No (rebuild required) | Go source: `cli.With*` options + `-ldflags -X` build-time injection | `cli.TelemetryConfig.Endpoint`, `PromptOnFirstRun`, `DefaultModeOnGrant`; `runtimetelemetry.DefaultEndpoint` |

User config answers *"how does the user want this binary to behave
right now?"*. Kit options answer *"what kit-framework policy does
the adopter's binary commit to?"*. The split exists because some
decisions — the telemetry collector URL, whether kit may prompt for
consent at all, the default emission tier — are properly the
adopter's call, not the operator's. Baking them in keeps them out
of `--help`, out of `kit telemetry status`, and out of a user's
ability to point a production binary at an attacker-controlled
collector.

Pattern: the adopter wires `cli.With*` options once in `main.go`;
secrets like the collector URL flow in via `-ldflags -X`
from a release-pipeline secret rather than via a source-file
literal. Adopter docs:
[`docs/adopters/reference/telemetry-compliance.md`](../../adopters/reference/telemetry-compliance.md#build-time-configuration-kit-options).

### Bus protocol

`runtime/bus` exposes Subscribe / Publish on topics. Three
transports implement the same interface:

- `memory` — in-process, single binary
- `sqlite` — local persisted, durable across restarts
- `network` — cross-process via dpkms hub on `/ws/bus`

Topic naming convention: `[Source].[Category].[Object].[Action]`
(e.g. `aps.profile.created`, `tlc.task.claimed`,
`rux.session.focus_in`). See [`bus-api.md`](../../adopters/reference/bus-api.md).

## Integrations

| Integration | Direction | Notes |
|---|---|---|
| `hop.top/c12n` | imported by ctxt | classification + metadata extraction; depends on kit |
| `hop.top/cxr` | imported by aps | execution framework on top of kit |
| `hop.top/upgrade` | imported by wsm | predates kit; kit was spun off |
| dpkms (ctxt) hub | network bus transport | `/ws/bus` cross-process event hub |
| Charm libs | Go transitive | bubbletea/v2, lipgloss/v2, log/v2, fang/v2, glamour/v2 |
| Spf13 stack | Go transitive | cobra v1.10, viper v1.21, pflag, afero, fsnotify |

## Related pages

- [Top-level README](../../../README.md) — adopter quickstart
- [`bus-api.md`](../../adopters/reference/bus-api.md) — bus types and topic format
- [`cli-api-reference.md`](../../adopters/reference/cli-api-reference.md) — Go CLI factory
- [`storage-abstractions.md`](../../adopters/concepts/storage-abstractions.md) — pick a storage layer
- [`engine-security.md`](../../adopters/reference/engine-security.md) — identity, trust, threat model
- [`telemetry.md`](../../adopters/guides/telemetry.md) — what kit telemetry collects, how to opt in/out (end users)
- [`telemetry-compliance.md`](../../adopters/reference/telemetry-compliance.md) — F13 ConsentingTelemetry checklist (tool authors)
- [`runtime/telemetry/README.md`](../../../go/runtime/telemetry/README.md) — wire-format + extension points (collaborators)
