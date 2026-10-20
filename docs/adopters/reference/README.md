# reference

Exact details per API: signatures, flags, exit codes, config shapes, wire formats. Concepts and how-tos live in the sibling sections.

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`alias-api.md`](alias-api.md) | YAML-backed command aliases, Go and TypeScript | you map short names to longer command paths |
| [`breaker.md`](breaker.md) | runtime circuit breakers: policy table, wrap helpers, bus topics, `breaker.yaml`, `kit breaker` | you bound how much, how fast or how often an operation may run |
| [`bus-api.md`](bus-api.md) | types, methods and sinks of `go/runtime/bus` | you have read the bus concept and need signatures |
| [`cli-api-reference.md`](cli-api-reference.md) | Go CLI factory, `go/console/cli` | you build a CLI in Go |
| [`cmdreflect.md`](cmdreflect.md) | `go/ai/cmdreflect`: one cobra tree reflected once into a `Tree` of descriptors every consumer projects from | you derive a spec, manifest or transport surface from a cobra tree |
| [`cmdsurface.md`](cmdsurface.md) | `go/transport/cmdsurface`: one command tree on several surfaces, every `Mount*` function, sinks, safety matrix, policy and threat model | you project a command tree onto REST, MCP, RPC, a socket or a function runtime |
| [`completion-api.md`](completion-api.md) | dynamic flag and positional completion for cobra, Commander, Click/Typer | you add value completion to a flag |
| [`compliance-api.md`](compliance-api.md) | static + runtime checker against the 12-factor AI CLI spec | you run or extend `compliance check` |
| [`config.md`](config.md) | `go/core/config`: layered loading, `-c`/`--config`, typed writes, `ParseScalar` coercion, hot reload | you load a typed config struct or write values back from `config set` |
| [`conformance.md`](conformance.md) | `go/conformance` and its sub-packages: static checker, grading client, xrr-backed harness, scenario and story DSLs | you wire the conformance suite into a test file or a CI matrix |
| [`domain-events.md`](domain-events.md) | `<app>.<entity>.<action>` topic catalog and wildcard rules | you subscribe with `*` or `#` patterns |
| [`engine-protocol.md`](engine-protocol.md) | HTTP/WS wire format shared by Go-native and engine-backed peers | you implement or debug a peer |
| [`engine-security.md`](engine-security.md) | identity, trust mesh and encryption model | you reason about what the engine protects |
| [`engine-sync.md`](engine-sync.md) | peer-to-peer sync between a Go app and an engine | you add a remote and want to know what replicates |
| [`flag-enums.md`](flag-enums.md) | flag value enums, parse-time flag errors and suggestions, `go/console/cli` | a mistyped or value-less flag should come back with a fix |
| [`go-primitives.md`](go-primitives.md) | index of every Go primitive kit ships, grouped by task | you would otherwise hand-roll something |
| [`hay.md`](hay.md) | `go/console/hay`: fuzzy resolution of user input against a corpus, with staged lookup and ambiguity handling | you resolve a typed name against a known set of candidates |
| [`help-rendering.md`](help-rendering.md) | standard help layout across the three languages | you customise or verify help output |
| [`llm.md`](llm.md) | `go/ai/llm`: provider-agnostic LLM client for completions, streaming, tool calling, image, speech, transcription and video | you call a model from Go and want one interface across providers |
| [`log-api.md`](log-api.md) | themed logger reading `quiet` and `no-color` from config | you log from a kit-built CLI |
| [`mcp-tasks.md`](mcp-tasks.md) | `extensions/mcp-tasks`: the MCP tasks extension for servers on the official Go SDK, with durable pollable tool calls | you run long tool calls that clients poll, cancel or feed input |
| [`mcpsdk.md`](mcpsdk.md) | `go/transport/mcpsdk`: serve a `cmdsurface` bridge over MCP on the official Go SDK, with version negotiation and the tasks binding | you serve MCP through the SDK instead of the hand-rolled surface |
| [`notify-sinks.md`](notify-sinks.md) | the three outbound notify sinks: constructors, options, templates and the egress pipeline | you send notifications out of the process and need a sink |
| [`output.md`](output.md) | `go/console/output`: the `--format` renderer (`table`, `json`, `yaml`, `csv`, `text`) and the cross-runtime column contract | you render command results or port the renderer to another runtime |
| [`parity-contract.md`](parity-contract.md) | what lives in `contracts/parity/`, which file holds which cross-language contract, how each port reads and tests it | you change a shared contract or check a port against it |
| [`php-sdk.md`](php-sdk.md) | PHP SDK long-form surfaces: URI facade, output rules, MCP over PSR-15, offline enforcement, telemetry | you require the experimental package and need the detail behind its README |
| [`provenance.md`](provenance.md) | `go/runtime/provenance`: wire-level provenance envelope, strict modes, lint and source wrappers | your structured output must say where each number came from |
| [`ps.md`](ps.md) | `go/console/ps`: the `<tool> ps` subcommand and the primitives to spawn, watch and stop child processes | you report on or supervise child processes |
| [`py-api-reference.md`](py-api-reference.md) | Python CLI factory, `hop_top_kit.cli` | you build a CLI with Typer |
| [`py-sdk.md`](py-sdk.md) | Python SDK long-form surfaces: MCP mount, URI facade, output rules, offline enforcement, telemetry envelope | you have `hop-top-kit` installed and need the detail behind its README |
| [`qmochi-charts.md`](qmochi-charts.md) | every qmochi chart type with options and worked examples, SVG output, automatic selection | you pick a terminal chart type or reach for one of its options |
| [`redact.md`](redact.md) | `go/core/redact`: egress filter for secrets, tokens and PII, with strategies, allowlists and rule sources | you strip sensitive data before text leaves the process |
| [`rs-sdk.md`](rs-sdk.md) | Rust SDK long-form surfaces: serve, output, MCP, storage, httpcache wire contract, offline enforcement, telemetry, bus | you depend on the experimental crate and need the detail behind its README |
| [`scope.md`](scope.md) | `go/core/scope`: path policy guardrails, modes, decision algorithm, pattern syntax, `scope.yaml`, `kit scope` | you bound which paths a tool or agent may read, write or execute |
| [`served-middleware.md`](served-middleware.md) | every `services.<svc>` middleware key, its default and reach, the order, and each refusal per surface | you configure auth or limits on a served tool, or map a refusal |
| [`served-observability.md`](served-observability.md) | tracing and metrics for served commands: propagation, keys, spans, instruments | you trace or measure the api, socket or RPC services |
| [`setflag-textflag-api.md`](setflag-textflag-api.md) | multi-value flags with prefix operators, Go, TS, Python | you replace `--add-X` / `--remove-X` pairs |
| [`sideeffect.md`](sideeffect.md) | `go/runtime/sideeffect`: the dry-run and test seam, its implementations, the `--dry-run` flag and its policy table | your commands write, call out or spawn, and need `--dry-run` and unit tests |
| [`socket-wire.md`](socket-wire.md) | `go/transport/socket`: the newline-delimited JSON wire, error codes, auth, cancellation, config keys and Go API | you write a client for the Unix socket service or debug its wire |
| [`stage.md`](stage.md) | `go/core/stage`: a scope's operating mode, `State` shape, event topics and `projects.yaml` v2 | you declare or enforce a scope's operating mode |
| [`telemetry-compliance.md`](telemetry-compliance.md) | F13 `ConsentingTelemetry` checklist for binaries opting into telemetry | your toolspec sets `telemetry.enabled: true` |
| [`telemetry.md`](telemetry.md) | `go/runtime/telemetry`: opt-in, redact-before-egress telemetry, mode contract, HTTPS sink, spool, wire format | you add telemetry to a tool or audit what it sends |
| [`toolspec-api.md`](toolspec-api.md) | `go/ai/toolspec` data types and BFS lookup | you read or emit tool manifests in Go |
| [`transport-api.md`](transport-api.md) | `go/transport/api`: the REST projection, bus topics, auth claims, guards, OpenAPI document, compression and read cache | you need the wire details of the REST service |
| [`transportsvc.md`](transportsvc.md) | `go/transport/transportsvc`: the `Transport` and `Invoker` contracts and the `TransportService` that gives a transport the serve lifecycle | you register your own transport as a service |
| [`ts-api-reference.md`](ts-api-reference.md) | TypeScript CLI factory, `@hop-top/kit/cli` | you build a CLI with Commander |
| [`uxp.md`](uxp.md) | `go/core/uxp`: agent-CLI identity, discovery and invocation, parity matrices, shim catalog and `Config` keys | you drive several agent CLIs from one normalized request |
| [`wizard-api.md`](wizard-api.md) | headless sequential wizard engine with pluggable frontends | you build an interactive multi-step flow in Go |
| [`xdg.md`](xdg.md) | `go/core/xdg`: per-user config, data, cache, state and runtime directories, plus user document folders | you resolve where a tool keeps its files |
