# llm

## What it answers

How a Go tool talks to any LLM provider through one client: completions,
streaming, tool calling, image generation, speech synthesis,
transcription and video analysis, selected by a `scheme://model` URI with
a file < URI < env config merge, fallback chains and bus events. Wrong
package when you need model metadata itself (`hop.top/aim`) or a
provider-specific wire detail (the adapter sub-packages below).

## Use it when

- you call a model without hard-coding a vendor → `llm.Resolve("anthropic://...")` then `llm.NewClient(provider).Chat(ctx, messages)`
- you stream tokens → `client.StreamChat(ctx, messages)` and iterate `iter.Next()` / `iter.Token()`
- you expose tools → `client.ChatWith(ctx, messages, []llm.ToolDef{...})` and read `resp.ToolCalls`
- one provider may fail → `llm.WithFallback(secondary)`, `llm.OnFallback(fn)`
- you pick a model by capability and budget → `llm.PickProvider(ctx, reg, profile, llm.BudgetBalanced)`; restrict with `llm.LoadPool()` + `PickProviderInPool`
- you observe calls → `llm.OnRequest`/`OnResponse`/`OnError`/`OnRoute`/`OnEvaResult`, or `llm.WithBus(bus)`
- you add a provider → `llm.Register("myscheme", func(cfg llm.ResolvedConfig) (llm.Provider, error) {...})`

## Quick start

```go
provider, _ := llm.Resolve("anthropic://claude-sonnet-4-5-20250514")
client := llm.NewClient(provider)

resp, _ := client.Chat(ctx, []llm.Message{
    {Role: "user", Content: "Hello"},
})
fmt.Println(resp.Message.Content)
```

## Contract

- Provider URI: `scheme://model[?param=val]`; 14 schemes, capabilities per
  scheme: [Provider URIs](../../../docs/adopters/reference/llm.md#provider-uris).
- Config merge: file < URI params < env vars. Pool: file < env
  (`LLM_POOL_DISABLE`) < CLI (`ResolvePool`).
- Bus topics default to `kit.ai.{request.started, response.received,
  request.errored, fallback.applied, route.selected, eva.evaluated}`;
  `WithTopicPrefix` rebrands the `source.category` prefix,
  `WithTopics` overrides individual topics.
- Picker: filters on `aim.Filter` plus token bounds, ranks by budget tier,
  tiebreaks alphabetically on `(Provider, ID)`; `ErrNoProviderMatches` +
  `*NoMatchError`; `LLM_PICKER_TRACE` gates one `slog` event per call:
  [Picker](../../../docs/adopters/reference/llm.md#picker).
- Model metadata comes from `hop.top/aim` `v0.1.0-alpha.0` via
  `llm.Default(ctx)`; inject with `llm.SetDefaultRegistry`.
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

<!-- release: track hop.top/aim v0.1.0-alpha.0 -->
