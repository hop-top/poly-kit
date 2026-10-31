# Capability index

> What kit gives you, one row per capability: what it does, where it lives in the
> Go implementation, and where its usage docs are. Per-SDK parity is in
> [`capability-parity`](capability-parity.md).

## Who this is for

Adopters choosing kit primitives before hand-rolling them, and SDK authors
looking for the next gap to close. [`go-primitives`](go-primitives.md) is the
import-path catalog by task; this page is the capability catalog by area, with
a documentation-status column and a parity matrix beside it.

## How to read this page

- Go (`go/`) is the reference implementation; every row is derived from it.
- One capability is one adopter-facing package or one cohesive primitive.
  Small helpers sit with their parent (secret backends, notify sinks, LLM
  providers, conformance sub-packages).
- Usage docs link a page under `docs/`. `not yet documented` means no page
  under `docs/` covers the capability; a package README, when it is
  substantial, is linked as the interim pointer and does not count as docs.
- 74 capabilities; 57 have docs under `docs/`, 17 do not yet.
- Parity status per SDK: see [`capability-parity`](capability-parity.md).

## CLI and console

The root-command factory and everything a command prints, prompts or completes.

| Capability | What it does | Go package | Usage docs |
|---|---|---|---|
| CLI factory | Root command with the global-flag contract, themed help, config wiring, version | `go/console/cli` | [CLI API reference](cli-api-reference.md); [Getting started](../guides/getting-started-cli.md) |
| Output and errors | `--format` table/json/yaml/csv, column projection, error envelope, exit-code taxonomy | `go/console/output` | [Output reference](output.md) |
| Logging | charm log wrapper honouring `quiet` / `no-color` | `go/console/log` | [Log API](log-api.md) |
| Markdown rendering | Glamour terminal markdown renderer | `go/console/markdown` | [render-markdown-in-the-terminal](../guides/render-markdown-in-the-terminal.md) |
| Command annotations | Reads kit cobra annotations off a command (cobra-specific leaf) | `go/console/cli/cmdmeta` | [declare-and-read-command-annotations](../guides/declare-and-read-command-annotations.md) |
| Shell completion | Dynamic flag/arg completion, completion scripts, Fig and Usage spec export | `go/console/cli/completion` | [Completion API](completion-api.md); [Completion user guide](../guides/completion-user-guide.md) |
| Aliases | Git-style user-defined command aliases | `go/console/alias` | [Alias API](alias-api.md) |
| Flag helpers | Enum flags, `SetFlag`, `TextFlag`, flag registration | `go/console/cli` | [Flag enums](flag-enums.md); [SetFlag/TextFlag](setflag-textflag-api.md) |
| Help rendering | Root `--help` layout contract, `--help-all` | `go/console/cli` | [Help rendering](help-rendering.md) |
| Autocorrect | Rewrites mistyped flag names; unknown commands get cobra did-you-mean | `go/console/cli` | [enable-flag-autocorrect](../guides/enable-flag-autocorrect.md) |
| Config path commands | Ready-made `config path` / `config paths` subcommands | `go/console/cli/config` | [Inspect config paths](../guides/inspect-config-paths.md) |
| URI scheme handlers | Register `yourtool://` schemes and handler commands | `go/console/uri` | [register-custom-uri-schemes](../guides/register-custom-uri-schemes.md) |
| Fuzzy matching | Subsequence, substring, Levenshtein disambiguation | `go/console/hay` | [Hay](hay.md) |
| Process status | Cross-tool `<tool> ps` convention | `go/console/ps` | [ps](ps.md) |
| Wizard | Interactive form/prompt engine (headless, line-based, Bubble Tea) | `go/console/wizard` | [Wizard API](wizard-api.md) |
| TUI components | Themed Bubble Tea components, styles, dialogs, AppShell | `go/console/tui` | [TUI component gallery](../guides/tui-component-gallery.md); [Migrate to AppShell](../guides/migrate-to-appshell.md) |
| Progress events | Per-phase progress events; human lines vs JSON chosen by kit | `go/console/progress` | [report-command-progress](../guides/report-command-progress.md) |

## Config, storage and secrets

