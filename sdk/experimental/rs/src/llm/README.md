# llm

## What it answers

How a Rust tool talks to an LLM provider through one seam: completions,
streaming, tool calls, selected by a `scheme://model` URI, with keys from
the URI, llm.yaml, a secret store or the environment, a fallback chain and
bus events. A port of Go `ai/llm` core; the only adapter is
[`anthropic/`](anthropic/README.md).

## Use it when

- you call a model without hard-coding a vendor → `apply_api_key(store, uri)?` then `resolve(&uri)?`
- you resolve against your own environment or keychain → `Registry::resolve_api_key(&Sources { file, env, store }, uri)`; `KeyResolution::source` says where the key came from
- you merge llm.yaml, env and URI → `Registry::load_config(&sources, uri)` (`load_config(uri)` on the process)
- one provider may fail → `Client::new(p).with_fallback(q)`; `LlmError::is_fallbackable`
- you observe calls → `Client::with_bus(bus)`; topics `kit.ai.request.started` and friends, `Topics::with_prefix`
- you add a provider → implement `Provider`, then `Registry::register(scheme, factory, Declaration)`
- you print a URI or base URL → `redact_uri(uri)`; config types and errors already mask keys
- your tool ids are dotted or namespaced (`pane.open`) → nothing to do; adapters sanitize them and return your names. Already valid and want them checked, never rewritten → `?tool_names=strict`
- you write an adapter for a provider that restricts tool names → `ToolNames::new(rule, mode, tools)?` per request (every entry point, tools or not); send `wire(name)`, map results with `restore_calls`

## Quick start

Verified by `tests/llm_anthropic.rs` and `tests/llm_conformance.rs`:

```rust,ignore
use hop_top_kit::llm::{apply_api_key, resolve, Message, Request, ToolDef};

let uri = apply_api_key(None, "anthropic://claude-opus-5-5")?; // ANTHROPIC_API_KEY
let provider = resolve(&uri)?;
let req = Request { messages: vec![Message::user("Weather in NYC?")], ..Request::default() };
let mut s = provider.stream_with_tools(&req, &[ToolDef::named("get_weather")]).await?;
while let Some(tok) = s.next().await? {
    if tok.done { break }
    print!("{}", tok.content);
}
let calls = &s.outcome().unwrap().tool_calls; // replay with Message::assistant_calls
```

## Contract

- Feature `llm` builds on `config`, `netpolicy`, `url` (reqwest's parser) and `sha2`; wasm32-safe apart from
  `ConfigFile::load` and the process wrappers. `llm-anthropic` adds `api`, `base64` and tokio's timer.
- `TokenStream`: text tokens, one `done` token, then `None`; an error ends it the same way.
  Dropping a future or stream cancels it. `outcome()` (stream, tool calls, usage) is Rust-only.
- Key precedence: URI `api_key`; llm.yaml `api_key`; `api_key_env`; the scheme's names, store
  then env; `LLM_API_KEY` for required keys only. Blank is unset everywhere.
- Base URL: `base_url` param, URI host, `LLM_BASE_URL`, llm.yaml. `Registry::resolve` reads the
  URI alone, as Go's `Resolve`.
- A resolved (non-URI) key goes to a URI-named endpoint only when loopback or same origin as the
  declared default/endpoints, llm.yaml `base_url` or `LLM_BASE_URL`; a URI's own key, any https host.
  No key over plain http to a remote host (`check_key_endpoint`). Else `LlmError::KeyEndpoint`.
- `Request::thinking` (budget below `max_tokens`) streams `Token::thinking`; replay the returned
  `ThinkingBlock`s on `Message::thinking`. `cache_control` on messages and tools; `Usage` counts
  cache tokens, `Usage::cost(&Price)` prices them.
- Tool names ([`tool-names.md`](../../../../../contracts/llm-v1/tool-names.md)): rules
  `ANTHROPIC_TOOL_NAMES`, `OPENAI_TOOL_NAMES`, `GEMINI_TOOL_NAMES`. `ToolNameRule::sanitize`
  collapses each run of disallowed bytes to `_`, prefixes `_` where the rule needs a leading
  letter, and shortens an overlong (or letterless) result to `max_len - 9` bytes + `_` + 8 hex of
  SHA-256(name). Per request, `ToolNames::new` fails the first bad tool with `LlmError::ToolName`
  (`ToolNameReason`: empty, invalid in strict mode, duplicate, collision) before any network call;
  never fallbackable. Replayed history goes out under wire names; returned calls (whole or in a
  stream outcome) carry the original; unknown names pass through. Provider param `tool_names`:
  `sanitize` (default) or `strict`; anything else fails adapter construction.
- Fallbackable: rate limits, transport failures, 5xx (529 included). Offline refusals are not.
- Keys never reach `Debug` or error text. Parity:
  [`contracts/llm-v1`](../../../../../contracts/llm-v1/README.md).
- Not ported: aim catalog routing and aliases, the picker and router, media URL/file sources,
  `On*` hook options (subscribe to the bus), adapters other than Anthropic.

## See also

- [`go/ai/llm`](../../../../../go/ai/llm/README.md), the Go reference
- [`netpolicy/`](../netpolicy/README.md): the guarded client every request goes through
