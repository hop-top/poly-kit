# llm-v1

## What it answers

What the llm core and the Anthropic adapter must do, independent of language: provider URI parsing and
credential masking, the Messages API body for a request, auth headers, text tokens and events from an SSE
body, what it accumulates into, how error responses map and retry, and what the client's bus events carry.
Wrong place for HTTP client wiring, timers and key stores; those are per-runtime.

## Use it when

- you port or change URI parsing, redaction or the anthropic adapter in any SDK: run every runner
- you change llm behaviour in Go: add a vector first, then make every port pass it

## Quick start

```sh
make test-parity-llm
# or separately:
go test ./go/ai/llm/ ./go/ai/llm/anthropic/ -run '^TestConformance'
cargo test --features llm-anthropic --test llm_conformance   # from sdk/experimental/rs
pnpm vitest run src/llm-anthropic.conformance.test.ts src/llm-toolnames.conformance.test.ts   # from sdk/ts
uv run pytest tests/test_llm_conformance.py                  # from sdk/py
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
| `anthropic_stream_events[]` | with `Request.StreamEvents`: text, tool-call `start`/`delta`/`end` (end = `Outcome.ToolCalls[index]`, original name) and cumulative `usage` events in order, then `done` or `error_kind` with no end for an open call; not opted in, exactly `anthropic_stream`'s tokens |
| `anthropic_accumulate[]` | text, thinking, tool calls, stop reason and usage of an SSE body; Go runs the SDK's `Message.Accumulate`, Rust, Python and TypeScript their stream outcome |
| `anthropic_error[]` | error kind (`permission_denied` for a 403 or `permission_error`, which is also `auth`), status, fallbackable, and attempts under the default retry policy (server waits honored in (0, 60s] only); the last response repeats |
| `anthropic_limits[]` | `max_body_bytes` / `max_event_bytes` params: a body or one SSE event over the limit is `too_large` |
| `anthropic_response[]` | a response parsed: text, thinking blocks, tool calls, stop reason, usage with cache tokens |
| `bus_request_started[]` | the `kit.ai.request.started` payload as JSON for a request and call: metadata only (op, model, counts, `content_bytes`, settings, sorted extension keys), no content |
| `bus_response_received[]` | the `kit.ai.response.received` payload as JSON: op, finish reason, usage, `content_bytes`, tool-call count, integer `duration_ms` (not compared), no content |
| `usage_cost[]` | `Usage` priced per million tokens, each kind (input, output, cache read, cache write) at its rate |
| `key_endpoint[]` | where a resolved key may go: `apply` (resulting URI), `load`, `resolve` (refused or not) |
| `tool_names[]` | tool-name checks and the sanitizer per rule and mode: wire names, the first error (`reason`, `name`, `other`), calls mapped back ([tool-names.md](tool-names.md)) |
| `anthropic_tool_choice[]` | the body's `tool_choice` for `Request.ToolChoice` on each call kind (`complete`, `stream`, `tools`, `stream_tools`), a named tool by its sent name, or the exact error with nothing sent |
| `anthropic_hermetic[]` | the adapter built hermetic: with `ANTHROPIC_*`, `LLM_BASE_URL` and `llm.yaml` set, the request goes to the configured (else default) host with only the caller's key |
| `anthropic_tool_names[]` | the anthropic adapter end to end: tool and history `tool_use` names sent, calls returned (`tools`, `stream_tools`) with the original name, errors with no request sent, the `tool_names` param |

Runners are strict: unknown fields, a `version` other than `v1` and a file no runner consumes fail the run. Servers are loopback only; no vector reaches the network.

### Known differences

Not vectored. A `retry-after` HTTP date: Go and TypeScript honor one within 60s, Rust and Python back off.
Python keeps `LLM_API_KEY` ahead of llm.yaml and `<SCHEME>_API_KEY` (Go: llm.yaml, then the provider variable, then `LLM_API_KEY`) and has no secret store.

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
| Rust | `sdk/experimental/rs/tests/llm_conformance.rs` (anthropic sections, including `anthropic_tool_names`, need feature `llm-anthropic`; `anthropic_tool_choice`, `anthropic_hermetic`, `anthropic_stream_events`, `bus_request_started`, `bus_response_received` and the vectors in `PENDING_RUST_PORT` skipped, pending the Rust port) |
| Python | `sdk/py/tests/test_llm_conformance.py`: every section, anthropic ones against a loopback server; `bus_*` against the `hop_top_kit.llm_events` payload builders (the Python client publishes no bus events) |
| TypeScript | `sdk/ts/src/llm-anthropic.conformance.test.ts` (every `anthropic_*` section, `credential_headers`, `redact_uri`, `usage_cost`) and `sdk/ts/src/llm-toolnames.conformance.test.ts` (`tool_names`); not run: `parse_uri`, `key_endpoint` and the llm.yaml `credential_headers` vector (the TS core has no llm.yaml or Resolve-time key guard), each listed with its reason in the runner; `anthropic_tool_choice`, `anthropic_hermetic`, `anthropic_stream_events`, `bus_request_started`, `bus_response_received` and the 403 / system-joining vectors pending the TypeScript port |

## See also

- [`go/ai/llm`](../../go/ai/llm/README.md), [`sdk/experimental/rs/src/llm`](../../sdk/experimental/rs/src/llm/README.md), [Python `hop_top_kit.llm`](../../docs/adopters/reference/py-sdk.md#provider-adapters), [TypeScript `@hop-top/kit/llm`](../../docs/adopters/reference/ts-api-reference.md#llm-adapters)
