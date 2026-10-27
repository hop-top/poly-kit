# anthropic

## What it answers

How the llm core reaches Claude: the Messages API over HTTP (no SDK
crate), as Go `ai/llm/anthropic` shapes it. Registers the `anthropic`
scheme in `default_registry()`. Feature `llm-anthropic`, native only.

## Use it when

- you resolve `anthropic://<model>` → nothing to do; the key is the URI's `api_key` (`apply_api_key` puts `ANTHROPIC_API_KEY` there)
- you need timeouts or a proxy → `Adapter::builder(cfg).client(GuardedClient::build(policy, reqwest_builder)?)`
- you run under a non-tokio timer or in tests → `.sleeper(Arc::new(|d| Box::pin(...)))`; `.max_retries(0)` disables retries
- you test without the process environment → `.env(|name| ...)` for the `ANTHROPIC_*` reads

## Contract

- Headers: `x-api-key`, `anthropic-version: 2023-06-01`; `authorization: Bearer` from a non-blank
  `ANTHROPIC_AUTH_TOKEN`; `anthropic-workspace-id` from a non-empty `ANTHROPIC_WORKSPACE_ID`. Those two
  go only to a trusted origin (default, `ANTHROPIC_BASE_URL`, llm.yaml, `LLM_BASE_URL`, loopback).
- Base URL: the config's, else `ANTHROPIC_BASE_URL`, else `https://api.anthropic.com`; an
  unparseable one is rejected at build with credentials masked.
- Body: system messages lift to `system` (the last wins); consecutive tool results merge into one
  user turn; `max_tokens` 0 sends 1024; `temperature: Some(0.0)` is sent.
- A tool without parameters sends `input_schema: {"type":"object"}`; Go omits the field, which the
  API rejects.
- Errors: 401/403 auth, 404 model, 429 rate limit, 5xx (529 included) `HttpStatus`, other `Api`.
  An SSE `error` event maps by its type (`overloaded_error` as 529, `rate_limit_error` as 429);
  Go leaves these unclassified.
- Retries: 2 more attempts on 408, 409, 429, 5xx and transport failures; `x-should-retry` wins;
  wait `retry-after-ms`/`retry-after` when in (0, 60s], else 500ms backoff capped at 8s with full jitter. Offline
  refusals are never retried.
- Streaming folds events as the Go SDK's `Message.Accumulate` does: tool-call JSON deltas
  concatenate, usage totals overwrite, out-of-order blocks are an error.

## See also

- [`go/ai/llm/anthropic`](../../../../../../go/ai/llm/anthropic/README.md), the Go reference
- [`contracts/llm-v1`](../../../../../../contracts/llm-v1/README.md): the shared corpus
