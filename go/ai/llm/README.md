# llm

Provider-agnostic LLM client for Go.

Unified interface for completions, streaming, tool calling, image
generation, speech synthesis, transcription, and video analysis
across providers. Three-layer config merge: file < URI < env vars.

## Install

```
go get hop.top/llm
```

## Library

### Provider URIs

```
scheme://model[?param=val]
```

| Scheme | Provider | Capabilities |
|--------|----------|-------------|
| `anthropic` | Anthropic | Complete, Stream, ToolCall |
| `openai` | OpenAI | Complete, Stream, ToolCall, Image, Speech, Transcribe |
| `openrouter` | OpenRouter | Complete, Stream, ToolCall |
| `gemini`, `google` | Google Gemini | Complete, Stream, ToolCall |
| `ollama` | Ollama | Complete, Stream, Image |
| `xai` | xAI | Complete, Stream, ToolCall |
| `groq` | Groq | Complete, Stream, ToolCall |
| `together` | Together | Complete, Stream, ToolCall |
| `fireworks` | Fireworks | Complete, Stream, ToolCall |
| `deepseek` | DeepSeek | Complete, Stream, ToolCall |
| `mistral` | Mistral | Complete, Stream, ToolCall |
| `lmstudio` | LM Studio | Complete, Stream, ToolCall |
| `routellm` | RouteLLM | Complete, Stream (routed) |
| `triton` | NVIDIA Triton | Score (inference) |

### Quick Start

```go
provider, _ := llm.Resolve("anthropic://claude-sonnet-4-5-20250514")
client := llm.NewClient(provider)

resp, _ := client.Chat(ctx, []llm.Message{
    {Role: "user", Content: "Hello"},
})
fmt.Println(resp.Message.Content)
```

### Streaming

```go
iter, _ := client.StreamChat(ctx, messages)
for iter.Next() {
    tok := iter.Token()
    fmt.Print(tok.Text)
}
```

### Tool Calling

```go
resp, _ := client.ChatWith(ctx, messages, []llm.ToolDef{
    {Name: "weather", Description: "Get weather", InputSchema: schema},
})
for _, tc := range resp.ToolCalls {
    fmt.Println(tc.Name, string(tc.Arguments))
}
```

### Fallback Chains

```go
client := llm.NewClient(primary,
    llm.WithFallback(secondary),
    llm.WithFallback(tertiary),
    llm.OnFallback(func(from, to int, err error) {
        log.Printf("fallback %d→%d: %v", from, to, err)
    }),
)
```

### Multimodal

```go
// Image generation
img, _ := client.GenerateImage(ctx, llm.ImageRequest{
    Prompt: "a sunset over mountains",
})

// Speech synthesis
audio, _ := client.Synthesize(ctx, llm.SynthesizeRequest{
    Text: "Hello world", Voice: "alloy",
})

// Transcription
transcript, _ := client.Transcribe(ctx, llm.TranscribeRequest{
    Source: llm.FileSource("recording.mp3"),
})

// Media sources
llm.FileSource("path/to/file")
llm.URLSource("https://example.com/image.png")
llm.InlineSource(data, "image/png")
```

### Event Hooks

```go
llm.NewClient(provider,
    llm.OnRequest(func(r llm.Request) { /* ... */ }),
    llm.OnResponse(func(r llm.Response, d time.Duration) { /* ... */ }),
    llm.OnError(func(err error) { /* ... */ }),
    llm.OnRoute(func(router string, score float64, model string) { /* ... */ }),
    llm.OnEvaResult(func(contract string, passed bool, violations []string) { /* ... */ }),
    llm.WithBus(eventBus),
)
```

### Bus topics

`Client` publishes 6 topics by default — non-uniform action
vocabulary on purpose, each event names the real verb:

| Topic                          | When |
|--------------------------------|------|
| `kit.ai.request.started`       | request initiated |
| `kit.ai.response.received`     | response complete |
| `kit.ai.request.errored`       | request failed |
| `kit.ai.fallback.applied`      | fallback chain advanced |
| `kit.ai.route.selected`        | router picked a model |
| `kit.ai.eva.evaluated`         | contract evaluation result |

Override the 2-segment `source.category` prefix to rebrand all
six at once (the trailing `object.action` pair is preserved):

```go
llm.NewClient(provider,
    llm.WithTopicPrefix("myapp.ai"),
)
// myapp.ai.request.started, myapp.ai.response.received, ...
```

Use `llm.WithTopics(llm.Topics{ ... })` to override individual
topics; empty fields fall back to `DefaultTopics`.

### Configuration

