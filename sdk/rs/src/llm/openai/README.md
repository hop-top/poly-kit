# openai

## What it answers

How the llm core reaches OpenAI and its compatible hosts: the Chat
Completions API over HTTP (no SDK crate), as Go `ai/llm/openai` shapes
it. Registers `openai`, `openrouter`, `xai`, `lmstudio`, `groq`,
`together`, `fireworks`, `deepseek` and `mistral` in
`default_registry()`. Feature `llm-openai`, native only.

## Use it when

- you resolve `openai://<model>` (or any scheme above) → nothing to do; the key is the URI's `api_key` (`apply_api_key` puts `<SCHEME>_API_KEY` there)
- you reach a self-hosted server (vLLM, llama.cpp, TGI, Ollama, LocalAI) → `openai://host:port/model`, sent to `http://host:port/v1`
- you use LM Studio → `lmstudio://model`; no key, `http://localhost:1234/v1` unless the URI names a host or `base_url`
- you need timeouts or a proxy → `Adapter::builder(cfg).client(GuardedClient::build(policy, reqwest_builder)?)`
- you run under a non-tokio timer or in tests → `.sleeper(...)`; `.max_retries(0)` disables retries
- your app owns its config → `.hermetic()`: no `OPENAI_ORG_ID` or `OPENAI_PROJECT_ID`
- you ask where a request goes → `Adapter::endpoint()`, credentials masked

## Contract

- Base URL: the config's, else the scheme's default (`openai`: `https://api.openai.com/v1`; others per `default_base_url`). `lmstudio` always ends in `/v1`; an `openai` host-form URI gets `/v1`; any other `base_url` is the API root as given. An unparseable one is rejected at build with credentials masked. Requests go to `<base>/chat/completions`.
- Headers: `authorization: Bearer <key>`, none for a blank key. For the `openai` scheme and not hermetic, `OpenAI-Organization` from `OPENAI_ORG_ID` and `OpenAI-Project` from `OPENAI_PROJECT_ID`. The adapter reads no other variable.
- Body: `max_tokens` only when above 0; `temperature: Some(0.0)` is sent; `stop` from `stop_sequences`. `tool_choice`: `auto`, `none`, `"required"` for `any`, or the named tool by its wire name; on a call without tools, or invalid, the request fails before sending. A stream with `stream_events` adds `stream_options.include_usage`.
- Messages: an assistant turn with calls sends `tool_calls` (arguments `{}` when empty, else valid JSON) and no `content` when empty; a tool result sends `tool_call_id`. Parts are for user messages only: images as `data:` URIs, PDFs as `file` parts. Audio and video fail with `UnsupportedModality`.
- Tool names follow `OPENAI_TOOL_NAMES`: sanitized on the way out, restored on returned calls; `?tool_names=strict` rejects invalid ones.
- Errors: 401 `Auth`, 403 `PermissionDenied`, 404 model, 429 rate limit, 5xx `HttpStatus` (fallbackable), other `Api`. An error chunk on a 200 stream is `Api` with status 0. 3xx is not followed. Credential values are scrubbed from every error text.
- Streams finish at `data: [DONE]` or, when the body ends without it, once a choice carried a `finish_reason`; any other end is `Truncated`. Tool-call fragments fold by index; a call starts on the fragment completing its id and name.
- Limits: body 32 MiB, one SSE event 8 MiB (`Limits`, or params `max_body_bytes`/`max_event_bytes`); over is `ResponseTooLarge`, never retried.
- Retries: 2 more attempts on 408, 409, 429, 5xx and transport failures; `x-should-retry` wins; wait `retry-after-ms`/`retry-after` when in (0, 60s], else 500ms backoff capped at 8s with full jitter. Offline refusals are never retried.
- Tool responses also carry `finish_reason` and `usage`, which Go drops.
- Not ported: image generation, transcription and speech (the `Provider` seam has no such calls), aim-catalog protocol claims.

## See also

- [`go/ai/llm/openai`](../../../../../../go/ai/llm/openai), the Go reference
- [`contracts/llm-v1`](../../../../../../contracts/llm-v1/README.md): the shared corpus (`openai_*`)
