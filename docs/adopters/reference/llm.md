# LLM Client Reference

> Reference for `hop.top/kit/go/ai/llm`: provider-agnostic LLM client
> for Go. Unified interface for completions, streaming, tool calling,
> image generation, speech synthesis, transcription, and video analysis
> across providers. Three-layer config merge: file < URI < env vars.
> License: MIT.

## Install

```sh
go get hop.top/llm
```

## Provider URIs

```text
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

### Local servers

Local schemes default to the server's documented address, so a bare
`scheme://model` reaches a server running with stock settings:

| Scheme | Default base URL |
|--------|------------------|
| `lmstudio` | `http://localhost:1234/v1` |
| `ollama` | `http://localhost:11434` |

Point at another host or port with `?base_url=`:

```text
lmstudio://qwen2.5-7b-instruct?base_url=http://gpu-box:1234/v1
ollama://llama3.2:3b?base_url=http://gpu-box:11434
```

Each scheme resolves once its adapter package is imported, blank
imports included (`_ "hop.top/kit/go/ai/llm/ollama"`).

### Aliases and catalog providers

A scheme also resolves under the aim catalog's name for the same
provider, to the same adapter, base URL and key:

| Alias | Scheme |
|-------|--------|
| `fireworks-ai` | `fireworks` |
| `togetherai` | `together` |

A provider in the aim catalog that no adapter registers by name
resolves through the adapter speaking its protocol. The `openai`
adapter speaks `openai-compatible` (most catalog providers), `openai`,
`openrouter`, `groq`, `xai`, `togetherai` and `mistral`:

```text
digitalocean://llama3.3-70b-instruct
```

The base URL is the catalog's. A `${VAR}` placeholder in it is
expanded from that environment variable, and only when the catalog
lists the variable as one of the provider's settings, not a key
(`DATABRICKS_HOST` for `databricks`). An unset variable, a placeholder
naming anything else, or a provider with no catalog base URL is an
error naming what to set; pass `?base_url=` instead. No request is
made to a guessed host.

Kit reads the catalog only from aim's on-disk cache (`llm.Default`'s
registry, `Cache().Load()`); `Resolve` never fetches it. With no cache
(offline, first run) catalog providers do not resolve; registered
schemes and their aliases are unaffected.

## Quick start

```go
provider, _ := llm.Resolve("anthropic://claude-sonnet-4-5-20250514")
client := llm.NewClient(provider)

resp, _ := client.Chat(ctx, []llm.Message{
    {Role: "user", Content: "Hello"},
})
fmt.Println(resp.Message.Content)
```

## Streaming

```go
iter, _ := client.StreamChat(ctx, messages)
for iter.Next() {
    tok := iter.Token()
    fmt.Print(tok.Text)
}
```

## Tool calling

```go
resp, _ := client.ChatWith(ctx, messages, []llm.ToolDef{
    {Name: "weather", Description: "Get weather", InputSchema: schema},
})
for _, tc := range resp.ToolCalls {
    fmt.Println(tc.Name, string(tc.Arguments))
}
```

### Returning tool results

The next request replays the model's calls, then one result per call,
linked by ID:

```go
msgs = append(msgs, llm.Message{
    Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls,
})
for _, tc := range resp.ToolCalls {
    msgs = append(msgs, llm.Message{
        Role: "tool", ToolCallID: tc.ID, Content: run(tc),
    })
}
resp, err = client.CallWithTools(ctx, llm.Request{Messages: msgs}, tools)
```

| Field | Role | Meaning |
|-------|------|---------|
| `ToolCalls` | `assistant` only | calls the model made; `Content` may be empty |
| `ToolCallID` | `tool` only | the `ToolCall.ID` this result answers; result text in `Content` |

