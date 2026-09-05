# Go Primitives Index

> Everything the Go SDK (`hop.top/kit`) hands you, grouped by what
> you are trying to do. Use it to find the primitive you would
> otherwise hand-roll, then follow the link to the deep-dive.

## Who this is for

Go authors building a CLI or service on kit who want to know what
already exists before writing it themselves. This page is a map,
not a tutorial: each entry says what a primitive is, when to reach
for it, its import path, and where the detail lives.

If you want the CLI factory specifically, start at
[`cli-api-reference.md`](cli-api-reference.md). If you are
modifying kit itself, you want
[`contributors/architecture/architecture.md`](../../contributors/architecture/architecture.md)
instead — that page explains how kit is built; this one explains
what kit gives you.

## Before you begin

```bash
go get hop.top/kit@latest
```

Every path below is under the single module `hop.top/kit`. There is
one module, so one `go get` makes all of it importable; you pay
transitive dependency cost only for what you actually import.

## How to read this page

Packages are grouped by the job you are doing, which mostly — but
not always — follows the directory layout. Where a job spans
directories (`console/cli/policy` and `runtime/policy` are two
different policy engines; secrets live under `storage/` but are a
security concern) the job wins and the table says so.

Three things are worth knowing before you scan the tables:

- **Import path is exact.** Copy it verbatim. A few packages have
  a package clause that differs from the last path segment; those
  are flagged and need an import alias.
- **`Cmd()` means a subcommand, not a library.** Some packages
  hand you a ready-made cobra command tree to mount into your root
  rather than an API to call. They are marked *subcommand*.
