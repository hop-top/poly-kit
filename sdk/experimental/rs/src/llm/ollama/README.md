# ollama

## What it answers

How the llm core reaches a local Ollama server: `/api/chat` over HTTP (no
SDK crate), as Go `ai/llm/ollama` shapes it. Registers the `ollama`
scheme in `default_registry()`. Feature `llm-ollama`, native only.

## Use it when

- you resolve `ollama://<model>` → `http://localhost:11434`; `ollama://host:port/model` names another server and stays at its root
- the server sits behind a gateway that checks a key → `OLLAMA_API_KEY` (optional, sent as `Authorization: Bearer`)
- you need timeouts or a proxy → `Adapter::builder(cfg).client(GuardedClient::build(policy, reqwest_builder)?)`

## Contract

- Calls: complete and stream. The model is required (`ollama: model is required`).
- Body: `model`, `messages`, `stream`; `options.temperature` only when set (0.0 is sent). Like Go, `max_tokens` and stop sequences are not sent.
- Messages: parts concatenate their text into `content` and send images as plain base64 in `images`; audio and video fail with `UnsupportedModality`. An assistant turn's calls go as `tool_calls`; a tool result as `tool_call_id` plus the `tool_name` resolved from an earlier call. A tool choice fails the request before sending (no tools go out).
- Errors: a body naming a missing model is a model error at any status; 401 `Auth`, 403 `PermissionDenied`, 404 model, 429 rate limit, 5xx `HttpStatus`, other `Api`. No retries, as in Go. Redirects are not followed (Go follows them). Credential values are scrubbed from error text; an unparseable base URL is rejected at build with credentials masked.
- Streams are NDJSON: the `done` line is the final token; a body ending without it ends the stream normally, as in Go.
- Not ported: image generation (`/api/generate`).

## See also

- [`go/ai/llm/ollama`](../../../../../../go/ai/llm/ollama), the Go reference
