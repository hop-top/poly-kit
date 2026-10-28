# llm-v1

## What it answers

What the llm core and the Anthropic and OpenAI adapters must do, independent of language: provider URI parsing and credential
masking, what a URL media source refuses to fetch (SSRF guard), the Messages API body for a request, auth headers, text tokens and
events from an SSE body, what it accumulates into, how error responses map and retry, what the client's bus events carry, and the
routellm adapter's base URL. Wrong place for HTTP client wiring, timers and key stores; those are per-runtime.

## Use it when

- you port or change URI parsing, redaction, the anthropic or the openai adapter in any SDK: run every runner
- you change llm behaviour in Go: add a vector first, then make every port pass it

## Quick start

```sh
make test-parity-llm
# or separately:
go test ./go/ai/llm/ ./go/ai/llm/anthropic/ ./go/ai/llm/google/ ./go/ai/llm/openai/ ./go/ai/llm/routellm/ -run '^TestConformance'
cargo test --features llm-anthropic --test llm_conformance   # from sdk/experimental/rs
pnpm vitest run src/llm-anthropic.conformance.test.ts src/llm-toolnames.conformance.test.ts src/llm-media.conformance.test.ts src/llm-openai.conformance.test.ts src/llm-routellm.conformance.test.ts   # from sdk/ts
uv run pytest tests/test_llm_conformance.py tests/test_llm_openai_conformance.py   # from sdk/py
```

## Contract

Go is canonical. The vectors are the source of truth: a runner failure means the implementation drifted, not that the vector should be edited. `vectors.json` carries `version: "v1"` and a `description` of every section.

| Section | Pins |
|---------|------|
| `redact_uri[]` | the masked text of a URI or URL (`RedactURI`) |
| `parse_uri[]` | scheme, model, `host:port`, params, or the exact error |
| `anthropic_request[]` | the request body, compared as JSON (numbers by value); requests may set `thinking_budget`, `cache_control` and thinking blocks to replay; an image or PDF part with a `url` goes out as a `url` source and is never fetched |
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
| `media_fetch[]` | a URL media source's download guard: http(s) only, no userinfo, public addresses only (v4-mapped and NAT64 by their IPv4), no redirects, a byte cap (`max_bytes`, default 32 MiB); `error_kind` `refused` (exact `message` when given), `redirect`, `http_status`, `too_large`; a `url` starting with `/` is served by the runner's loopback server, let past the address check, and `attempts` counts its requests |
| `gemini_stream_events[]` | the gemini adapter's `StreamWithTools` (no tools: `Stream`) over a `streamGenerateContent` SSE body: text; with `stream_events`, each whole `functionCall` as `start`, one `delta` (none without args), `end` back to back, and a cumulative `usage` per chunk with `usageMetadata`; then `done` on a `finishReason` or prompt `blockReason` (`want_outcome`), or `error_kind` (`truncated` before either). Go only |
| `anthropic_tool_names[]` | the anthropic adapter end to end: tool and history `tool_use` names sent, calls returned (`tools`, `stream_tools`) with the original name, errors with no request sent, the `tool_names` param |
| `openai_request[]`, `openai_error[]`, `openai_limits[]` | the Chat Completions body per call (`complete`, `tools`, `stream`, `stream_tools`) as JSON: tools by wire name, `tool_choice` (`any` is `"required"`, a named tool by its sent name), the tool round trip, `stream_options.include_usage` only with `stream_events`; errors as `anthropic_error` (403 `permission_denied`; no error text holds the key, also when a body echoes it; redirects not followed); `max_body_bytes` / `max_event_bytes` as `anthropic_limits` (a stream bounded per event only) |
| `openai_stream[]`, `openai_stream_events[]`, `openai_accumulate[]` | text tokens then done, or `error_kind`: finished at `data: [DONE]` or, without it, once a choice carried a `finish_reason`, else `truncated`; an error chunk is `api` (status 0). With `stream_events`: tool-call `start` (the fragment completing id and name), `delta` (each arguments fragment), `end` (open calls, index order, on the `finish_reason` chunk), `usage` from the final chunk, no end for a call open at an error; not opted in, exactly the tokens. The `StreamWithTools` outcome: text, calls by index with original names (arguments `{}` when none streamed), finish reason, usage |
| `routellm_base_url[]` | the routellm adapter's API root: provider base URL (given as `base_url`) over `ROUTELLM_BASE_URL` over extras `routellm.base_url` over `http://localhost:6060`, `/v1` appended once; hermetic reads no `ROUTELLM_*` |

