# anthropic

## What it answers

How the llm core reaches Claude: the Messages API over HTTP (no SDK
crate), as Go `ai/llm/anthropic` shapes it. Registers the `anthropic`
scheme in `default_registry()`. Feature `llm-anthropic`, native only.

## Use it when

- you resolve `anthropic://<model>` → nothing to do; the key is the URI's `api_key` (`apply_api_key` puts `ANTHROPIC_API_KEY` there)
- you need timeouts or a proxy → `Adapter::builder(cfg).client(GuardedClient::build(policy, reqwest_builder, Redirects::None)?)`
- you run under a non-tokio timer or in tests → `.sleeper(Arc::new(|d| Box::pin(...)))`; `.max_retries(0)` disables retries
- you test without the process environment → `.env(|name| ...)` for the `ANTHROPIC_*` reads
- your app owns its config → `.hermetic()`: no `ANTHROPIC_*`, `LLM_BASE_URL` or llm.yaml, from the process or `.env`/`.config_file`

## Contract

- Headers: `x-api-key`, `anthropic-version: 2023-06-01`; `authorization: Bearer` from a non-blank
  `ANTHROPIC_AUTH_TOKEN`; `anthropic-workspace-id` from a non-empty `ANTHROPIC_WORKSPACE_ID`. Those two
  go only to a trusted origin (default, `ANTHROPIC_BASE_URL`, llm.yaml, `LLM_BASE_URL`, loopback);
  llm.yaml and `LLM_BASE_URL` are read only when one of them is set. Hermetic: none of these read.
- Base URL: the config's, else `ANTHROPIC_BASE_URL`, else `https://api.anthropic.com`; an
  unparseable one is rejected at build with credentials masked.
- Body: every system message, wherever placed, joins in order with a blank line into `system`
  (empty ones skipped); a `cache_control` on a system message ends a text block there, so system
  messages `a` (cached), `b`, `c` (cached), `d` send `a` | `b\n\nc` | `d`. Consecutive tool
  results merge into one user turn; `max_tokens` 0 sends 1024; `temperature: Some(0.0)` is sent.
- `tool_choice`: `{"type":"auto"|"any"|"none"}` or `{"type":"tool","name":<wire name>}` on tool
  calls; on `complete`/`stream`, or invalid, the request fails before sending.
- A tool without parameters sends `input_schema: {"type":"object","properties":{}}`, as Go does.
- Tool names follow `ANTHROPIC_TOOL_NAMES` (`^[a-zA-Z0-9_-]{1,128}$`): definitions and replayed
  `tool_use` blocks go out sanitized, tool calls come back under the caller's names; collisions,
  duplicates and empty names fail before sending. `?tool_names=strict` sends names as given and
  rejects invalid ones; any other value fails `build`.
- Errors: 401 `Auth`, 403 `PermissionDenied`, 404 model, 429 rate limit, 5xx (529 included) `HttpStatus`, other `Api`.
  An SSE `error` event maps by its type (`overloaded_error` as 529, `rate_limit_error` as 429,
  `permission_error` as 403). A
  stream ending before `message_stop` is `Truncated`, never a success. 3xx: not followed, `HttpStatus`.
- Limits: body 32 MiB, one SSE event 8 MiB (`Limits`, or params `max_body_bytes`/`max_event_bytes`);
  over is `ResponseTooLarge`. Credential values are scrubbed from every error text.
- Retries: 2 more attempts on 408, 409, 429, 5xx and transport failures; `x-should-retry` wins;
  wait `retry-after-ms`/`retry-after` when in (0, 60s], else 500ms backoff capped at 8s with full jitter. Offline
  refusals are never retried.
- Streaming folds events as the Go SDK's `Message.Accumulate` does: tool-call JSON deltas
  concatenate, usage totals overwrite, out-of-order blocks are an error.
- `Request::stream_events`: `tool_use` block start → `ToolCallEvent` start, non-empty
  `input_json_delta` → delta, block stop → end with the complete call; usage on `message_start`
  (input, cache) and `message_delta` (cumulative output). Output usage arrives only near the end.

## See also

- [`go/ai/llm/anthropic`](../../../../../../go/ai/llm/anthropic/README.md), the Go reference
- [`contracts/llm-v1`](../../../../../../contracts/llm-v1/README.md): the shared corpus
