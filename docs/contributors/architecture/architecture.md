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
identity, storage, event bus, sync, and transport primitives so
that every tool in the family looks, behaves, and integrates the
same way across Go, TypeScript, and Python.

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
| `kit` sidecar binary | `cmd/kit/` | Engine running alongside other binaries |
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
| `console/cli` | Fang+Cobra+Viper root command factory + Theme contract (ADR-0002) |
| `console/tui` | Pre-themed Bubble Tea v2 components (spinner, progress, dialog, list) |
| `console/wizard` | Interactive command-driven form builder |
| `console/output` | table/json/yaml renderer; owns `--format` flag (ADR-0003) |
| `console/markdown` | Glamour v2 terminal renderer |
| `console/log` | Viper-configured charm.land/log/v2 wrapper |
| `console/alias` | Git-style command alias bridge to Click/Typer |
| `console/hay` | Breadcrumb / trace protocol for UI hints |
| `console/ps` | Process status utilities |

### `go/core/` — Identity, config, projects, compliance

| Package | Role |
|---|---|
| `core/config` | Layered loader: system → user → project → env; Pkl support; SIGHUP/signal-driven hot reload via `Reloadable[T]` + `reload:"true"` partition |
| `core/identity` | Local-first Ed25519 identity; JWT; symmetric encryption |
| `core/upgrade` | Self-upgrade check, download, replace + Badge |
| `core/util` | Stdlib-only helpers (env, fingerprint, humanize, jsonl, must, ptr, retry, since, slug) |
| `core/uxp` | AI CLI detection, project registry, diagnostics |
| `core/xdg` | XDG Base Directory path resolution |
| `core/projects` | Project registry and metadata lookup |
| `core/compliance` | Compliance policy engine + audit logging |

### `go/runtime/` — Bus, domain, jobs, peers, sync

| Package | Role |
|---|---|
| `runtime/bus` | Event-driven pub/sub; memory + SQLite + network transports; `TopicOf` builder + `ParseTopic` + `Qualifiers` payload convention (ADR-0017) |
| `runtime/domain` | Generic DDD building blocks (Entity, Repository, StateMachine, Service) |
| `runtime/domain/version` | Append-only version DAG for entity history |
| `runtime/domain/sqlite` | SQLite repository implementations |
| `runtime/job` | Job scheduler interface + Temporal, Restate, Hatchet, DurableTask adapters |
| `runtime/peer` | Decentralised peer discovery; trust mesh; TOFU |
| `runtime/sync` | Local-first multi-remote entity replication; HLC `Clock` + `WallClock` interface (`SystemWallClock` / `FixedClock` / `MockWallClock`) for deterministic tests |

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
| `hop_top_kit.cli` | `console/cli` (Typer) |
| `hop_top_kit.output` | `console/output` |
| `hop_top_kit.xdg` | `core/xdg` |
| `hop_top_kit.config` | `core/config` |

Parity is enforced via `make test-parity` against shared contract
fixtures.

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

## Architecture decisions

The full set of ADRs lives at [`contributors/adr/`](../adr/). Three foundational
ones shape everything else:

- [ADR-0001 CLI framework selection](../adr/0001-cli-framework-selection.md) — Go uses cobra+viper+fang; TS/Py mirror via Commander/Typer
- [ADR-0002 Theme architecture](../adr/0002-theme-architecture.md) — Single theme per tool; brand accent override via `console/cli.Theme`
- [ADR-0003 Output hints pipeline](../adr/0003-output-hints-pipeline.md) — Human vs machine split: progress/help → stderr, structured → stdout; `--format` flag

## Related pages

- [Top-level README](../../../README.md) — adopter quickstart
- [`bus-api.md`](../../adopters/reference/bus-api.md) — bus types and topic format
- [`cli-api-reference.md`](../../adopters/reference/cli-api-reference.md) — Go CLI factory
- [`storage-abstractions.md`](../../adopters/concepts/storage-abstractions.md) — pick a storage layer
- [`engine-security.md`](../../adopters/reference/engine-security.md) — identity, trust, threat model
