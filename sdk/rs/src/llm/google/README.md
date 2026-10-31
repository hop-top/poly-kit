# google

## What it answers

How the llm core reaches Gemini: the `generateContent` and
`streamGenerateContent` APIs over HTTP (no SDK crate), as Go
`ai/llm/google` shapes them. Registers `gemini` and `google` in
`default_registry()`. Feature `llm-google`, native only.

## Use it when

- you resolve `gemini://<model>` or `google://<model>` → the key is the URI's `api_key` (`apply_api_key` puts `GOOGLE_API_KEY`, then `GEMINI_API_KEY`, there)
- you build the adapter directly without a key → it falls back to `GOOGLE_API_KEY`, `GEMINI_API_KEY`, `LLM_API_KEY` (`.env(...)` to stand in for the process; `.hermetic()` reads none)
- you loop on tools with a Gemini 3 model → replay the returned `ToolCall`s unchanged: their `provider_data["google"]` carries the thought signature
- you need timeouts or a proxy → `Adapter::builder(cfg).client(GuardedClient::build(policy, reqwest_builder)?)`

## Contract

- Key: sent in `x-goog-api-key`, never in the URL, and scrubbed from every error text. No key is an error at build. The model is required; base URL defaults to `https://generativelanguage.googleapis.com/v1beta`.
- Body: `system` messages join into `systemInstruction`; `assistant` is role `model`; consecutive tool results merge into one user turn; `generationConfig` only when temperature (0.0 is sent), `maxOutputTokens` or stop sequences are set. `tool_choice` maps to `functionCallingConfig` (`AUTO`, `ANY`, `NONE`, a named tool as `ANY` plus `allowedFunctionNames`); on a call without tools, or invalid, the request fails before sending.
- Tool results: a JSON-object content passes through as the response struct, anything else is wrapped as `{"output": text}`; the call must match an earlier assistant call. Tool names follow `GEMINI_TOOL_NAMES` (dots kept).
- Thought signatures: echoed on the `functionCall` part they came with; in the current turn a step's first unsigned call is sent with `skip_thought_signature_validator`; earlier turns go as given. Another namespace's provider data never reaches the wire.
- Responses: finish `STOP` is `stop`, `MAX_TOKENS` `length`, `SAFETY` `content_filter`; a call without an id gets `call_` plus 26 base32 characters. Tool responses also carry `finish_reason` and `usage`, which Go drops.
- Streams: without tools or `stream_events`, text tokens until `STOP`/`MAX_TOKENS` or the end of the body. With tools or `stream_events`, each `functionCall` arrives whole (start, one delta when it has arguments, end), usage is a cumulative snapshot per chunk, the turn ends on a finish reason or a blocked prompt, and a body ending before either is `Truncated`.
- Errors: an error body maps by its own `code`, a bare status by the HTTP status: 401 `Auth`, 403 `PermissionDenied`, 404 model, 429 rate limit, 5xx `HttpStatus`, other `Api`. A stream `error` event maps the same. Not retried; redirects not followed (Go follows them); an event over 1 MiB (`max_event_bytes`) is `ResponseTooLarge`.
- Not ported: Go's aim-catalog key facts.

## See also

- [`go/ai/llm/google`](../../../../../go/ai/llm/google), the Go reference
- [`contracts/llm-v1`](../../../../../contracts/llm-v1/README.md): `gemini_stream_events`
