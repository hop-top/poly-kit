# llm-v1

## What it answers

What the llm core and the Anthropic adapter must do, independent of
language: provider URI parsing and credential masking, where a
credential may go, the Messages API
body for a request, auth headers, text tokens from an SSE body, what an
SSE body accumulates into, and how error responses map and retry. Wrong
place for HTTP client wiring, timers and key stores; those are per-runtime.

## Use it when

- you port or change URI parsing, redaction, the credential guard or the anthropic adapter in any SDK: run every runner
- you change llm behaviour in Go: add a vector first, then make every port pass it

## Quick start

```sh
make test-parity-llm
# or separately:
go test ./go/ai/llm/ ./go/ai/llm/anthropic/ -run '^TestConformance'
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
| `anthropic_accumulate[]` | text, thinking, tool calls, stop reason and usage of an SSE body; Go runs the SDK's `Message.Accumulate` |
| `anthropic_error[]` | error kind, status, fallbackable, and attempts under the default retry policy (server waits honored in (0, 60s] only); the last response repeats |
| `key_endpoint[]` | where a resolved key may go: `apply` (resulting URI), `load`, `resolve` (refused or not); malformed endpoints (userinfo, backslash, percent-escape, whitespace) refused |
| `credential_headers[]` | the key, bearer token and workspace header the anthropic adapter sends for a base URL; env credentials only to trusted origins |

Runners are strict: unknown fields, a `version` other than `v1` and a file no runner consumes fail the run. Servers are loopback only; no vector reaches the network.

## Runners

| Port | Runner |
|------|--------|
| Go | `go/ai/llm/conformance_test.go`, `go/ai/llm/anthropic/conformance_test.go` (`TestConformance*`) |

## See also

- [`go/ai/llm`](../../go/ai/llm/README.md)
