# reference

Exact details per API: signatures, flags, exit codes, config shapes, wire formats. Concepts and how-tos live in the sibling sections.

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`alias-api.md`](alias-api.md) | YAML-backed command aliases, Go and TypeScript | you map short names to longer command paths |
| [`bus-api.md`](bus-api.md) | types, methods and sinks of `go/runtime/bus` | you have read the bus concept and need signatures |
| [`cli-api-reference.md`](cli-api-reference.md) | Go CLI factory, `go/console/cli` | you build a CLI in Go |
| [`completion-api.md`](completion-api.md) | dynamic flag and positional completion for cobra, Commander, Click/Typer | you add value completion to a flag |
| [`compliance-api.md`](compliance-api.md) | static + runtime checker against the 12-factor AI CLI spec | you run or extend `compliance check` |
| [`domain-events.md`](domain-events.md) | `<app>.<entity>.<action>` topic catalog and wildcard rules | you subscribe with `*` or `#` patterns |
| [`engine-protocol.md`](engine-protocol.md) | HTTP/WS wire format shared by Go-native and engine-backed peers | you implement or debug a peer |
| [`engine-security.md`](engine-security.md) | identity, trust mesh and encryption model | you reason about what the engine protects |
| [`engine-sync.md`](engine-sync.md) | peer-to-peer sync between a Go app and an engine | you add a remote and want to know what replicates |
| [`go-primitives.md`](go-primitives.md) | index of every Go primitive kit ships, grouped by task | you would otherwise hand-roll something |
| [`help-rendering.md`](help-rendering.md) | standard help layout across the three languages | you customise or verify help output |
| [`log-api.md`](log-api.md) | themed logger reading `quiet` and `no-color` from config | you log from a kit-built CLI |
| [`php-sdk.md`](php-sdk.md) | PHP SDK long-form surfaces: URI facade, output rules, MCP over PSR-15, offline enforcement, telemetry | you require the experimental package and need the detail behind its README |
| [`py-api-reference.md`](py-api-reference.md) | Python CLI factory, `hop_top_kit.cli` | you build a CLI with Typer |
| [`py-sdk.md`](py-sdk.md) | Python SDK long-form surfaces: MCP mount, URI facade, output rules, telemetry envelope | you have `hop-top-kit` installed and need the detail behind its README |
| [`qmochi-charts.md`](qmochi-charts.md) | every qmochi chart type with options and worked examples, SVG output, automatic selection | you pick a terminal chart type or reach for one of its options |
| [`rs-sdk.md`](rs-sdk.md) | Rust SDK long-form surfaces: serve, output, MCP, storage, httpcache wire contract, telemetry, bus | you depend on the experimental crate and need the detail behind its README |
| [`setflag-textflag-api.md`](setflag-textflag-api.md) | multi-value flags with prefix operators, Go, TS, Python | you replace `--add-X` / `--remove-X` pairs |
| [`telemetry-compliance.md`](telemetry-compliance.md) | F13 `ConsentingTelemetry` checklist for binaries opting into telemetry | your toolspec sets `telemetry.enabled: true` |
| [`toolspec-api.md`](toolspec-api.md) | `go/ai/toolspec` data types and BFS lookup | you read or emit tool manifests in Go |
| [`ts-api-reference.md`](ts-api-reference.md) | TypeScript CLI factory, `@hop-top/kit/cli` | you build a CLI with Commander |
| [`wizard-api.md`](wizard-api.md) | headless sequential wizard engine with pluggable frontends | you build an interactive multi-step flow in Go |
