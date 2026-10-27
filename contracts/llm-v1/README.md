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
| `anthropic_request[]` | the request body, compared as JSON (numbers by value); requests may set `thinking_budget`, `cache_control` and thinking blocks to replay |
| `anthropic_request_error[]` | requests rejected before sending, exact message, nothing sent |
| `anthropic_headers[]` | key, version, bearer token and workspace headers from the `ANTHROPIC_*` env |
| `anthropic_stream[]` | text tokens of an SSE body, then done, or the typed error (`error_kind`; `truncated` without `message_stop`) |
| `anthropic_accumulate[]` | text, thinking, tool calls, stop reason and usage of an SSE body; Go runs the SDK's `Message.Accumulate`, Rust its stream outcome |
| `anthropic_error[]` | error kind, status, fallbackable, and attempts under the default retry policy (server waits honored in (0, 60s] only); the last response repeats |
| `anthropic_limits[]` | `max_body_bytes` / `max_event_bytes` params: a body or one SSE event over the limit is `too_large` |
| `anthropic_response[]` | a response parsed: text, thinking blocks, tool calls, stop reason, usage with cache tokens |
| `usage_cost[]` | `Usage` priced per million tokens, each kind (input, output, cache read, cache write) at its rate |
| `key_endpoint[]` | where a resolved key may go: `apply` (resulting URI), `load`, `resolve` (refused or not) |
| `tool_names[]` | tool-name checks and the sanitizer per rule and mode: wire names, the first error (`reason`, `name`, `other`), calls mapped back ([tool-names.md](tool-names.md)) |
| `anthropic_tool_names[]` | the anthropic adapter end to end: tool and history `tool_use` names sent, calls returned (`tools`, `stream_tools`) with the original name, errors with no request sent, the `tool_names` param |

Runners are strict: unknown fields, a `version` other than `v1` and a file no runner consumes fail the run. Servers are loopback only; no vector reaches the network.

### Known differences

Not vectored. A `retry-after` HTTP date: Go honors one within 60s, Rust backs off.

## Tool names

Normative spec: [tool-names.md](tool-names.md). Rules `anthropic`
(`[a-zA-Z0-9_-]`, 128), `openai` (same set, 64), `gemini` (adds `.:`,
leading letter or `_`, 128). An invalid name: each disallowed byte run
becomes `_`; too long or no letter/digit left: cut and append `_` + 8
hex of SHA-256. Empty, non-UTF-8, duplicate, colliding (and in
`?tool_names=strict`, invalid) names fail before sending; returned
calls carry the original name.

## Runners

| Port | Runner |
|------|--------|
| Go | `go/ai/llm/conformance_test.go`, `go/ai/llm/anthropic/conformance_test.go` (`TestConformance*`) |
| Rust | `sdk/experimental/rs/tests/llm_conformance.rs` (anthropic sections, including `anthropic_tool_names`, need feature `llm-anthropic`) |
| Python | `sdk/py/tests/test_llm_conformance.py`: `tool_names`; every other section skipped with its reason (no anthropic adapter in Python) |
| TypeScript | `sdk/ts/src/llm-toolnames.conformance.test.ts`: `tool_names` only (no anthropic adapter in TypeScript) |

## See also

- [`go/ai/llm`](../../go/ai/llm/README.md), [`sdk/experimental/rs/src/llm`](../../sdk/experimental/rs/src/llm/README.md)
