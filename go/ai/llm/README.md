# llm

## What it answers

How a Go tool talks to any LLM provider through one client: completions,
streaming, tool calling, image generation, speech synthesis,
transcription and video analysis, selected by a `scheme://model` URI
(config from llm.yaml and env, the URI highest), fallback chains and bus
events. Wrong package when you need model metadata itself (`hop.top/aim`)
or a provider-specific wire detail (the adapter sub-packages below).

## Use it when

- you call a model without hard-coding a vendor → `llm.Resolve("anthropic://...")` then `llm.NewClient(provider).Chat(ctx, messages)`
- you stream tokens → `client.Stream(ctx, req)` and call `Next()` until `Done`; set `req.StreamEvents` for tool-call events and usage snapshots mid-stream (anthropic, gemini): [Streaming](../../../docs/adopters/reference/llm.md#streaming)
- you expose tools → `client.CallWithTools(ctx, req, []llm.ToolDef{...})` and read `resp.ToolCalls`; constrain with `req.ToolChoice`
- one provider may fail → `llm.WithFallback(secondary)`, `llm.OnFallback(fn)`
- you pick a model by capability and budget → `llm.PickProvider(ctx, reg, profile, llm.BudgetBalanced)`; restrict with `llm.LoadPool()` + `PickProviderInPool`
- you observe calls → `llm.OnRequest`/`OnResponse` (in process, full content), `OnError`/`OnRoute`/`OnEvaResult`, or `llm.WithBus(bus)` (request/response events are metadata only; opt into content with `llm.WithRequestContent(llm.RedactRequest(r))`): [Bus topics](../../../docs/adopters/reference/llm.md#bus-topics)
- a URI-form model needs its provider key → `llm.ApplyAPIKey(ctx, store, uri)`; `errors.Is(err, llm.ErrMissingKey)` when none is set; `llm.ResolveAPIKey` also says where it came from; a blank key (empty or whitespace, `?api_key=` included) counts as unset; a key goes to a URI-named endpoint only when loopback or a trusted origin, never over plain http to a remote host (`llm.ErrKeyEndpoint`): [Provider keys](../../../docs/adopters/reference/llm.md#provider-keys), [Blank keys](../../../docs/adopters/reference/llm.md#blank-keys), [Where a key may go](../../../docs/adopters/reference/llm.md#where-a-key-may-go)
- you embed kit in an app that owns its config → `llm.Resolve(uri, llm.Hermetic())`: no env var, no llm.yaml, no catalog cache read: [Hermetic mode](../../../docs/adopters/reference/llm.md#hermetic-mode)
- you add a provider → `llm.Register("myscheme", factory, llm.Declaration{...})`: [Custom adapters](../../../docs/adopters/reference/llm.md#custom-adapters)
- you send media from a URL that may be untrusted → `llm.URLSource(u)`: providers that take URLs get the URL, every other fetch is http(s)-only, public-address-only, redirect-free and capped (`llm.MediaMaxBytes`); refusals match `llm.ErrMediaURLRefused`: [Media URLs](../../../docs/adopters/reference/llm.md#media-urls)
- you print or log a model URI or base URL → `llm.RedactURI(uri)`, `llm.RedactURLError(err)`; kit's own errors already mask them: [Credentials in errors](../../../docs/adopters/reference/llm.md#credentials-in-errors)

## Quick start

```go
provider, _ := llm.Resolve("anthropic://claude-sonnet-4-5-20250514")
client := llm.NewClient(provider)

resp, _ := client.Complete(ctx, llm.Request{Messages: []llm.Message{
    {Role: "user", Content: "Hello"},
}})
fmt.Println(resp.Content)
```

## Contract

- Provider URI: `scheme://model[?param=val]`; 14 schemes, aim aliases, catalog
  providers by protocol: [Provider URIs](../../../docs/adopters/reference/llm.md#provider-uris).
- Config merge (`LoadConfig`): the URI outranks all; base URL then `LLM_BASE_URL` > llm.yaml; the key as `ApplyAPIKey` resolves it; an llm.yaml provider block also configures the provider's aliases, `llm.ProviderSettingsFor(uri)` returns it as written: [Provider blocks](../../../docs/adopters/reference/llm.md#provider-blocks-and-aliases). Pool: file < env (`LLM_POOL_DISABLE`) < CLI (`ResolvePool`).
- Bus topics default to `kit.ai.{request.started, response.received, request.errored, fallback.applied, route.selected, eva.evaluated}`; `WithTopicPrefix` rebrands the `source.category` prefix, `WithTopics` overrides individual topics. `request.started` and `response.received` carry counts, sizes, settings and usage, never prompt or reply content, unless `WithRequestContent` / `WithResponseContent` opt in.
- Tool round trip: replay the model's calls as
  `{Role: "assistant", ToolCalls: resp.ToolCalls}`, then one
  `{Role: "tool", ToolCallID: call.ID, Content: result}` per call.
  `ToolCalls` is valid only on `assistant`, `ToolCallID` only on `tool`;
  adapters return an error for linkage their provider cannot express
  instead of sending it as plain text:
  [Tool calling](../../../docs/adopters/reference/llm.md#tool-calling).
- Tool names: any `ToolDef.Name` (`pane.open`) goes out in a form the provider accepts and comes back original in every `ToolCall`; empty, duplicate and colliding names fail with `*errors.ErrToolName` before sending; `?tool_names=strict` checks instead of rewriting: [Tool names](../../../docs/adopters/reference/llm.md#tool-names).
- Replay `resp.ToolCalls` verbatim: `ToolCall.ProviderData` holds
  provider-opaque state keyed by adapter (Gemini thought signatures
  under `google`) that the provider requires back; each adapter reads
  only its own key and ignores the rest.
- Picker: filters on `aim.Filter` plus token bounds, ranks by budget tier,
  tiebreaks alphabetically on `(Provider, ID)`; `ErrNoProviderMatches` +
  `*NoMatchError`; `LLM_PICKER_TRACE` gates one `slog` event per call:
  [Picker](../../../docs/adopters/reference/llm.md#picker).
- Model metadata comes from `hop.top/aim` `v0.1.0-alpha.6` via `llm.Default(ctx)`; inject with `llm.SetDefaultRegistry`.
- License: MIT.

## Neighbours

- [`anthropic/`](anthropic/README.md), [`openai/`](openai/README.md),
  [`google/`](google/README.md), [`ollama/`](ollama/README.md),
  [`triton/`](triton/README.md), [`routellm/`](routellm/README.md):
  provider adapters
- [`router/`](router/README.md): native routing engine (BERT, intent-based)
- [`errors/`](errors/README.md): structured error types with fallback semantics
- `hop.top/kit/go/runtime/bus`: the bus `WithBus` publishes to

## See also

- [LLM client reference](../../../docs/adopters/reference/llm.md):
  install, provider table, streaming, tools, fallback, multimodal, hooks,
  topics, registry, picker, pool, tracing, interfaces
- [Go primitives](../../../docs/adopters/reference/go-primitives.md)

<!-- release: track hop.top/aim v0.1.0-alpha.6 -->