Where configuration resolves from and where state and secrets live.

| Capability | What it does | Go package | Usage docs |
|---|---|---|---|
| Layered config | System, user, project, env layers; hot reload; PKL schema support | `go/core/config` | [Config](config.md); [Inspect config paths](../guides/inspect-config-paths.md) |
| XDG paths | XDG Base Directory resolution for config, data, cache, state | `go/core/xdg` | [XDG](xdg.md) |
| Project registry | Reader/writer for the shared project registry | `go/core/projects` | [use-the-project-registry](../guides/use-the-project-registry.md) |
| Key-value store | KV interface, TTL store, memory/SQLite/Badger/etcd/TiDB drivers | `go/storage/kv` | [Storage abstractions](../concepts/storage-abstractions.md) |
| Blob store | Object storage interface for opaque bytes | `go/storage/blob` | [store-files-with-blob](../guides/store-files-with-blob.md) |
| SQLite connections | Shared SQLite management: WAL, busy timeout, foreign keys | `go/storage/sqldb` | [Storage abstractions](../concepts/storage-abstractions.md) |
| SQLite JSON store | Generic JSON KV over SQLite with backup/restore and at-rest encryption | `go/storage/sqlstore` | [use-the-sqlite-json-store](../guides/use-the-sqlite-json-store.md) |
| HTTP cache | Caching `http.RoundTripper` over a TTL store | `go/storage/httpcache` | [cache-http-responses](../guides/cache-http-responses.md) |
| Secrets | Store/MutableStore/Keeper plus env, keyring, age file, file, 1Password, Infisical, GitHub Actions, OpenBao, composite and memory backends | `go/storage/secret` | [Secret management guide](../guides/secret-management-guide.md) |

## Serving and transports

Projecting one command tree onto REST, RPC, socket and MCP.

| Capability | What it does | Go package | Usage docs |
|---|---|---|---|
| Serve lifecycle | Service contract, registry, supervisor, failure policy, `serve` command | `go/console/serve` | [Serve lifecycle contract](../../contracts/serve-lifecycle.md); [Migrate to served commands](../guides/migrate-to-served-commands.md) |
| Service command group | `service` command group with built-in api and socket services | `go/console/cli/service` | [Manage the service lifecycle](../guides/manage-the-service-lifecycle.md); [Migrate to cli/service](../guides/migrate-to-cli-service.md) |
| Command surface bridge | Projects one cobra tree onto REST, WebSocket, Socket, RPC and MCP | `go/transport/cmdsurface` | [cmdsurface](cmdsurface.md); [cmdsurface example](../guides/cmdsurface-example.md) |
| Transport service seam | Register a transport service: reflect once at startup, mount on the supervisor | `go/transport/transportsvc` | [transportsvc](transportsvc.md); [Build a transport service](../guides/build-a-transport-service.md) |
| REST API toolkit | Router, middleware, resources, OpenAPI 3.1, WebSocket; typed clients | `go/transport/api` | [Expose over REST](../guides/expose-cli-over-rest.md); [Transport API](transport-api.md) |
| ConnectRPC | Connect/gRPC/gRPC-Web scaffold with interceptors; typed client | `go/transport/rpc` | [Expose over gRPC](../guides/expose-cli-over-grpc.md) |
| Unix socket | Command tree as NDJSON over an owner-only Unix domain socket | `go/transport/socket` | [Serve over a Unix socket](../guides/serve-cli-over-unix-socket.md); [Socket wire](socket-wire.md) |
| MCP server | Command tree as Model Context Protocol tools; tasks, confirmation, audit | `go/transport/mcpsdk` | [Expose over MCP](../guides/expose-cli-over-mcp.md); [Serve MCP from any SDK](../guides/serve-mcp-from-any-sdk.md); [mcpsdk](mcpsdk.md) |
| Served authentication | Bearer verification: tool JWT, JWKS, OIDC discovery, API keys | `go/transport/authn` | [Secure remote serving](../guides/secure-remote-serving.md) |
| Served middleware | Permission, quota, rate limit, audit, usage ledger for served commands | `go/transport/api` | [Served middleware](served-middleware.md) |
| Served observability | OpenTelemetry spans and metrics for served commands | `go/transport/observability` | [Served observability](served-observability.md) |
| Bridge payloads | Payload types, manifest loader, payload-to-handler matching | `go/bridge` | [route-bridge-payloads](../guides/route-bridge-payloads.md) |

