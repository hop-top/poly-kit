# AI and agents

Open when: calling models from a tool, building prompts or judgments, or
making a tool legible to agent harnesses.

## Decisions

| Need | Use |
|------|-----|
| call a model without hard-coding a vendor | `llm.Resolve("<scheme>://...")`, `llm.NewClient(provider).Chat` in `go/ai/llm` |
| stream tokens, tool calls, structured output | `client.Stream`, `client.CallWithTools`, `CompleteStructured` |
| fallback, routing by capability or budget | `llm.WithFallback`, `llm.PickProvider`; the `llm router` command tree is `go/console/cli/router` |
| provider key from the secret store | `llm.ApplyAPIKey(ctx, store, uri)`; `errors.Is(err, llm.ErrMissingKey)` |
| embed kit in an app that owns its config | `llm.Resolve(uri, llm.Hermetic())` |
| model calls in tests without the network | `go/ai/llm/llmxrr` (record and replay, scrubs keys) |
| versioned prompt templates | `go/ai/prompt` |
| typed judgments, calibration, evals | `go/ai/judge` and its `calibrate`, `judgetest`, `eval` subpackages |
| describe the tool to harnesses | `go/ai/toolspec` (`spec` command, per-harness adapters) |
| reflect a cobra tree into tool descriptors | `go/ai/cmdreflect` |
| drive other agent CLIs with one request shape | `go/core/uxp` |
| extension contract and discovery | `go/ai/ext` |
| check a tool against the 12-factor AI CLI spec | `go/core/compliance` |

## Traps

- Never log or print model URIs raw: `llm.RedactURI`. kit's own errors are
  already masked.
- URL-sourced media goes through `llm.URLSource`; it refuses private
  addresses and redirects.
- Request and response bus events carry metadata only unless content is
  opted into.
- A command an agent should call is also an MCP tool: see
  `serving-and-transports.md`; annotations drive what an agent may run.
- Test fakes and cassettes must not carry provider-format keys; build them
  at runtime.

## Pages

- [LLM](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/llm.md), [prompt](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/prompt.md),
  [judge](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/judge.md)
- [toolspec API](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/toolspec-api.md),
  [adopter guide](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/integrations/toolspec-adopter-guide.md),
  [cmdreflect](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/cmdreflect.md),
  [uxp](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/uxp.md),
  [compliance](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/compliance-api.md),
  [extension discovery](https://github.com/hop-top/poly-kit/blob/main/docs/contracts/ext-discover-protocol.md)