Keep `resp.ToolCalls` verbatim, including `ToolCall.ProviderData`, when
you store or replay history. It holds provider-opaque state keyed by
adapter namespace, which the provider needs back on the same call.
Gemini thinking models are the case today: they put a thought signature
on the first call of each step and return 400 ("Function call is
missing a thought_signature") if it is missing from the current turn.
Each adapter reads only its own key and ignores the rest, so history
can pass between providers in a fallback chain.

Wire mapping per adapter:

| Adapter | Assistant calls | Tool result |
|---------|-----------------|-------------|
| `openai` and its schemes, `routellm` | `tool_calls` | `role: tool` + `tool_call_id` |
| `anthropic` | `tool_use` blocks | `tool_result` blocks in one `user` message per run of results |
| `gemini` / `google` | `functionCall` parts | `functionResponse` parts in one `user` content; name from the linked call; a JSON-object result is the response, other text is `{"output": ...}` |
| `ollama` | `tool_calls`, arguments as an object | `role: tool` + `tool_call_id` + `tool_name` |

- Gemini may omit call IDs; the adapter then synthesizes one
  (`call_...`), so `ToolCall.ID` is always set. Requests to Gemini carry
  no IDs: it pairs results with calls by name and order.
- Gemini thought signatures live in `ProviderData["google"]` as
  `{"thought_signature": "..."}` and go back as `thoughtSignature` on
  the same `functionCall` part. With parallel calls only the first
  carries one. A step in the current turn with no signed call (history
  built by hand or by another provider) gets Gemini's documented
  `skip_thought_signature_validator` on its first call, so the request
  is accepted, at some cost to reasoning quality; earlier turns go out
  as given.
- The router server's OpenAI-compatible endpoint maps a tool call's
  `extra_content` onto `ProviderData` unchanged.
- Adapters return an error instead of degrading: `ToolCalls` or
  `ToolCallID` on the wrong role, a result with `Parts`, arguments that
  are not valid JSON, a call without an ID (`openai`, `anthropic`), a
  result without `ToolCallID` (`openai`, `anthropic`, `gemini`), or a
  Gemini result whose ID matches no earlier call. `ollama` still accepts
  a bare `role: tool` message, as it did before.
- A tool error travels as result text; no adapter sets a provider
  error flag such as Anthropic's `is_error`.

## Fallback chains

```go
client := llm.NewClient(primary,
    llm.WithFallback(secondary),
    llm.WithFallback(tertiary),
    llm.OnFallback(func(from, to int, err error) {
        log.Printf("fallback %d→%d: %v", from, to, err)
    }),
)
```

## Multimodal

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

## Event hooks

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

## Bus topics

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

## Configuration

```go
cfg, _ := llm.LoadConfig("anthropic://claude-sonnet-4-5-20250514?temperature=0.7")
```

Three-layer merge: config file < URI params < env vars.

A provider block in `{xdg.ConfigDir("hop")}/llm.yaml` may carry the key
itself or name the variable holding it:

```yaml
providers:
  openrouter:
    api_key_env: MY_OPENROUTER_KEY   # or api_key: sk-or-...
```

`LoadConfig` reads `api_key`, else the variable `api_key_env` names.
[Provider keys](#provider-keys) gives both the highest precedence.

## Provider keys

`llm.Resolve` reads a key from the URI's `api_key` param and nowhere
else, so a URI-form model such as `openrouter://openai/gpt-4.1-nano`
reaches its provider unauthenticated unless the key is put on the URI.
`llm.ApplyAPIKey` does that:

```go
uri, err := llm.ApplyAPIKey(ctx, store, "openrouter://openai/gpt-4.1-nano")
switch {
case errors.Is(err, llm.ErrMissingKey):
    var missing *llm.MissingKeyError
    errors.As(err, &missing) // missing.EnvVars[0] == "OPENROUTER_API_KEY"
    // word the message and exit code your way
case err != nil:
    // secret-store backend failure, or uri has no scheme
}
provider, err := llm.Resolve(uri)
```

Key sources, highest precedence first:

1. `llm.yaml` `providers.<scheme>.api_key`, then the variable
   `providers.<scheme>.api_key_env` names (other names still follow)
2. the adapter's declaration (`llm.Declaration.Key`, given at
   `Register`): google's two variables, the local runtimes
3. aim catalog facts for the provider: its key variables in catalog
   order (names ending `_KEY`, `_APIKEY`, `_PAT`, `_TOKEN`), optional
   when its base URL is on loopback; read from the on-disk cache only
4. the `<SCHEME>_API_KEY` convention, other characters folded to `_`
5. `LLM_API_KEY`, for required keys only

A URI's own `api_key` outranks all five. For the registered schemes
every layer agrees, cache or not:

| Scheme | Key variables, highest precedence first | Required |
|--------|------------------------------------------|----------|
| `anthropic` | `ANTHROPIC_API_KEY` | yes |
| `openai` | `OPENAI_API_KEY` | yes |
| `google`, `gemini` | `GOOGLE_API_KEY`, then `GEMINI_API_KEY` | yes |
| `openrouter` | `OPENROUTER_API_KEY` | yes |
| `groq` | `GROQ_API_KEY` | yes |
| `xai` | `XAI_API_KEY` | yes |
| `together` | `TOGETHER_API_KEY` | yes |
| `fireworks` | `FIREWORKS_API_KEY` | yes |
| `deepseek` | `DEEPSEEK_API_KEY` | yes |
| `mistral` | `MISTRAL_API_KEY` | yes |
| `ollama` | `OLLAMA_API_KEY` | no |
| `routellm` | `ROUTELLM_API_KEY` | no |
| `triton` | `TRITON_API_KEY` | no |
| `lmstudio` | none | no |

An alias uses its scheme's key (`fireworks-ai` reads
`FIREWORKS_API_KEY`); a catalog provider its catalog variables
(`digitalocean` reads `DIGITALOCEAN_ACCESS_TOKEN`).