- **Not everything under `go/` is for you.** kit has 194 non-test
  packages; this page names the 120 an adopter plausibly imports.
  The rest are internal plumbing, and
  [what was excluded](#what-is-not-in-this-index) is listed at the
  bottom so the judgement is reviewable.

## I need to build a CLI

The starting point for every kit-based tool.

| Primitive | What it is | Reach for it when | Import path |
|---|---|---|---|
| cli | Cobra + Viper + Fang root-command factory; owns global flags, theme, config wiring | Always — this is the entry point | `hop.top/kit/go/console/cli` |
| output | `--format` renderer: table, JSON, YAML, plus structured error envelopes and next-step hints | Any command that prints results | `hop.top/kit/go/console/output` |
| log | Thin `charm.land/log/v2` wrapper reading `quiet` / `no-color` from viper | You want a logger honouring the shared flag contract | `hop.top/kit/go/console/log` |
| markdown | Glamour terminal markdown renderer; stateless functions | Printing rich help or docs to a terminal | `hop.top/kit/go/console/markdown` |
| cmdmeta | Reads kit's cobra annotations off a command; leaf package, zero kit imports | Writing something that inspects commands without importing `cli` | `hop.top/kit/go/console/cli/cmdmeta` |
| completion | Dynamic value completion for flags and positional args | Adding shell completion beyond static lists | `hop.top/kit/go/console/cli/completion` |
| alias | Git-style user-defined command aliases backed by YAML | Letting users define their own shortcuts | `hop.top/kit/go/console/alias` |
| cli/config | Ready-made `config path` / `config paths` subcommands | Exposing where config resolved from *(subcommand)* | `hop.top/kit/go/console/cli/config` |
| uri | Custom URI-scheme registration and handler commands | Your tool should be launchable from `yourtool://` links | `hop.top/kit/go/console/uri` |
| hay | Fuzzy matching and disambiguation (subsequence, substring, Levenshtein, combined) | Resolving a partial name a user typed | `hop.top/kit/go/console/hay` |

Deep dives: [`cli-api-reference.md`](cli-api-reference.md),
[`completion-api.md`](completion-api.md),
[`alias-api.md`](alias-api.md),
[`help-rendering.md`](help-rendering.md),
[`setflag-textflag-api.md`](setflag-textflag-api.md).

## I need a terminal UI

| Primitive | What it is | Reach for it when | Import path |
|---|---|---|---|
| tui | Pre-themed Bubble Tea v2 components: Badge, Confirm, Progress, List, Dialog, Overlay, AppShell | Building an interactive full-screen UI | `hop.top/kit/go/console/tui` |
| tui/styles | Semantic colors from `cli.Theme` compiled into lipgloss styles | Styling your own components to match kit | `hop.top/kit/go/console/tui/styles` |
| tui/dialog | Modal dialog interface plus an overlay stack | Layering modals over base content | `hop.top/kit/go/console/tui/dialog` |
| wizard | Interactive form/prompt engine; runs headless, line-based, or TUI | Multi-step guided input, scriptable in CI | `hop.top/kit/go/console/wizard` |
| wizard/wizardtui | Bubble Tea frontend for the wizard engine, split out to isolate its deps | You want the wizard rendered as a TUI | `hop.top/kit/go/console/wizard/wizardtui` |
| progress | Per-phase progress events; kit decides human lines vs JSON | Long-running commands that must report under `--format json` | `hop.top/kit/go/console/progress` |

Deep dives: [`wizard-api.md`](wizard-api.md),
[`tui-component-gallery.md`](../guides/tui-component-gallery.md).

## I need configuration and paths

| Primitive | What it is | Reach for it when | Import path |
|---|---|---|---|
| config | Layered loader: system → user → project → env, with hot reload via `Reloadable[T]` | Any tool with a config file | `hop.top/kit/go/core/config` |
| config/pkl | Pkl schema support: resolution, validation, wizard generation | You want a typed, validated config schema instead of loose YAML | `hop.top/kit/go/core/config/pkl` |
| xdg | XDG Base Directory resolution for config, data, cache, state | Deciding where to put a file | `hop.top/kit/go/core/xdg` |
| projects | Reader/writer for the shared project registry | Mapping a directory to a registered project | `hop.top/kit/go/core/projects` |

Deep dive: [`inspect-config-paths.md`](../guides/inspect-config-paths.md).

## I need to store something

Pick the layer by shape of the data, not by backend.

| Primitive | What it is | Reach for it when | Import path |
|---|---|---|---|
| kv | Minimal key-value interface plus `TTLStore` | Simple keyed values, backend swappable | `hop.top/kit/go/storage/kv` |
| blob | Object storage interface for opaque bytes | Files and payloads too big for KV | `hop.top/kit/go/storage/blob` |
| sqlstore | Generic JSON-marshalling KV over SQLite, with backup/restore and encryption | You want persistence without designing a schema | `hop.top/kit/go/storage/sqlstore` |
| sqldb | Shared SQLite connection management: WAL, busy timeout, foreign keys, migrations | You own your schema and want sane connection defaults | `hop.top/kit/go/storage/sqldb` |
| httpcache | Caching `http.RoundTripper` backed by a `kv.TTLStore` | Caching upstream HTTP responses | `hop.top/kit/go/storage/httpcache` |

Backends implement the interface above them; import the one you
want and the abstraction stays swappable.

| Backend | For | Import path | Note |
|---|---|---|---|
| kv/sqlite | `kv` | `hop.top/kit/go/storage/kv/sqlite` | Local file, no server; reachable via `kv.Open` |
| kv/badger | `kv` | `hop.top/kit/go/storage/kv/badger` | Embedded, no server; reachable via `kv.Open` |
| kv/etcd | `kv` | `hop.top/kit/go/storage/kv/etcd` | Needs an etcd cluster; blank-import to reach it via `kv.Open` |
| kv/tidb | `kv` | `hop.top/kit/go/storage/kv/tidb` | Needs a TiDB/MySQL server; blank-import to reach it via `kv.Open` |
| blob/local | `blob` | `hop.top/kit/go/storage/blob/local` | Local filesystem |
| blob/s3 | `blob` | `hop.top/kit/go/storage/blob/s3` | You supply the AWS SDK client |

`kv.Open` dispatches on `Config.Backend` through a driver registry:
blank-import the backend package you want and it registers itself. A
name whose driver was never imported is refused with an error naming
the package to import. Use `kv.OpenContext` where you have a context —
it lets a driver police its initial connect, which is what makes
`--offline` cover the first dial and not only later queries.

Deep dive: [`storage-abstractions.md`](../concepts/storage-abstractions.md).

## I need to handle secrets

Secrets live under `storage/` but are a security decision: the
interface is uniform, the trust model is not.

| Primitive | What it is | Reach for it when | Import path |
|---|---|---|---|
| secret | `Store` / `MutableStore` / `Keeper` interfaces plus backend registry | Always — code against this, not a backend | `hop.top/kit/go/storage/secret` |
| secret/composite | Routes operations across several backends by key pattern | Different secrets belong in different vaults | `hop.top/kit/go/storage/secret/composite` |

Backends come in two flavours. Most register a scheme on import,
so a blank import plus `secret.Open(secret.Config{Backend: "..."})`
is all you need. Two do not register and must be constructed
directly.

| Backend | Trust model | `secret.Open` scheme | Import path |
|---|---|---|---|
| env | Process environment | `env` | `hop.top/kit/go/storage/secret/env` |
| keyring | OS keychain | `keyring` | `hop.top/kit/go/storage/secret/keyring` |
| agefile | age-encrypted YAML file | `agefile` | `hop.top/kit/go/storage/secret/agefile` |
| memory | In-process, for tests | `memory` | `hop.top/kit/go/storage/secret/memory` |
| onepassword | 1Password; shells out to `op` | `onepassword` | `hop.top/kit/go/storage/secret/onepassword` |
| infisical | Infisical service | `infisical` | `hop.top/kit/go/storage/secret/infisical` |
| ghsecrets | GitHub Actions secrets, write-only | `ghsecrets` | `hop.top/kit/go/storage/secret/ghsecrets` |
| file | Plaintext file, optional at-rest encryption | *(none — call `file.New`)* | `hop.top/kit/go/storage/secret/file` |
| openbao | OpenBao/Vault server | *(none — call `openbao.New`)* | `hop.top/kit/go/storage/secret/openbao` |

`secret/local` is not a store: it is a `secret.Keeper`
(NaCl secretbox keyed off an `identity.Keypair`). Pair it with
`file.New` when you want the plaintext-file backend encrypted at
rest — `hop.top/kit/go/storage/secret/local`.

Deep dive: [`secret-management-guide.md`](../guides/secret-management-guide.md).

## I need to serve my commands somewhere else

kit's distinguishing move: write a cobra command once, project it
onto other surfaces without rewriting it.

| Primitive | What it is | Reach for it when | Import path |
|---|---|---|---|
| cmdsurface | The bridge projecting one cobra tree onto REST, WebSocket, SSE, MCP, RPC, cron, webhooks, Lambda | Any "expose my CLI as X" job | `hop.top/kit/go/transport/cmdsurface` |
| transportsvc | Registration seam for transport services: reflect once at start, pinned surface, readiness, ordered stop | Writing your own transport | `hop.top/kit/go/transport/transportsvc` |
| serve | Service contract, registry, supervisor, failure policy; imports no transport | Running services with lifecycle management | `hop.top/kit/go/console/serve` |
| api | HTTP toolkit: router, middleware, resources, OpenAPI 3.1, WebSocket, Huma | Building a JSON API by hand | `hop.top/kit/go/transport/api` |
| api/client | Typed REST and WebSocket clients for kit API services | Calling a kit service from Go | `hop.top/kit/go/transport/api/client` |
| mcpsdk | Projects a cmdsurface bridge onto Model Context Protocol | Exposing commands as MCP tools for agents | `hop.top/kit/go/transport/mcpsdk` |
| rpc | ConnectRPC scaffold with interceptors mirroring the HTTP middleware | gRPC/Connect CRUD without per-entity codegen | `hop.top/kit/go/transport/rpc` |
| rpc/client | Typed ConnectRPC client for kit entity services | Calling a kit RPC service | `hop.top/kit/go/transport/rpc/client` |
| socket | Serves the command tree as NDJSON over a Unix domain socket | Local IPC without a network port | `hop.top/kit/go/transport/socket` |
| bridge | Payload types, manifest loader and payload-to-handler matching for OS-level shells. Partial — see below | Wiring a Share Extension, Shortcut or browser extension | `hop.top/kit/go/bridge` |

`socket` is loopback-only with owner-only permissions and does no
caller authentication beyond filesystem mode. `bridge` today ships
the wire types, the manifest loader and the matcher that picks which
installed handler should take a payload. An embeddable receiver is
not implemented, so a host still has to execute the winning handler
itself — spawning the subprocess, opening the socket or making the
HTTP call — and to accept payloads from the shells in the first
place; treat the package as incomplete.

Deep dives: [`expose-cli-over-rest.md`](../guides/expose-cli-over-rest.md),
[`expose-cli-over-mcp.md`](../guides/expose-cli-over-mcp.md),
[`serve-cli-over-unix-socket.md`](../guides/serve-cli-over-unix-socket.md),
[`build-a-transport-service.md`](../guides/build-a-transport-service.md),
[`engine-protocol.md`](engine-protocol.md).

## I need events and background work

| Primitive | What it is | Reach for it when | Import path |
|---|---|---|---|
| bus | Pub/sub over topics; memory, SQLite and network transports behind one interface | Decoupling producers from consumers; hooking a CLI | `hop.top/kit/go/runtime/bus` |
| job | Backend-agnostic async job queue: `Job`, `Service`, `Status`, poller, state machine | Work that outlives the request | `hop.top/kit/go/runtime/job` |
| notify | Outbound notification sinks with severity filtering and retry | Telling a human something happened | `hop.top/kit/go/runtime/notify` |
| domain | Generic DDD building blocks: Entity, Repository, StateMachine, audit | Modelling entities with lifecycle and audit | `hop.top/kit/go/runtime/domain` |
| domain/sqlite | SQLite-backed `domain.Repository` and `AuditRepository` | Persisting domain entities locally | `hop.top/kit/go/runtime/domain/sqlite` |
| domain/version | Append-only version DAG over a domain repository | Entity history you can walk | `hop.top/kit/go/runtime/domain/version` |
| sync | Local-first entity replication across multiple remotes, HLC clock | Offline-capable data that reconciles later | `hop.top/kit/go/runtime/sync` |
| peer | Decentralised peer discovery and trust mesh, TOFU | Peer-to-peer tool topologies | `hop.top/kit/go/runtime/peer` |

Job backends — the core types are engine-agnostic; pick one:

| Backend | Needs | Import path |
|---|---|---|
| durabletask | SQLite only — the zero-infrastructure default | `hop.top/kit/go/runtime/job/durabletask` |
| temporal | A Temporal server | `hop.top/kit/go/runtime/job/temporal` |
| restate | A Restate runtime | `hop.top/kit/go/runtime/job/restate` |
| hatchet | A Hatchet server, and you inject the client | `hop.top/kit/go/runtime/job/hatchet` |
| mock | Nothing; in-memory, for tests | `hop.top/kit/go/runtime/job/mock` |

The hatchet adapter does not import the Hatchet SDK: you pass in
anything satisfying its local `HatchetClient` interface.

Notification sinks. Each package's *package clause differs from
its directory* — alias the import as shown:

| Sink | Import as | Needs | Import path |
|---|---|---|---|
| email | `emailsink` | An SMTP server for the shipped mailer | `hop.top/kit/go/runtime/notify/sinks/email` |
| webhook | `webhooksink` | An HTTP endpoint | `hop.top/kit/go/runtime/notify/sinks/webhook` |
| desktop | `osnotifysink` | `osascript` (macOS) or `notify-send` (Linux) | `hop.top/kit/go/runtime/notify/sinks/osnotify` |

Deep dives: [`bus-api.md`](bus-api.md),
[`bus-overview.md`](../concepts/bus-overview.md),
[`domain-events.md`](domain-events.md),
[`notifications-overview.md`](../concepts/notifications-overview.md),
[`hook-cli-into-bus.md`](../guides/hook-cli-into-bus.md).

## I need guardrails on what my tool can do

kit ships several distinct enforcement primitives. They are not
interchangeable — this is the table that tells them apart.

| Primitive | Guards | Reach for it when | Import path |
|---|---|---|---|
| scope | Filesystem paths | Restricting which paths an operation may touch | `hop.top/kit/go/core/scope` |
| breaker | Blast radius: bytes written, ops performed, calls made | Bounding runaway writes, spawns or HTTP calls | `hop.top/kit/go/core/breaker` |
| netpolicy | Network egress | Honouring `--offline` process-wide | `hop.top/kit/go/core/netpolicy` |
| redact | Text leaving the process | Stripping secrets and PII from logs, telemetry, prompts | `hop.top/kit/go/core/redact` |
| cli/policy | Delegation safety at the command layer | Gating destructive commands invoked by an agent | `hop.top/kit/go/console/cli/policy` |
| runtime/policy | Events on the bus, via declarative rules | Expressing "who may do what" outside Go code | `hop.top/kit/go/runtime/policy` |
| runtime/policy/withcel | Constructs a policy engine with the CEL evaluator wired in | You want CEL rules, and want its deps isolated | `hop.top/kit/go/runtime/policy/withcel` |
| stage | Operating mode: active, feature_freeze, maintenance, sunset, archived | Behaviour that changes with a project's lifecycle stage | `hop.top/kit/go/core/stage` |
| consent | Telemetry consent state machine, `DO_NOT_TRACK` precedence | Asking before collecting anything | `hop.top/kit/go/core/consent` |
| sideeffect | Four interfaces (Exec, FS, HTTP, Bus) as the canonical mutation seam | You want `--dry-run` to work by construction | `hop.top/kit/go/runtime/sideeffect` |
| provenance | Records where each field of an output came from | Proving an answer's origin | `hop.top/kit/go/runtime/provenance` |

Two caveats worth knowing before you rely on these. `netpolicy`
guards `net/http` only — raw `net.Dial`, `database/sql`, gRPC and
raw TLS are unguarded, so `--offline` is advisory for those paths;
loopback is deliberately exempt. `runtime/policy` is
evaluator-agnostic and ships no evaluator: supply one, or use
`withcel` to get the CEL backend wired for you. It allows on no
match, and deny overrides on composition.

`sideeffect` implementations: `.../sideeffect/real` for
production, `.../sideeffect/dryrun` to describe instead of doing,
`.../sideeffect/testfake` to record calls in tests.

`provenance` wrappers record automatically:
`.../provenance/wrap/execwrap` (os/exec),
`.../provenance/wrap/httpwrap` (net/http),
`.../provenance/wrap/sqlwrap` (database/sql).

Deep dives: [`choose-enforcement-mode.md`](../guides/choose-enforcement-mode.md),
[`configure-bus-enforcement.md`](../guides/configure-bus-enforcement.md),
[`telemetry-compliance.md`](telemetry-compliance.md).

## I need to talk to an LLM

| Primitive | What it is | Reach for it when | Import path |
|---|---|---|---|
| llm | Provider-agnostic config resolution and provider registry; `scheme://model` URIs, three-layer merge, fallback | Any LLM call that should not hard-code a vendor | `hop.top/kit/go/ai/llm` |
| llm/errors | Structured error types, `errors.Is`/`As` compatible, with `IsFallbackable` | Deciding whether to retry or fall back | `hop.top/kit/go/ai/llm/errors` |
| llm/router | Native routing engine scoring prompts to pick strong vs weak model | Cutting cost by routing easy prompts down | `hop.top/kit/go/ai/llm/router` |
| llm/routellm | Config types for the RouteLLM router | Configuring RouteLLM from your `llm.yaml` | `hop.top/kit/go/ai/llm/routellm` |

Provider adapters register a URI scheme on import:

| Adapter | Schemes | Import path |
|---|---|---|
| anthropic | `anthropic` | `hop.top/kit/go/ai/llm/anthropic` |
| openai | `openai`, `openrouter`, `xai`, `lmstudio`, `groq`, and more | `hop.top/kit/go/ai/llm/openai` |
| google | `gemini`, `google` | `hop.top/kit/go/ai/llm/google` |
| ollama | `ollama` | `hop.top/kit/go/ai/llm/ollama` |
| triton | `triton` | `hop.top/kit/go/ai/llm/triton` |

## I need my tool to be legible to agents

| Primitive | What it is | Reach for it when | Import path |
|---|---|---|---|
| toolspec | Structured knowledge base describing a CLI: commands, flags, intents, error patterns. Pure data, zero transitive deps | Publishing machine-readable knowledge about your tool | `hop.top/kit/go/ai/toolspec` |
| toolspec/cli | The `<tool> spec` subcommand and manifest builder | Emitting your own manifest *(subcommand)* | `hop.top/kit/go/ai/toolspec/cli` |
| toolspec/adapters | Renders a manifest into per-harness formats | Targeting a specific agent harness | `hop.top/kit/go/ai/toolspec/adapters` |
| toolspec/policy | Maps risk metadata (side-effect × network) onto allow/ask/deny | Deriving harness permissions from your own metadata | `hop.top/kit/go/ai/toolspec/policy` |
| cmdreflect | The one place kit reflects a cobra tree into descriptors, recording why a command is non-invocable | Building tooling over command structure | `hop.top/kit/go/ai/cmdreflect` |
| uxp | Detection and capability mapping for agent CLIs | Discovering which agent CLIs are installed | `hop.top/kit/go/core/uxp` |
| uxp/invoke | Builds native argv for agent CLIs from one normalized `Invocation`; pure, returns diagnostics | Driving Claude Code, Codex, Gemini and friends uniformly | `hop.top/kit/go/core/uxp/invoke` |
| ext | Extensibility contract: extensions declare capabilities and kit routes them | Letting third parties extend your tool | `hop.top/kit/go/ai/ext` |

`uxp/invoke` adapters follow one shape, one package per CLI under
`hop.top/kit/go/core/uxp/invoke/adapters/<name>`, each exporting
`New() Adapter`: `claude`, `codex`, `copilot`, `crush`,
`cursoragent`, `gemini`, `goose`, `kimi`, `opencode`, `qwen`,
`vibe`. Unlike kit's other driver packages these do not
self-register on import — you construct the ones you want and
assemble the set yourself.

`ext` sub-packages cover the capabilities: `ext/registry`
(init-time plugin registry), `ext/hook` (priority-ordered lifecycle
hooks), `ext/discover` (PATH-based binary discovery), `ext/config`
(config-driven toggles), `ext/dispatch` (registers discovered
binaries as cobra subcommands).

toolspec ingest sources parse existing artifacts into a spec:
`sources/help`, `sources/completion`, `sources/tldr`,
`sources/thefuck`, `sources/llm`, `sources/usp`.

Deep dives: [`toolspec-api.md`](toolspec-api.md),
[`toolspec-adopter-guide.md`](../integrations/toolspec-adopter-guide.md),
[`claude-code-permissions.md`](../integrations/claude-code-permissions.md).

## I need identity, upgrades and housekeeping

| Primitive | What it is | Reach for it when | Import path |
|---|---|---|---|
| identity | Local-first Ed25519 identity: keypairs, JWT claims, symmetric encryption | Signing, verifying, or encrypting between tools | `hop.top/kit/go/core/identity` |
| id | TypeID entity IDs — prefixed, sortable, cross-language | Generating entity IDs | `hop.top/kit/go/core/id` |
| upgrade | Self-upgrade: version check, download, replace, plus schema migrations | Shipping a binary that updates itself | `hop.top/kit/go/core/upgrade` |
| util | Stdlib-only helpers: env, fingerprint, humanize, jsonl, must, ptr, retry, since | Small things you would otherwise re-write | `hop.top/kit/go/core/util` |
| telemetry | Opt-in, redact-before-egress usage telemetry with an on-disk spool | Anonymous usage signals, consent-gated | `hop.top/kit/go/runtime/telemetry` |
| compliance | Checks a tool against the 12-factor AI CLI spec, static and runtime | Auditing your own conformance | `hop.top/kit/go/core/compliance` |
| ps | Cross-tool `<tool> ps` process-status convention | Your tool manages long-running work | `hop.top/kit/go/console/ps` |
| avatar | Provider-agnostic avatar URL/data-URI generation from a seed | Rendering identicons for users or projects | `hop.top/kit/go/core/avatar` |
| repohost | Unified driver interface over GitHub, GitLab, Gitea, Gitee, Bitbucket | Reading PRs, issues, commits without per-host glue | `hop.top/kit/go/integrations/repohost` |

`upgrade` schema-migration drivers: `.../upgrade/driver/configfile`,
`.../upgrade/driver/fsdir`, `.../upgrade/driver/sqlite`,
`.../upgrade/driver/tidb`.

`repohost` drivers register on import, one per host under
`hop.top/kit/go/integrations/repohost/<host>` with an `Open()`
entry point; each has a `<host>/mock` sibling for tests.

Deep dives: [`engine-security.md`](engine-security.md),
[`compliance-api.md`](compliance-api.md),
[`telemetry.md`](../guides/telemetry.md),
[`auth.md`](../integrations/repohost/auth.md).

## I need to test my CLI against kit's contract

| Primitive | What it is | Reach for it when | Import path |
|---|---|---|---|
| conformance | Layer-A test helper: `AssertCLI` asserts your root satisfies the kit contract | One-line contract assertion in your test suite | `hop.top/kit/go/conformance` |
| conformance/harness | Integration toolkit asserting contract properties under controlled conditions | Deeper assertions: dry-run purity, exit-code classes, JSON schema | `hop.top/kit/go/conformance/harness` |
| conformance/scenario | The scenario DSL: closed-vocabulary YAML rubric grading a captured run | Authoring a graded conformance scenario | `hop.top/kit/go/conformance/scenario` |
| conformance/story | Story DSL: closed-key YAML schema, parser, three-tier validator | Writing stories that describe expected behaviour | `hop.top/kit/go/conformance/story` |
| conformance/recorder | Turns a scenario plus a binary into an uploadable cassette | Recording a run for grading | `hop.top/kit/go/conformance/recorder` |
| conformance/client | Adopter seam to the grading service: upload a cassette, get a verdict | Grading against the hosted service | `hop.top/kit/go/conformance/client` |
| conformance/badge | Writes the shields.io endpoint JSON and derives the verdict | Publishing a conformance badge | `hop.top/kit/go/conformance/badge` |
| sideeffect/testfake | Recording Exec/FS/HTTP/Bus fakes with `AssertCalled` | Asserting what your command tried to do | `hop.top/kit/go/runtime/sideeffect/testfake` |
| job/mock | In-memory `job.Service` | Testing job producers without a backend | `hop.top/kit/go/runtime/job/mock` |
| secret/memory | In-memory secret store | Tests that need a vault | `hop.top/kit/go/storage/secret/memory` |

Mind the two similarly-named trees. Everything an adopter imports
lives under `hop.top/kit/go/conformance` — `AssertCLI` at its root,
the `harness` toolkit and the `verifynoleak` scanners beneath it.
`hop.top/kit/go/console/cli/conformance` is the **`kit conformance`
subcommand tree**: every package under it returns a cobra command
and none export test helpers. Import the first from your tests;
mount the second if you want the commands.

Deep dive: [`toolspec-harness-guide.md`](../integrations/toolspec-harness-guide.md).

## Ready-made subcommands

These packages return a cobra command tree to mount into your
root, rather than an API to call.

| Subcommand | Gives your users | Import path |
|---|---|---|
| breaker | `breaker list/show/reset` — inspect circuit breakers | `hop.top/kit/go/console/cli/breaker` |
| scope | `scope show/check` — inspect path policy | `hop.top/kit/go/console/cli/scope` |
| stage | `stage` — read and propose operating-mode changes | `hop.top/kit/go/console/stage` |
| config paths | `config path`, `config paths` | `hop.top/kit/go/console/cli/config` |
| uri | Custom URI-scheme registration | `hop.top/kit/go/console/uri` |
| llm router | `llm router start/stop/list/config` | `hop.top/kit/go/console/cli/router` |
| uxp | Build and inspect agent-CLI invocations | `hop.top/kit/go/core/uxp/invoke/cmd/uxp` |
| spec | `<tool> spec` — emit your toolspec manifest | `hop.top/kit/go/ai/toolspec/cli` |
| upgrade | Self-upgrade and migration commands | `hop.top/kit/go/core/upgrade` |

## What is not in this index

kit has 194 non-test packages under `go/`; this index lists the
ones an adopter imports. The line drawn, and what fell outside it:

**Internal decomposition.** Packages that exist to keep another
package's dependencies or file count manageable, and that you reach
through their parent instead: the `verifynoleak/*` scanner
internals (`mdfence`, `rules`, `scanner`, `source`, `suppress`),
the harness internals (`classifier`, `diff`, `predicates`),
`conformance/scenario/verbs`, `conformance/scenariorules`,
`conformance/story/{parser,schema,toolspec,validator}`,
`conformance/svc` (the grading *service*, not the client),
`conformance/scenario/judge` (the judge seam; production
implementations live outside kit), `core/scope/rules` (embedded
rule corpora, auto-generated), `core/breaker/policy`,
`console/cli/idemstore`, `core/uxp/invoke/shim` (a deliberately
closed catalog of adapter mapping helpers), `runtime/policy/cel`
(reach it via `withcel`), and `core/upgrade/skill`.

**Not libraries.** `console/hay/stack`,
`core/uxp/internal/parityreadme` and
`tools/provenancelint/cmd/provcheck` are `package main`.
`tools/provenancelint` is a `go/analysis` analyzer you run as a
linter, not an API you call.

**Test-only, no importable package.** `runtime/policy/e2e`,
`conformance/verifynoleak` and its `citemplates`
directory contain only `_test.go` files, so they do not appear in
`go list` output as importable packages at all.
`core/xdg/scopetest` is documented in its own source as
intentionally empty, existing only to host tests.

**Defined but not yet importable.** `go/security` exports nothing
today, but its scope is fixed: trust in artifacts and execution —
artifact signature verification (cosign, minisign, SLSA) for
`upgrade`, sandboxed exec behind `sideeffect`'s `Exec` seam, a
hash-chained audit log feeding `provenance`, and SARIF
normalization for scanner findings. Keys stay in `identity`,
secrets in `secret`, egress in `netpolicy`, rules in `policy` and
`scope`; repository trust scoring is out of scope. Each family
gets a row in the tables above when it ships.

**Declared placeholder.** `go/integrations` itself exports nothing
and says so in its doc comment; its one child, `repohost`, is
listed under [identity, upgrades and housekeeping](#i-need-identity-upgrades-and-housekeeping).

**Not counted.** Packages under `incubator/` (for example
`incubator/qmochi`, terminal charting) are outside `go/` and
explicitly experimental until promoted.

## Where this disagrees with the architecture page

[`contributors/architecture/architecture.md`](../../contributors/architecture/architecture.md)
is the contributor-facing view and covers 42 of the 194 packages.
Where the two differ, the code wins. Known divergences at the time
of writing:

- It describes "seven role-based domains"; `go/` currently holds
  eleven top-level areas — `ai`, `bridge`, `conformance`,
  `console`, `core`, `integrations`, `runtime`, `security`,
  `storage`, `tools`, `transport`.
- It lists `storage/sqldb` as "PostgreSQL/MySQL via Go stdlib";
  the package doc says SQLite connection management.
- `go/README.md` lists six areas, omitting `bridge`,
  `conformance`, `integrations`, `security` and `tools`. Several
  area READMEs likewise list only a subset of their sub-packages —
  `go/ai/README.md` omits `llm/`, `go/runtime/README.md` omits
  `notify`, `policy`, `provenance` and `telemetry`, and
  `go/transport/README.md` omits `cmdsurface`, `socket`, `mcpsdk`
  and `transportsvc`.

Two package-level docs are also stale against their own code:
`go/integrations` says a repo-host adapter does not exist, though
`integrations/repohost` ships with five drivers; and
`go/conformance/README.md` names the wrong import path for
`AssertCLI`, as noted above.

## Related pages

- [`cli-api-reference.md`](cli-api-reference.md) — the CLI factory in detail
- [`ts-api-reference.md`](ts-api-reference.md) — TypeScript equivalent
- [`py-api-reference.md`](py-api-reference.md) — Python equivalent
- [`storage-abstractions.md`](../concepts/storage-abstractions.md) — picking a storage layer
- [`quickstart.md`](../quickstart.md) — 10 minutes to a working CLI
- [`contributors/architecture/architecture.md`](../../contributors/architecture/architecture.md) — how kit is built