## Events, jobs and runtime

In-process events, background work, replication and telemetry.

| Capability | What it does | Go package | Usage docs |
|---|---|---|---|
| Event bus | Pub/sub over topics; memory, SQLite and network adapters; CloudEvents | `go/runtime/bus` | [Bus overview](../concepts/bus-overview.md); [Bus API](bus-api.md); [Hook CLI into bus](../guides/hook-cli-into-bus.md) |
| Job queue | Backend-agnostic async jobs: `Job`, `Service`, `Status`, in-memory mock | `go/runtime/job` | [run-background-jobs](../guides/run-background-jobs.md) |
| Notifications | Outbound sinks (email, webhook, desktop) with severity filtering and retry | `go/runtime/notify` | [Notifications overview](../concepts/notifications-overview.md); [Notify sinks](notify-sinks.md) |
| Domain building blocks | Entity, repository, state machine, SQLite repository, version DAG | `go/runtime/domain` | [Domain events](domain-events.md); [Engine overview](../concepts/engine-overview.md) |
| Entity sync | Local-first entity replication across remotes, HLC ordering | `go/runtime/sync` | [Engine sync](engine-sync.md) |
| Peer mesh | Decentralised peer discovery and TOFU trust; `peer` subcommands | `go/runtime/peer` | [Trust a peer](../guides/trust-a-peer.md) |
| Telemetry | Opt-in, redact-before-egress usage telemetry with on-disk spool | `go/runtime/telemetry` | [Telemetry guide](../guides/telemetry.md); [Telemetry reference](telemetry.md); [Telemetry compliance](telemetry-compliance.md) |

## Guardrails and trust

What a tool is allowed to touch, and proof of who it is.