Resolution order for a required key: the secret store under each name
(nil store skips it), then the environment under each name, then
`LLM_API_KEY`. Every name is also the store key, uppercase. The google
order matches Google's genai SDK, which prefers `GOOGLE_API_KEY` when
both are set; the `google` adapter reads the environment in the same
order.

`ApplyAPIKey` leaves the URI unchanged when it already carries
`api_key`, when the scheme is local and its own variable is unset
(`LLM_API_KEY` is never lent to a local runtime), and when no adapter
serves the scheme (kit lends no credential to a host it cannot reach). It needs `scheme://`; mapping a bare model id to a
scheme is the caller's policy. A required key found nowhere returns a
`*MissingKeyError` (`errors.Is(err, llm.ErrMissingKey)`, also
`secret.ErrNotFound`) listing the names consulted. Errors name
variables, never values; the returned URI holds the key, so don't log it.

Lower-level helpers: `llm.ProviderKeyFor(uri)` returns the resolved
variables and whether the key is optional (`ok` false when no adapter
serves the scheme), `llm.SecretFor(ctx, store, uri)` returns the key
itself, and `llm.EnvKeyFor(uri)` returns one name (`api_key_env` when
set; for `google`/`gemini` it stays `GEMINI_API_KEY` for compatibility;
unknown schemes and `lmstudio` get `LLM_API_KEY`).

## Model registry

`aim` (`hop.top/aim`, `v0.1.0-alpha.0`) is the source of truth for model
metadata — capabilities, modalities, cost, context windows. The picker
consumes this accessor; library code calls `llm.Default(ctx)` rather than
constructing a registry directly so tests and embedders can inject custom
sources via `llm.SetDefaultRegistry`. The lazy default reuses one
`aim.NewRegistry` across calls; swapping the provider invalidates that cache.

```go
t := true
reg, err := llm.Default(ctx)
models, _ := reg.Models(ctx, aim.Filter{ToolCall: &t})
```

## Request profile and budget tier

`RequestProfile` is the consumer-facing input to `PickProvider`:
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

## Picker

`PickProvider` selects a single `*aim.Model` for a profile and budget:

```go
func PickProvider(ctx context.Context, reg *aim.Registry, profile RequestProfile, budget BudgetTier) (*aim.Model, error)
```

- Filter: queries `reg.Models(ctx, profile.Filter)`, then drops candidates
  whose known `Limit.Context` / `Limit.Output` falls below the profile's
  bounds. Unknown limits (zero) pass through.
- Rank: `BudgetCheap` minimises token-weighted price
  (`0.75*Cost.Input + 0.25*Cost.Output`); `BudgetPremium` maximises
  `Limit.Context` and tiebreaks on `Cost.Input`; `BudgetBalanced` picks
  `survivors[len/2]` after the price-asc sort. For even-sized survivor
  lists this is the upper-middle entry (e.g. `len=2` picks the more
  expensive of the two). Nil-cost models are price 0 (Cheap prefers,
  Premium loses tiebreaks).
- Tiebreak: alphabetical `(Provider, ID)` makes every call deterministic.

```go
reg, _ := llm.Default(ctx)
m, err := llm.PickProvider(ctx, reg, prof, llm.BudgetBalanced)
```

Errors are sentinel + structured: `errors.Is(err, llm.ErrNoProviderMatches)`
detects the no-match case; `var nme *llm.NoMatchError; errors.As(err, &nme)`
extracts `CandidateCount` and per-model `Eliminated` reasons for logs.

## Pool configuration

A `pool` block in `llm.yaml` restricts which `(scheme, model)` pairs the
picker is allowed to pick. An empty or missing pool means "everything in
aim's registry is fair game".