```go
cfg, _ := llm.LoadConfig("anthropic://claude-sonnet-4-5-20250514?temperature=0.7")
```

Three-layer merge: config file < URI params < env vars.

### Model registry

`aim` (`hop.top/aim`) is the source of truth for model metadata — capabilities,
modalities, cost, context windows. The picker (forthcoming) consumes this
accessor; library code calls `llm.Default(ctx)` rather than constructing a
registry directly so tests and embedders can inject custom sources via
`llm.SetDefaultRegistry`. The lazy default reuses one `aim.NewRegistry` across
calls; swapping the provider invalidates that cache.

```go
t := true
reg, err := llm.Default(ctx)
models, _ := reg.Models(ctx, aim.Filter{ToolCall: &t})
```

### Request profile and budget tier

`RequestProfile` is the consumer-facing input to the forthcoming `PickProvider`:
an `aim.Filter` plus `MaxInputTokens` / `MaxOutputTokens` bounds (the picker
rejects models whose context window or output limit is smaller). `BudgetTier`
(`cheap` / `balanced` / `premium`) captures the cost/capability trade-off as a
stable categorical so the surface survives upstream pricing churn; use
`ParseBudgetTier` for case-insensitive CLI input. Layering rule: consumers
derive the profile from invocation context, kit picks the provider.

```go
t := true
prof := llm.RequestProfile{
    Filter:         aim.Filter{ToolCall: &t, StructuredOutput: &t},
    MaxInputTokens: 8192,
}
```

### Picker

`PickProvider` selects a single `*aim.Model` for a profile and budget:

```go
func PickProvider(ctx context.Context, reg *aim.Registry, profile RequestProfile, budget BudgetTier) (*aim.Model, error)
```

- Filter: queries `reg.Models(ctx, profile.Filter)`, then drops candidates
  whose known `Limit.Context` / `Limit.Output` falls below the profile's
  bounds. Unknown limits (zero) pass through.
- Rank: `BudgetCheap` minimises token-weighted price
  (`0.75*Cost.Input + 0.25*Cost.Output`); `BudgetPremium` maximises
  `Limit.Context` and tiebreaks on `Cost.Input`; `BudgetBalanced` picks the
  median of the price-sorted survivors. Nil-cost models are price 0 (Cheap
  prefers, Premium loses tiebreaks).
- Tiebreak: alphabetical `(Provider, ID)` makes every call deterministic.

```go
reg, _ := llm.Default(ctx)
m, err := llm.PickProvider(ctx, reg, prof, llm.BudgetBalanced)
```

Errors are sentinel + structured: `errors.Is(err, llm.ErrNoProviderMatches)`
detects the no-match case; `var nme *llm.NoMatchError; errors.As(err, &nme)`
extracts `CandidateCount` and per-model `Eliminated` reasons for logs.

### Custom Adapters

```go
llm.Register("myscheme", func(cfg llm.ResolvedConfig) (llm.Provider, error) {
    return &MyAdapter{model: cfg.Model}, nil
})
```

## Interfaces

| Interface | Methods |
|-----------|---------|
| `Provider` | Base provider |
| `Completer` | `Complete(ctx, []Message) (Response, error)` |
| `Streamer` | `Stream(ctx, []Message) (TokenIterator, error)` |
| `ToolCaller` | `CompleteWithTools(ctx, []Message, []ToolDef) (Response, error)` |
| `ImageGenerator` | `GenerateImage(ctx, ImageRequest) (ImageResponse, error)` |
| `SpeechSynthesizer` | `Synthesize(ctx, SynthesizeRequest) (SynthesizeResponse, error)` |
| `Transcriber` | `Transcribe(ctx, TranscribeRequest) (TranscribeResponse, error)` |
| `VideoAnalyzer` | `AnalyzeVideo(ctx, ...)` |
| `VideoGenerator` | `GenerateVideo(ctx, VideoGenRequest)` |

## Sub-packages

| Package | Description |
|---------|-------------|
| [anthropic/](anthropic/) | Anthropic Messages API adapter |
| [openai/](openai/) | OpenAI-compatible adapter (+ OpenRouter, xAI, Groq, etc.) |
| [google/](google/) | Google Gemini REST adapter |
| [ollama/](ollama/) | Ollama local inference adapter |
| [triton/](triton/) | NVIDIA Triton Inference Server scorer |
| [routellm/](routellm/) | RouteLLM cost-aware routing adapter |
| [router/](router/) | Native routing engine (BERT, intent-based) |
| [errors/](errors/) | Structured error types with fallback semantics |

## License

MIT