| Capability | What it does | Go package | Usage docs |
|---|---|---|---|
| Path scope | Filesystem path policy with default deny rules; `scope show` and `scope check` | `go/core/scope` | [Scope](scope.md) |
| Breaker | Circuit breaker, byte/op budgets, rate limiter; `breaker` subcommands | `go/core/breaker` | [Breaker](breaker.md) |
| Network policy | `--offline` enforcement beneath the default HTTP client; typed refusal | `go/core/netpolicy` | [Offline refusals](../guides/cli-parity-guide.md#offline-refusals) |
| Redaction | Secret and PII redaction with gitleaks rule semantics | `go/core/redact` | [Redact](redact.md) |
| Delegation safety | Command-layer guard: `--confirm`, `--max-ops`, policy files for destructive ops; stricter off-TTY | `go/console/cli/policy` | [guard-destructive-commands](../guides/guard-destructive-commands.md) |
| Event policy engine | Declarative rules over bus events; optional CEL evaluator | `go/runtime/policy` | [Configure bus enforcement](../guides/configure-bus-enforcement.md); [Choose enforcement mode](../guides/choose-enforcement-mode.md) |
| Operating mode | Stages (active, feature_freeze, maintenance, sunset); `stage` subcommand | `go/core/stage` | [Stage](stage.md) |
| Consent grants | Per-capability consent with audit and revoke; telemetry-consent migration | `go/core/consent` | [Consent grants](consent-grants.md) |
| Side-effect seams | Exec, FS, HTTP, Bus interfaces as the mutation boundary; recording fakes | `go/runtime/sideeffect` | [Side effects](sideeffect.md) |
| Provenance | Records where each output field came from; analyzer for lint | `go/runtime/provenance` | [Provenance](provenance.md) |
| Audit log | Hash-chained append-only audit log and verifier | `go/security` | [keep-a-tamper-evident-audit-log](../guides/keep-a-tamper-evident-audit-log.md) |
| Identity | Local-first Ed25519 keypairs, JWT claims, symmetric key derivation | `go/core/identity` | [Engine security](engine-security.md) |
| TypeID | Prefixed, sortable entity IDs; cross-language wire form | `go/core/id` | [use-typeid-entity-ids](../guides/use-typeid-entity-ids.md) |
| Upgrade and migrations | Version check, download, replace; versioned state migrations | `go/core/upgrade` | [upgrade-and-migrate](../guides/upgrade-and-migrate.md) |
| Utilities | Retry with backoff, env, fingerprint, humanize, jsonl helpers | `go/core/util` | [util-helpers](util-helpers.md) |
| Avatars | Provider-agnostic avatar URL / data-URI generation from a seed | `go/core/avatar` | [generate-avatars](../guides/generate-avatars.md) |

## AI and agents

Talking to models, and being legible to agents.

| Capability | What it does | Go package | Usage docs |
|---|---|---|---|
| LLM client | Provider-agnostic config, registry, messages, tool calls, streaming, error taxonomy | `go/ai/llm` | [LLM](llm.md) |
| LLM providers | Anthropic, OpenAI, Google, Ollama, Triton adapters | `go/ai/llm/anthropic` | [LLM](llm.md) |
| LLM routing | Strong/weak model routing, RouteLLM config; `llm router` subcommands | `go/ai/llm/router` | [LLM](llm.md) |
| Toolspec | Tool manifest, `spec` command, per-harness adapters, risk policy | `go/ai/toolspec` | [Toolspec API](toolspec-api.md); [Adopter guide](../integrations/toolspec-adopter-guide.md); [Harness guide](../integrations/toolspec-harness-guide.md) |
| Command reflection | Single place kit reflects a cobra tree into descriptors | `go/ai/cmdreflect` | [cmdreflect](cmdreflect.md) |
| Agent CLI invocation | Detect agent CLIs and build native argv from one normalized invocation | `go/core/uxp` | [uxp](uxp.md) |
| Extensions | Extension contract: declared capabilities, discovery, hooks, registry | `go/ai/ext` | [Extension discovery protocol](../../contracts/ext-discover-protocol.md) |
| 12-factor compliance check | Checks a tool against the 12-factor AI CLI spec, static and dynamic | `go/core/compliance` | [Compliance API](compliance-api.md) |

## Conformance and integrations

Testing a tool against the contract; talking to code hosts.

| Capability | What it does | Go package | Usage docs |
|---|---|---|---|
| Conformance assertions | `AssertCLI`: assert a root satisfies the kit contract | `go/conformance` | [Conformance](conformance.md); [Enforce CLI conformance](../guides/enforce-cli-conformance.md) |
| Conformance harness | Integration toolkit asserting contract properties under controlled runs | `go/conformance/harness` | [Conformance](conformance.md) |
| Scenario and story DSL | Closed-vocabulary YAML rubrics and stories with parser and validation | `go/conformance/scenario` | [Conformance](conformance.md) |
| Cassettes and grading client | Record a scenario into a cassette, upload for grading, write the badge | `go/conformance/recorder` | [Conformance](conformance.md) |
| Repo host drivers | Unified driver over GitHub, GitLab, Gitea, Gitee and others | `go/integrations/repohost` | [Repohost auth](../integrations/repohost/auth.md) |

## Not indexed

- Internal decomposition, test-only packages, `package main` binaries and
  auto-generated rule corpora: see
  [what is not in `go-primitives`](go-primitives.md#what-is-not-in-this-index).
- Command-wiring packages under `go/console/cli/` that only mount a
  capability listed above (`mcpserve`, `rpcserve`, `svcconfig`,
  `celpermission`, `flagerrexit`, `idemstore`).
- The sidecar engine under `engine/`: it lives outside `go/` and has its own
  docs ([`run-the-engine`](../guides/run-the-engine.md),
  [`engine-protocol`](engine-protocol.md)).
- SDK-only modules with no Go package of the same name (for example the
  TypeScript `aim` and `stream`, Rust `timeutil`).
- `incubator/` packages (experimental until promoted).

## See also

- [`go-primitives`](go-primitives.md): the same Go packages grouped by task
- [`parity-contract`](parity-contract.md): every cross-language contract and its guard
- [`cli-parity-guide`](../guides/cli-parity-guide.md): the CLI contract the SDKs share