```yaml
pool:
  - alias: fast
    scheme: openai
    model: gpt-4o-mini
  - alias: legacy
    scheme: openai
    model: gpt-3.5-turbo
    enabled: false
```

Resolution order is **file < env < CLI**: `LLM_POOL_DISABLE` is a comma-
separated list of aliases or `scheme:model` strings that flips matching
entries off; downstream CLIs that already parsed flags pass the same shape
to `ResolvePool` for a final layer of overrides.

```go
pool, _ := llm.LoadPool()
m, _ := llm.PickProviderInPool(ctx, reg, prof, llm.BudgetBalanced, pool)
```

Pool eliminations surface through `NoMatchError.Eliminated` with
`Stage == "pool_disabled"` so operators can distinguish "pool too narrow"
from "all pool members eliminated by budget caps".

## Tracing

`PickProvider` emits one structured `slog` event per call, gated on the
`LLM_PICKER_TRACE` environment variable. Recognised truthy values (case-
insensitive): `1`, `true`, `on`, `yes`. Anything else, including unset,
suppresses the event. Tracing also stays silent on registry-query errors —
only successful picks and `ErrNoProviderMatches` outcomes trace.

Stable keys: `picker.budget`, `picker.filter.{tool_call,reasoning,structured_output,temperature,provider,family,input,output}`,
`picker.profile.max_{input,output}_tokens`, `picker.candidate_count`,
`picker.eliminated_count`, `picker.outcome` (`matched` / `no_match`), and on
match `picker.chosen.provider` / `picker.chosen.model`. See the `picker.go`
package doc for the full list.

Sample line:

```text
level=INFO msg=llm.pick picker.budget=balanced picker.filter.tool_call=true picker.filter.reasoning=<nil> picker.filter.structured_output=<nil> picker.filter.temperature=<nil> picker.profile.max_input_tokens=8192 picker.candidate_count=12 picker.eliminated_count=3 picker.outcome=matched picker.chosen.provider=openai picker.chosen.model=gpt-4o
```

Enable programmatically before invoking the picker:

```go
os.Setenv("LLM_PICKER_TRACE", "1")
m, err := llm.PickProvider(ctx, reg, prof, llm.BudgetBalanced)
```

## Custom adapters

```go
llm.Register("myscheme", func(cfg llm.ResolvedConfig) (llm.Provider, error) {
    return &MyAdapter{model: cfg.Model}, nil
})
```

Without a declaration the scheme's key comes from the aim catalog, else
`MYSCHEME_API_KEY`. Declare what the catalog cannot know:

```go
llm.Register("myscheme", New, llm.Declaration{
    Key:       &llm.ProviderKey{EnvVars: []string{"MY_KEY", "MY_OLD_KEY"}},
    BaseURL:   "https://api.example.com/v1", // passed as cfg.Provider.BaseURL
    Protocols: []string{"my-protocol"},      // serve catalog providers speaking it
})
```

A declaration outranks the catalog. `Key` is used as given
(`&llm.ProviderKey{Optional: true}` with no names: takes no key);
`BaseURL` applies when the URI names none, aliases included; a protocol
has one claimant, and a second `Register` claiming it panics.

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
| [anthropic/](../../../go/ai/llm/anthropic/README.md) | Anthropic Messages API adapter |
| [openai/](../../../go/ai/llm/openai/README.md) | OpenAI-compatible adapter (+ OpenRouter, xAI, Groq, etc.) |
| [google/](../../../go/ai/llm/google/README.md) | Google Gemini REST adapter |
| [ollama/](../../../go/ai/llm/ollama/README.md) | Ollama local inference adapter |
| [triton/](../../../go/ai/llm/triton/README.md) | NVIDIA Triton Inference Server scorer |
| [routellm/](../../../go/ai/llm/routellm/README.md) | RouteLLM cost-aware routing adapter |
| [router/](../../../go/ai/llm/router/README.md) | Native routing engine (BERT, intent-based) |
| [errors/](../../../go/ai/llm/errors/README.md) | Structured error types with fallback semantics |

## Related pages

- [`go-primitives.md`](go-primitives.md): `llm` and its sub-packages among the Go primitives
- [`bus-api.md`](bus-api.md): the bus `WithBus` publishes to
- [`../concepts/bus-overview.md`](../concepts/bus-overview.md): topic naming the `kit.ai.*` topics follow
