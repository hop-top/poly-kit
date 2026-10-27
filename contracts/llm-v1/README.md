# llm-v1

## What it answers

What the llm core and the Anthropic adapter must do, independent of
language: provider URI parsing and credential masking, the Messages API
body for a request, auth headers, text tokens from an SSE body, what an
SSE body accumulates into, and how error responses map and retry. Wrong
place for HTTP client wiring, timers and key stores; those are per-runtime.

## Use it when

- you port or change URI parsing, redaction or the anthropic adapter in any SDK: run both runners
- you change llm behaviour in Go: add a vector first, then make every port pass it

## Quick start

```sh
make test-parity-llm
# or separately:
go test ./go/ai/llm/ ./go/ai/llm/anthropic/ -run '^TestConformance'
cargo test --features llm-anthropic --test llm_conformance   # from sdk/experimental/rs
```

## Contract

Go is canonical. The vectors are the source of truth: a runner failure means the implementation drifted, not that the vector should be edited. `vectors.json` carries `version: "v1"` and a `description` of every section.

| Section | Pins |
|---------|------|
| `redact_uri[]` | the masked text of a URI or URL (`RedactURI`) |
| `parse_uri[]` | scheme, model, `host:port`, params, or the exact error |
| `anthropic_request[]` | the request body, compared as JSON (numbers by value) |
| `anthropic_request_error[]` | requests rejected before sending, exact message, nothing sent |
| `anthropic_headers[]` | key, version, bearer token and workspace headers from the `ANTHROPIC_*` env |
| `anthropic_stream[]` | text tokens of an SSE body, then done, or an error |
| `anthropic_accumulate[]` | text, thinking, tool calls, stop reason and usage of an SSE body; Go runs the SDK's `Message.Accumulate`, Rust its stream outcome |
| `anthropic_error[]` | error kind, status, fallbackable, and attempts under the default retry policy; the last response repeats |

Runners are strict: unknown fields, a `version` other than `v1` and a file no runner consumes fail the run. Servers are loopback only; no vector reaches the network.

### Known differences

Not vectored. A tool without parameters: Go sends no `input_schema` (the API rejects it), Rust sends `{"type":"object"}`. An SSE `error` event: Go returns it unclassified, Rust maps it by error type (`overloaded_error` as a fallbackable 529). Rust's text stream also fails on malformed block indexes, which Go's ignores.

## Runners

| Port | Runner |
|------|--------|
| Go | `go/ai/llm/conformance_test.go`, `go/ai/llm/anthropic/conformance_test.go` (`TestConformance*`) |
| Rust | `sdk/experimental/rs/tests/llm_conformance.rs` (anthropic sections need feature `llm-anthropic`) |

## See also

- [`go/ai/llm`](../../go/ai/llm/README.md), [`sdk/experimental/rs/src/llm`](../../sdk/experimental/rs/src/llm/README.md)
