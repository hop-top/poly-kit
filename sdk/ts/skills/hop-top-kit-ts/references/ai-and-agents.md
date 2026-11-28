# AI and agents

Open when calling an LLM, routing between models, emitting a capability manifest or running the
12-factor check. Status per capability: [coverage](coverage.md). Depth:
[TypeScript API reference](https://github.com/hop-top/poly-kit/blob/next/docs/adopters/reference/ts-api-reference.md)
(sections LLM adapters, LLM tool calling, Capability manifest).

## Modules

| Import | Use for | Cue |
|--------|---------|-----|
| `@hop-top/kit/llm` | client, registry, fallback, tool calling | `createLLM(uri, { fallback })`, `parseURI`, `loadConfig` |
| `@hop-top/kit/anthropic` | `anthropic://` scheme | import for its side effect, then `createLLM` |
| `@hop-top/kit/openai` | `openai://` and OpenAI-compatible schemes | same; `lmstudio://` needs no key |
| `@hop-top/kit/routellm` | `routellm://` strong/weak routing server | adapter over the openai one |
| `@hop-top/kit/toolspec` | `<tool> spec` capability manifest | `registerSpecCommand(program, '1.0')` after all commands |
| `@hop-top/kit/compliance` | 12-factor CLI check | `Factor` enum; static and runtime checks |
| `@hop-top/kit/aim` | model registry (models.dev source, cache) | `Registry`, `ModelsDevSource`, `Cache` |

Matrix cell `unverified`: LLM routing (`@hop-top/kit/routellm`). The LLM client and providers
are `partial`: Evidence names the gap (no Google or Ollama adapter here). Read it in coverage.

## LLM

- An adapter registers its URI schemes when imported: forget the import and `createLLM` has no
  provider for the scheme.
- Key order: the URI's `api_key`, then `llm.yaml` (under the `hop` config dir), then the scheme's
  env var (`ANTHROPIC_API_KEY`, `<SCHEME>_API_KEY`), then `LLM_API_KEY`. A key found nowhere
  throws `MissingKeyError`, listing the variables consulted.
- `stream` and `streamWithTools` return a `ToolStream`: iterate tokens, then `outcome()` holds
  content, tool calls and usage; it is undefined before the stream ends.
- `{ hermetic: true }` on `createLLM` reads nothing ambient (no env keys, no `llm.yaml`): the
  URI alone configures the provider.
- The client corpus is shared with Go, Rust and Python; behaviour beyond it is this port's.
- The in-process RouteLLM controller and the Triton client have source under `src/` but no
  entry in the package `exports` map when this was written: import `routellm` for the server
  adapter; the controller and Triton are not importable.

## Toolspec

- Commands describe themselves with `kit/*` annotations (`kit/side-effect`, `kit/args`,
  `kit/exit-codes`, `kit/output-schema`). Register once, after every command exists.
- `spec --format kit-manifest` emits Go's manifest byte for byte; `spec coverage --min N`
  exits 4 below `N`. The `mcp` format and Go's curation options are not ported.
- Reflecting a command tree at runtime and invoking other agent CLIs are Go-only (coverage).
