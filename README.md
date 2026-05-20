# kit

A polyglot framework for building agent-friendly CLIs.

Go (primary), TypeScript, Python. Rust and PHP experimental.

- [Why kit](#why-kit)
- [Features](#features)
- [Primitives](#primitives)
- [Install](#install)
- [Getting started](#getting-started)
- [Status](#status)
- [License](#license)

## Why kit

CLIs are agents' preferred UI. Writing code is also their goto
solution — and that burns tokens on work scaffolding, linting, and
convention tooling already do.

```
Use appropriate CLIs    = notable token savings.
Notable token savings   = replace repetitive work by cli.
More custom CLIs        = more code, more deps, messier maintenance.
Messier maintenance     = decreasing reliability and re-usability.
```

kit caps the spiral: a polyglot framework that owns the boring layer
so adopters spend tokens on guardrails, harnesses, evaluations, and
benchmarks — not flag parsers and output renderers.

## Features

- **Cross-language parity.** Same flags, same help layout, same
  output formats across Go, TS, Python. Contract in
  `contracts/parity/parity.json`.
- **Command surface bridge.** One cobra tree projects to 13
  transport surfaces: CLI, REST, RPC, MCP, WS, SSE, Bus, Cron,
  Library, Webhook, OAuth callback, Signed URL, FaaS (AWS Lambda,
  Cloud Run). Destructive commands locked from remote surfaces by
  default. See [`go/transport/cmdsurface/`](go/transport/cmdsurface/).
- **Guardrail primitives.** Path scoping (`go/core/scope`), egress
  filtering (`go/core/redact`), runtime circuit breakers
  (`go/core/breaker`), operating-mode declarations (`go/core/stage`).
- **Consenting telemetry.** Opt-in anonymous usage emission with
  default-deny posture, redact-on-write, and a cross-language wire
  contract (Go / py / ts / rs / php). Adopters wire one
  [`go/runtime/telemetry`](go/runtime/telemetry/) emitter; users
  control consent via
  [`kit telemetry status|enable|disable|reset|inspect`](docs/adopters/guides/telemetry.md).
- **Engine.** Typed-JSON document store with versioning DAG;
  in-memory and SQLite backends.
- **Storage abstractions.** Secret (10 backends), KV (4), blob (2).
- **Conformance harness.** xrr-backed cassette grading, story DSL,
  scenario DSL.
- **Three SDKs at parity.** [`go/`](go/), [`sdk/ts/`](sdk/ts/),
  [`sdk/py/`](sdk/py/).

## Primitives

Shared building blocks every kit-using tool can adopt. Each primitive
has a cross-language reference implementation and an ADR pinning the
specification.

| Primitive | Purpose                                        | Spec                                                              |
| --------- | ---------------------------------------------- | ----------------------------------------------------------------- |
| TypeID    | Self-describing entity IDs (`task_01j6…`).     | [ADR 0001](docs/adr/README.md#0001-typeid-primitive) — Jetify TypeID v0.3.0 |

## Install

### Go

```sh
go get hop.top/kit
```

### TypeScript

```sh
npm install @hop-top/kit
```

### Python

```sh
pip install hop-top-kit
```

## Getting started

The flagship example is at [`examples/spaced/`](examples/spaced/).
It demonstrates a persona keeping context current in a live "space"
that maintains itself via deterministic tools, so any LLM reads
finished context on demand instead of rebuilding it each session.

For more examples, see [`examples/`](examples/).

## Status

Pre-1.0. All components baseline at `0.1.0-alpha.0`. APIs may change
before `1.0.0`; pin minor versions for stability.

## License

See [LICENSE](LICENSE).
