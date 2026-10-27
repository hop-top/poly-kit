# LLM Client Reference

> Reference for `hop.top/kit/go/ai/llm`: provider-agnostic LLM client
> for Go. Unified interface for completions, streaming, tool calling,
> image generation, speech synthesis, transcription, and video analysis
> across providers. Config merged from llm.yaml, env and the URI, the
> URI highest.
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

Point at another host or port with a host-form URI or `?base_url=`:

```text
lmstudio://gpu-box:1234/qwen2.5-7b-instruct
lmstudio://qwen2.5-7b-instruct?base_url=http://gpu-box:1234/v1
ollama://gpu-box:11434/llama3.2:3b
ollama://llama3.2:3b?base_url=http://gpu-box:11434
```

A local server behind an authenticating proxy (or ollama.com's API)
takes its key from the URI as any provider does: with `OLLAMA_API_KEY`
or `TRITON_API_KEY` set, `ApplyAPIKey` puts it on the URI and the
`ollama` and `triton` adapters send it as `Authorization: Bearer <key>`
on every request (ollama chat, streamed chat and image generation;
triton inference). Unset or [blank](#blank-keys), no `Authorization`
header is sent and nothing is required.

LM Studio serves its OpenAI-compatible API under `/v1` only, so
`lmstudio` appends `/v1` to any base URL that doesn't already end in
it: a host-form URI, `?base_url=`, a config-file `base_url` or
`LLM_BASE_URL`. `http://gpu-box:1234` and `http://gpu-box:1234/v1`
reach the same endpoint.

An `openai` host-form URI reaches a self-hosted OpenAI-compatible
server under `/v1`, where vLLM, llama.cpp's `llama-server`, TGI,
Ollama's OpenAI endpoint and LocalAI serve the API, as
`api.openai.com` does:

```text
openai://localhost:8000/meta-llama/Llama-3.1-8B-Instruct   # http://localhost:8000/v1
openai://gpu-box:8080/qwen2.5-7b-instruct                  # http://gpu-box:8080/v1
```

A host form can't carry a path: `openai://gpu-box:8000/v1/m` names the
model `v1/m`. For an API mounted anywhere else, drop the host and name
its root with `?base_url=` (which outranks the host), `LLM_BASE_URL` or
a config-file `base_url` (which a host form outranks; see
[Configuration](#configuration)). `openai` uses those as given and never
appends `/v1`, so a gateway or proxy prefix reaches the right path:

```text
openai://my-model?base_url=http://gpu-box:4000                      # LiteLLM proxy root
openai://gpt-4o?base_url=https://gateway.example/acct/gw/openai     # gateway prefix
```

Other schemes use the base URL as given, host form included.

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

### Credentials in errors

A URI can carry a key (`?api_key=`), and a base URL can carry one in
its userinfo or query. No llm or adapter error echoes it: each URI or
request URL an error quotes is masked, scheme, host and model kept.
The openai-compatible and anthropic adapters reject an unparseable base
URL when the provider is created, with the URL masked, instead of on
every request.

```text
llm: invalid URI "openai/gpt-4o?api_key=REDACTED": missing scheme in URI "openai/gpt-4o?api_key=REDACTED"
```

Mask the URIs you print or log yourself the same way:

- `llm.RedactURI(uri)` replaces the value of `api_key`, `key`, `token`,
  `secret`, `password`, `signature` and names ending in `_key`,
  `_token`, `_secret` and the like, plus userinfo (`user:pass@`), with
  `REDACTED`. The URI need not parse.
- `llm.RedactURLError(err)` masks the URL net/http quotes in a
  transport error (`*url.Error`); use the error it returns.

## Quick start

```go
provider, _ := llm.Resolve("anthropic://claude-sonnet-4-5-20250514")
client := llm.NewClient(provider)

resp, _ := client.Complete(ctx, llm.Request{Messages: []llm.Message{
    {Role: "user", Content: "Hello"},
}})
fmt.Println(resp.Content)
```

## Streaming

```go
iter, _ := client.Stream(ctx, req)
defer iter.Close()
for {
    tok, err := iter.Next()
    if err != nil {
        return err // typed, see Errors; io.EOF only after Done
    }
    if tok.Done {
        break
    }
    fmt.Print(tok.Content)
}
```

The last token has `Done` set; every `Next` after it returns `io.EOF`.
`client.StreamWithTools(ctx, req, tools)` streams a tool-calling turn the
same way; once `Done` arrived, `ToolStream.Outcome()` returns the whole
turn (text, thinking blocks, tool calls, stop reason, usage). After an
error, a cancelled `ctx` or `Close`, there is no outcome.

### Tool-call and usage events

Set `Request.StreamEvents` and a stream also yields, as they happen,
tool-call events in `Token.ToolCall` and usage snapshots in
`Token.Usage`. Without it a stream yields exactly what it always has.

```go
req.StreamEvents = true
st, _ := client.StreamWithTools(ctx, req, tools)
for {
    tok, err := st.Next()
    if err != nil || tok.Done {
        break
    }
    switch {
    case tok.Usage != nil:
        if tok.Usage.Cost(price) > budget {
            st.Close() // stop paying; no outcome
        }
    case tok.ToolCall != nil && tok.ToolCall.Kind == llm.ToolCallEnd:
        go run(*tok.ToolCall.Call) // complete, before the turn ends
    default:
        fmt.Print(tok.Content)
    }
}
```

| `Token` field | When | Carries |
|---------------|------|---------|
| `ToolCall`, `Kind` `ToolCallStart` | a call opens | `Index` (its position in `Outcome().ToolCalls`), `ID`, `Name` (your original name) |
| `ToolCall`, `Kind` `ToolCallDelta` | arguments stream | `Index`, `ArgumentsDelta`: a raw JSON fragment, not valid alone |
| `ToolCall`, `Kind` `ToolCallEnd` | a call is complete | `Index`, `ID`, `Name`, `Call`: exactly `Outcome().ToolCalls[Index]` |
| `Usage` | the provider reports usage | the turn's usage so far: cumulative, never a delta; the last equals `Outcome().Usage` |

Each call goes start, deltas, end; parallel calls may interleave, told
apart by `Index`. A call that started and did not end (the stream
failed, was cancelled or closed) did not complete: no end event is made
up for it, and `Outcome()` reports none.

**Budgets.** Usage arrives only when the provider sends it. Anthropic
sends input and cache tokens when the message starts, so an input or
cache budget can stop a turn before any output; it sends the output
count only near the end (`message_delta`), so a running output budget
needs your own estimate from the text deltas. kit does not estimate.

Support: `anthropic` (`Stream`, `StreamWithTools`). `openai`,
`routellm` and `gemini` stream text only and implement no
`StreamWithTools` yet; they ignore `StreamEvents`. When they do, the
mapping will be:

| Provider stream | Events |
|-----------------|--------|
| Anthropic | `content_block_start` (tool_use) → start; `input_json_delta` → delta; `content_block_stop` → end; usage on `message_start` (input, cache) and `message_delta` (cumulative output) |
| OpenAI | `tool_calls[i]` → start on the fragment carrying id and name, then deltas; every open call ends, in index order, on the `finish_reason` chunk; usage only with `stream_options.include_usage`, in the final chunk |
| Gemini | a `functionCall` arrives whole: start, one delta, end back to back; `usageMetadata` on every chunk (true mid-stream usage) |

## System messages

Put any number of `Role: "system"` messages anywhere in
`Request.Messages`; none is dropped.

| Adapter | Sent as |
|---------|---------|
| `anthropic` | the `system` field: every system message's `Content`, in order, joined with a blank line (`"\n\n"`); empty ones skipped |
| `openai` and its schemes, `routellm` | one `system` message each, in place |
| `gemini` / `google` | one `systemInstruction` part each, in order |

On `anthropic`, a `CacheControl` on a system message ends a system text
block there and carries that breakpoint: system messages `a` (cached),
`b`, `c` (cached, 1h), `d` go out as three blocks `a` | `b\n\nc` | `d`,
so each breakpoint caches exactly the text up to it.

## Tool calling

```go
resp, _ := client.CallWithTools(ctx, llm.Request{Messages: messages}, []llm.ToolDef{
    {Name: "weather", Description: "Get weather", Parameters: schema},
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

### Tool choice

`Request.ToolChoice` constrains tool use on `CallWithTools` and
`StreamWithTools`; nil leaves the provider default.

```go
req.ToolChoice = &llm.ToolChoice{Mode: llm.ToolChoiceTool, Name: "pane.open"}
```

| Mode | `anthropic` | `openai`, `routellm` | `gemini` / `google` |
|------|-------------|----------------------|---------------------|
| `ToolChoiceAuto` | `{"type":"auto"}` | `"auto"` | `AUTO` |
| `ToolChoiceAny` (call at least one) | `{"type":"any"}` | `"required"` | `ANY` |
| `ToolChoiceNone` | `{"type":"none"}` | `"none"` | `NONE` |
| `ToolChoiceTool` + `Name` | `{"type":"tool","name":…}` | `{"type":"function","function":{"name":…}}` | `ANY` + `allowedFunctionNames` |

`Name` is the tool's `ToolDef.Name` as you defined it; the adapter sends
the provider-safe form ([Tool names](#tool-names)). Before any network
call, a request fails when `llm.CheckToolChoice` does: an unknown mode,
a choice on a call without tools (`Complete`, `Stream`, every `ollama`
call), `ToolChoiceTool` without a `Name`, a `Name` with another mode,
or a `Name` no tool has.

### Tool names

Providers restrict tool names; ids such as `pane.open` or
`capability:files.read` work as `ToolDef.Name` all the same. Each
tool-calling adapter sends a name its provider rejects in a form it
accepts, and every `ToolCall` it returns (`ToolResponse.ToolCalls`,
`StreamOutcome.ToolCalls`) carries your original name. Replayed history
is mapped the same way, so `resp.ToolCalls` goes back verbatim.

| Adapter | Accepted names |
|---------|----------------|
| `anthropic` | `^[a-zA-Z0-9_-]{1,128}$` |
| `openai` and its schemes, `routellm` | `^[a-zA-Z0-9_-]{1,64}$` |
| `gemini` / `google` | `^[a-zA-Z_][a-zA-Z0-9_.:-]{0,127}$` |

A valid name goes out unchanged. Otherwise each run of other characters
becomes one `_` (Gemini also gets a leading `_` when the name does not
start with a letter or `_`), and a name still too long, or with no
ASCII letter or digit left, is cut and ends in `_` plus 8 hex digits of
its SHA-256: `pane.open` is sent as `pane_open`, a 65-character name to
OpenAI as 55 characters + `_635361c4`. `llm.AnthropicToolNames.Sanitize`
(and the `OpenAI`, `Gemini` rules) returns what a name becomes. The
algorithm is the same in every kit SDK:
[contracts/llm-v1/tool-names.md](../../../contracts/llm-v1/tool-names.md).

Before any network call, a request fails with `*errors.ErrToolName`
(not fallbackable; `Reason`, `Name`, `Other`) when a tool name is empty
(`empty`) or not UTF-8 (`invalid_utf8`), defined twice (`duplicate`),
or maps to the same sent name as an earlier tool (`collision`, e.g.
`pane.open` and `pane:open`; `Other` is the earlier one).

Opt out of rewriting with the `tool_names` provider param:
`anthropic://claude-sonnet-4-5?tool_names=strict` sends names as given
and fails a name the provider rejects with reason `invalid`, before
sending. `tool_names=sanitize` is the default; any other value fails
`Resolve`.

### Thinking and prompt caching

`Request.Thinking = &llm.ThinkingConfig{BudgetTokens: n}` enables
extended thinking (`anthropic`: at least 1024, below `MaxTokens`).
Thinking deltas stream as `Token.Thinking`; the blocks come back in
`Response.Thinking`, `ToolResponse.Thinking` and
`StreamOutcome.Thinking`. Replay them unchanged on the assistant
message (`Message.Thinking`) in the next turn; tool use with thinking
requires it.

`Message.CacheControl` and `ToolDef.CacheControl` mark prompt-cache
breakpoints (`TTL` "", "5m" or "1h"): on a system message its text,
otherwise its last content block. `Usage.CacheReadTokens` and
`CacheCreationTokens` report cache traffic, included in `TotalTokens`;
`Usage.Cost(llm.Price{...})` prices each kind at its own rate.

`StreamWithTools` (`llm.ToolStreamer`) streams text, then the whole
turn from `ToolStream.Outcome()`; with `Request.StreamEvents`, tool calls
and usage also stream as they happen ([Streaming](#tool-call-and-usage-events)).

## Errors

Adapters map provider failures to `hop.top/kit/go/ai/llm/errors`
types; `errors.IsFallbackable(err)` says whether the next provider in a
chain should be tried.

| Provider answer | Error | Fallbackable |
|-----------------|-------|--------------|
| 401, `authentication_error` | `*ErrAuth` | no |
| 403, `permission_error` | `*ErrPermissionDenied`, also matched as `*ErrAuth` | no |
| 404 | `*ErrModel` | no |
| 429 | `*ErrRateLimit` | yes |
| 5xx | `*HTTPStatusError` | yes |

A 403 means the key was accepted but may not do what was asked (no
access to the model, workspace or feature). Test for
`*ErrPermissionDenied` first to tell it from a bad key;
`errors.As(err, new(*ErrAuth))` matches both, as it did before the split.

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

Each field, highest precedence first:

| Field | Sources |
|-------|---------|
| `APIKey` | as [Provider keys](#provider-keys) resolves it, no secret store: URI `api_key`, `llm.yaml` `api_key` / `api_key_env`, the provider's variables, `LLM_API_KEY` (required keys only); empty when found nowhere. A [blank](#blank-keys) URI `api_key` is dropped from `URI.Params` and `Params` |
| `BaseURL` | URI `?base_url=`, URI host (`http://host:port`), `LLM_BASE_URL`, `llm.yaml` `base_url` |
| `Model` | URI model, `llm.yaml` `model` |

The URI is the caller's own choice and outranks everything, as in
`llm.Resolve`, which reads the URI alone: for a URI naming its key or
endpoint, `LoadConfig` and `Resolve` agree. `LoadConfig` and
`ApplyAPIKey` agree on the key in every case.

A provider block in `{xdg.ConfigDir("hop")}/llm.yaml` may carry the key
itself or name the variable holding it:

```yaml
providers:
  openrouter:
    api_key_env: MY_OPENROUTER_KEY   # or api_key: sk-or-...
```

`LoadConfig` and `ApplyAPIKey` read `api_key`, else the variable
`api_key_env` names; [Provider keys](#provider-keys) gives both the
highest precedence after the URI's own `api_key`.

### Provider blocks and aliases

A block configures its provider under any of the provider's names, for
`LoadConfig`, key resolution and `ProviderSettingsFor` alike. Blocks
tried, first found wins and is used whole (never merged):

1. `providers.<scheme>`
2. for an alias, the registered scheme it reaches (`fireworks-ai`
   reads `providers.fireworks`)
3. aim's catalog id and curated aliases for the provider (`fireworks`
   reads `providers.fireworks-ai`, `google` reads `providers.gemini`)

A catalog provider routed by protocol skips step 2: `digitalocean`
never reads `providers.openai`, whose key and base URL belong to
another host. A scheme no adapter serves reads its own block only.

`llm.ProviderSettingsFor(uri)` returns that block as written (base
URL, model, `api_key_env`, extras, and `Block`, the key it sits under),
without the URI or `LLM_BASE_URL` layered over it and never the
`api_key` value. Use it to apply the file's `base_url` yourself, e.g.
to a fallback URI that must not inherit the primary's `LLM_BASE_URL`:

```go
if s, ok := llm.ProviderSettingsFor("fireworks-ai"); ok && s.BaseURL != "" {
    uri += "?base_url=" + s.BaseURL
}
```

## Provider keys

`llm.Resolve` reads a key from the URI's `api_key` param and nowhere
else, so a URI-form model such as `openrouter://openai/gpt-4.1-nano`
reaches its provider unauthenticated unless the key is put on the URI.
A [blank](#blank-keys) `api_key` is no key: `Resolve` drops it and the
adapter gets none.
`llm.ApplyAPIKey` does that:

```go
uri, err := llm.ApplyAPIKey(ctx, store, "openrouter://openai/gpt-4.1-nano")
switch {
case errors.Is(err, llm.ErrMissingKey):
    var missing *llm.MissingKeyError
    errors.As(err, &missing) // missing.EnvVars[0] == "OPENROUTER_API_KEY"
    // word the message and exit code your way
case err != nil:
    // uri has no scheme, or the key cannot travel in a URI
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

A URI's own `api_key` outranks all five. A [blank](#blank-keys) value
at any source counts as unset, so the next source answers. For the
registered schemes every layer agrees, cache or not:

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
| `ollama` | `OLLAMA_API_KEY`, sent as a bearer token | no |
| `routellm` | `ROUTELLM_API_KEY`, sent as a bearer token | no |
| `triton` | `TRITON_API_KEY`, sent as a bearer token | no |
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

A secret-store backend failure (a locked keyring, an unreachable
vault: any error but `secret.ErrNotFound`) is not a missing key and
does not stop the search: that name counts as absent from the store,
and the next name, the environment and `LLM_API_KEY` still answer. It
is surfaced, never dropped. When no source has the key, the
`*MissingKeyError` carries it in `StoreErr` (also in its message and
reachable with `errors.Is`); when a later source supplies the key,
`ApplyAPIKey` and `SecretFor` log it as a warning on `slog.Default()`.
Store errors name keys, never values.

`ApplyAPIKey` leaves the URI unchanged when it already carries a
non-blank `api_key`. A blank one (`?api_key=`, `?api_key`, spaces) is
removed and the key resolves as if the URI named none, so
`openai://m?api_key=` with no key set returns a `*MissingKeyError`
before any request. The URI is also unchanged, a blank `api_key` aside,
when the scheme is local and its own variable is unset
(`LLM_API_KEY` is never lent to a local runtime), and when no adapter
serves the scheme and its own `llm.yaml` block names no key (kit lends
no other credential to a host it cannot reach). It needs `scheme://`; mapping a bare model id to a
scheme is the caller's policy. A required key found nowhere returns a
`*MissingKeyError` (`errors.Is(err, llm.ErrMissingKey)`, also
`secret.ErrNotFound`) listing the names consulted. Errors name
variables, never values; the returned URI holds the key, so don't log it.

### Blank keys

A key that is empty or only whitespace is no key, at every source: the
URI's `api_key` param, `llm.yaml` `api_key`, the variable `api_key_env`
names, the secret store, the provider variables and `LLM_API_KEY`. It
counts as unset: resolution moves on to the next source, a required key
blank everywhere is a `*MissingKeyError`, and nothing blank is sent (an
`Authorization: Bearer` header with no token is never valid). A blank
`api_key_env` names no variable. `ApplyAPIKey`, `ResolveAPIKey`,
`LoadConfig` and `SecretFor` agree on this for every source.

The adapters hold the same line for what reaches them directly: the
`openai` adapter sends no `Authorization` header for a blank key (and
never the SDK's own `OPENAI_API_KEY` in its place), `anthropic` refuses
a blank key and does not send a blank `ANTHROPIC_AUTH_TOKEN`,
`google` falls through a blank key to its variables, and `ollama` and
`triton` send no `Authorization` header for a blank key.

A non-blank key is used as written, surrounding whitespace included.

### Where a key may go

A key kit resolved from `llm.yaml`, a secret store or the environment
goes to an endpoint the URI names (its `base_url` param or host form)
only when that endpoint is loopback or shares an origin with the
provider's default (`Declaration.BaseURL`, `Declaration.Endpoints`, the
catalog's base URL), `llm.yaml` `base_url` or `LLM_BASE_URL`. To use
another host, configure it in one of those, or pass `api_key` in the
URI: a key the URI carries itself is the caller's choice. Any other
case is a `*KeyEndpointError` (`errors.Is(err, llm.ErrKeyEndpoint)`)
from `ApplyAPIKey` and `LoadConfig`.

Whatever its source, a key never goes over plain `http` to a host other
than loopback: `Resolve`, `LoadConfig` and the `anthropic` adapter
refuse it (`llm.CheckKeyEndpoint`). Local servers on `localhost` or
`127.0.0.1` keep working over http. `ResolveAPIKey` and `SecretFor`
send nothing and do not check.

An endpoint that carries userinfo (`user:pass@`), a backslash, a
percent-escape, whitespace or a control character, or does not parse,
gets no credential at all (`KeyEndpointError.Malformed`). Origins are
compared on the parsed URL: scheme, lower-cased host, port (or the
scheme's default).

Credentials kit takes from the environment rather than the caller
(`ANTHROPIC_AUTH_TOKEN`, the `anthropic-workspace-id` header) follow
the same rule through `llm.CredentialsTrusted`: the `anthropic` adapter
sends them only to loopback, the default endpoint, `ANTHROPIC_BASE_URL`,
`llm.yaml` `base_url` or `LLM_BASE_URL`, and refuses the token over
plain `http` to a remote host.

The `anthropic` adapter honors a server's `Retry-After-Ms` or
`Retry-After` only for a wait in (0, 60s]; any other value falls back
to the SDK's backoff.

The `anthropic` adapter also: sends `ANTHROPIC_AUTH_TOKEN` and the
workspace header only where `llm.CredentialsTrusted` allows; follows no
redirect (a 3xx is an `HTTPStatusError`); ends a stream without
`message_stop` with `*llmerrors.ErrStreamTruncated`, never a success;
classifies an SSE `error` event by its type (`overloaded_error` as a
fallbackable 529); bounds a response body to 32 MiB and one SSE event to
8 MiB (params `max_body_bytes`, `max_event_bytes`), past which it fails
with `*llmerrors.ErrResponseTooLarge`; and scrubs its credentials from
every error message.

### Where a key came from

`llm.ResolveAPIKey(ctx, store, uri)` runs the same resolution as
`ApplyAPIKey` and returns a `KeyResolution`: the key (`Value`), where
it came from (`Source`), the scheme's plan (`Known`, `Key`, as
`ProviderKeyFor` gives it) and any store failure met on the way
(`StoreErr`, returned, not logged). Report a key's origin from it
instead of instrumenting the store:

```go
res, err := llm.ResolveAPIKey(ctx, store, "openrouter://openai/gpt-4.1-nano")
fmt.Println(res.Source) // "store:OPENROUTER_API_KEY"; never print res.Value
```

| `Source.Kind` | Key came from | `Source.Name` |
|---------------|---------------|---------------|
| `uri` | the URI's non-blank `api_key` param | empty |
| `config` | an `llm.yaml` block's `api_key` | `providers.<block>.api_key` |
| `store` | the secret store | the key name asked for |
| `env` | a provider variable (incl. `api_key_env`'s) | the variable |
| `fallback` | `LLM_API_KEY` | `LLM_API_KEY` |
| `""` (none) | nothing: no key needed, or none found | empty |

A required key found nowhere returns a `*MissingKeyError` beside a
resolution that still describes the scheme. `String`, `%#v` and JSON
leave `Value` out.

Lower-level helpers: `llm.ProviderKeyFor(uri)` returns the resolved
variables and whether the key is optional (`ok` false when no adapter
serves the scheme), `llm.SecretFor(ctx, store, uri)` returns the key
itself (the same chain, ignoring the URI's own `api_key`), and `llm.EnvKeyFor(uri)` returns one name (`api_key_env` when
set; for `google`/`gemini` it stays `GEMINI_API_KEY` for compatibility;
unknown schemes and `lmstudio` get `LLM_API_KEY`).

### Secret stores are synchronous

`secret.Store` (`hop.top/kit/go/storage/secret`) is a blocking,
context-aware interface: `Get(ctx, key)`. `ApplyAPIKey`, `ResolveAPIKey`
and `SecretFor` call it in the caller's goroutine with the caller's
`ctx`, one name at a time, and return when it does. There is no async
variant; in Go the caller picks the concurrency:

- bound a slow backend (a vault over the network, a keyring prompt)
  with `context.WithTimeout`; a store must return when `ctx` is done.
  A store error, cancellation included, counts that name as absent and
  is reported in `StoreErr`, as above;
- resolve keys before the hot path (at startup, or on your own
  goroutine) and hand the resulting URI to `Resolve`;
- a store that is itself asynchronous wraps its call in `Get`, waiting
  on its result or `ctx.Done()`.

### Hermetic mode

An app embedding kit that owns its configuration passes
`llm.Hermetic()` to `Resolve`; nothing ambient is read then, only the
URI counts:

```go
p, err := llm.Resolve("anthropic://claude-sonnet-4-5?api_key="+key+"&base_url="+url, llm.Hermetic())
```

| Ambient input | Ambient (default) | Hermetic |
|---------------|-------------------|----------|
| `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_WORKSPACE_ID` | read by `anthropic` | ignored |
| `llm.yaml` (`base_url` trusting env credentials), `LLM_BASE_URL` | consulted by `anthropic` | not read |
| `OPENAI_ORG_ID`, `OPENAI_PROJECT_ID` (openai SDK headers) | sent by `openai` and its schemes | not sent |
| `ROUTELLM_*` | read by `routellm` | ignored |
| aim catalog cache (catalog-only providers) | `Resolve` routes by protocol | not read: only registered schemes and curated aliases resolve |

The flag reaches the adapter as `ResolvedConfig.Hermetic`; set it
yourself when you call an adapter's `New` directly. Without a
`base_url`, an adapter uses its built-in default endpoint. `LoadConfig`,
`ApplyAPIKey`, `ResolveAPIKey` and `SecretFor` exist to read `llm.yaml`
and the environment: a hermetic app does not call them.

## Model registry

`aim` (`hop.top/aim`, `v0.1.0-alpha.6`) is the source of truth for model
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