Runners are strict: unknown fields, a `version` other than `v1` and a file no runner consumes fail the run. Servers are loopback only; no vector reaches the network.

### Known differences

Not vectored. A `retry-after` HTTP date: Go and TypeScript honor one within 60s, Rust and Python back off. Python keeps `LLM_API_KEY` ahead of llm.yaml and `<SCHEME>_API_KEY` (Go: llm.yaml, then the provider variable, then `LLM_API_KEY`) and has no secret store. TypeScript resolves keys in Go's order with no secret store. Both find an llm.yaml block by scheme name only (Go also reads the block of a provider alias). Rust has no URL media sources (media are bytes the caller read): `media_fetch` is not applicable, and `anthropic_request` vectors with a `url` part are skipped by name. `routellm` model field parsing is not vectored: every port parses it alike (router pair or tier name) and sends it over the openai adapter when a RouteLLM base URL is set. `routellm_base_url`: with no base URL, Go and TypeScript use `http://localhost:6060` while Python routes in-process through the `routellm` Controller, which takes no tier name, so Python skips the two vectors expecting the default, by name; Rust has no routellm adapter: not applicable. Only Go has a gemini adapter: `gemini_stream_events` is not applicable in Rust, Python and TypeScript, whose runners list it as such. Rust has no openai adapter: the `openai_*` sections are not applicable there. An openai error chunk with a numeric `code`: Python takes it as the status, Go and TypeScript report status 0 (only uncoded chunks are vectored).

## Tool names

Normative spec: [tool-names.md](tool-names.md). Rules `anthropic` (`[a-zA-Z0-9_-]`, 128), `openai` (same set, 64), `gemini` (adds `.:`, leading letter or `_`, 128).
An invalid name: each disallowed byte run becomes `_`; too long or no letter/digit left: cut and append `_` + 8 hex of SHA-256.
Empty, non-UTF-8, duplicate, colliding (and in `?tool_names=strict`, invalid) names fail before sending; returned calls carry the original name.

## Runners

| Port | Runner |
|------|--------|
| Go | `go/ai/llm/conformance_test.go` (provider-neutral sections, `media_fetch`), `go/ai/llm/anthropic/conformance_test.go` (`TestConformance*`), `go/ai/llm/google/conformance_test.go` (`gemini_*`), `go/ai/llm/openai/conformance_test.go` (`openai_*`), `go/ai/llm/routellm/conformance_test.go` (`routellm_base_url`) |
| Rust | `sdk/experimental/rs/tests/llm_conformance.rs`: every section; the anthropic ones and `bus_*` need feature `llm-anthropic`. `anthropic_hermetic` against an offline client (the request refused at the guard, which reports its destination; headers from the adapter's credential set); `bus_*` through `Client` and a bus. `media_fetch` (no URL media sources), `gemini_stream_events`, the `openai_*` sections and `routellm_base_url` checked present, not applicable |
| Python | `sdk/py/tests/test_llm_conformance.py`: every section, anthropic ones and served `media_fetch` vectors against a loopback server; `bus_*` against the `hop_top_kit.llm_events` payload builders (the Python client publishes no bus events); `openai_*` in `sdk/py/tests/test_llm_openai_conformance.py`, nothing pending; `routellm_base_url` against `routellm_adapter.server_base_url`, the two default-URL vectors skipped with the reason |
| TypeScript | `sdk/ts/src/llm-anthropic.conformance.test.ts` (every `anthropic_*` section, `credential_headers`, `key_endpoint`, `parse_uri`, `redact_uri`, `usage_cost`; `bus_request_started` and `bus_response_received` against the pure payload builders `requestStartedPayload` / `responseReceivedPayload`, as the TS client publishes no bus events) and `sdk/ts/src/llm-toolnames.conformance.test.ts` (`tool_names`) and `sdk/ts/src/llm-media.conformance.test.ts` (`media_fetch`) and `sdk/ts/src/llm-openai.conformance.test.ts` (`openai_*`, nothing pending) and `sdk/ts/src/llm-routellm.conformance.test.ts` (`routellm_base_url`); every other vector runs. `key_endpoint`'s `resolve` op runs `resolve`, which in TS is `loadConfig` plus the adapter |

## See also

- [`go/ai/llm`](../../go/ai/llm/README.md), [`sdk/experimental/rs/src/llm`](../../sdk/experimental/rs/src/llm/README.md), [Python `hop_top_kit.llm`](../../docs/adopters/reference/py-sdk.md#provider-adapters), [TypeScript `@hop-top/kit/llm`](../../docs/adopters/reference/ts-api-reference.md#llm-adapters)
