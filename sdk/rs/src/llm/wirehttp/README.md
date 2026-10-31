# wirehttp

## What it answers

What do the HTTP llm adapters share: one guarded POST with retries and
error mapping, SSE decoding, size limits and the retry timer. Crate
internal except `Limits` and `Sleeper`, re-exported from `llm`. Feature
`llm-http` (pulled in by every HTTP adapter), native only. Provider
wiring (URLs, headers, status mapping) is the adapter's, not this module's.

## Use it when

- you write an adapter → build an `Http` (client, destination, retries, `Sleeper`, `Limits`, scrubber) and call `post(url, headers, body, stream, map)`; `map` turns a non-2xx into the provider's error
- you decode a stream → `sse::SseDecoder`
- you bound memory from a URI or config → `Limits::from_params(provider, &params)`
- you need a URL from an untrusted name → `push_segments`, which encodes each path segment

## Quick start

```rust
use hop_top_kit::llm::Limits;

let l = Limits::default();
assert_eq!(l.max_body_bytes, 32 << 20);
assert_eq!(l.max_event_bytes, 8 << 20);
```

## Contract

- Retries: 408, 409, 429, 5xx and transport failures; `x-should-retry` wins over the status. The wait is
  `retry-after-ms` / `retry-after` when in (0, 60s], else 500ms backoff capped at 8s with full jitter. An offline
  refusal is never retried. No `Sleeper`: no retry.
- `Limits`: `max_body_bytes` 32 MiB, `max_event_bytes` 8 MiB; params of the same names set them, an adapter
  builder's `limits` outranks both; a value that is not a positive byte count fails. Over the limit is
  `ResponseTooLarge`. An error body is read no further than the limit.
- Credentials never reach error text: the scrubber replaces each non-blank secret with `REDACTED`; transport
  errors name method and `scheme://host[:port]`, never userinfo or query. Message text is capped at 512 chars.
- `env_key_trusted`: an environment credential goes only to loopback or the scheme's default, `LLM_BASE_URL` or
  llm.yaml `base_url` origin.
- SSE follows the Go SDK's decoder: lines end at `\n`, blank line dispatches, `data` lines join with `\n`, a body
  ending mid-event drops it.
- 3xx is not followed; `redirect(provider, status)` is the shared refusal.

## Neighbours

- [`anthropic/`](../anthropic/README.md), `openai/`, `google/`, `ollama/`: the adapters on top
- `crate::netpolicy`: `GuardedClient`, the dial and `--offline` gate
- `crate::util::retry`: backoff and jitter

## See also

- [llm core](../README.md)
- [`contracts/llm-v1`](../../../../../contracts/llm-v1/README.md): the shared corpus
