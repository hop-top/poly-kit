# AI and agents

Open when calling models, routing between them, or grading a CLI. Long form:
[Rust SDK reference](https://github.com/hop-top/poly-kit/blob/next/docs/adopters/reference/rs-sdk.md#feature-notes).

| Need | Feature | Notes |
|------|---------|-------|
| Messages, tools, streaming, fallback, `scheme://model` URIs | `llm` | core is wasm-safe; reads `llm.yaml` through `config` |
| Anthropic, OpenAI and compatible hosts | `llm-anthropic`, `llm-openai` | native; HTTP through the guarded client |
| Gemini, Ollama, Triton, RouteLLM | `llm-google`, `llm-ollama`, `llm-triton`, `llm-routellm` | native |
| Model picker over the `llm.yaml` pool | `llm-picker` | wasm-safe |
| Routing engine over `router-*` models | `llm-router` | native; scores through Triton |
| 12-factor AI CLI check over a toolspec | `compliance` | static pass |

Gaps taken from the parity page:

- LLM client: the page names no Rust gap; the shared corpus is `llm-v1`
  (Go, Rust, Python, TypeScript).
- LLM providers: Rust has anthropic, openai, google, ollama and triton.
- 12-factor compliance check: the static pass only, no runtime pass.

Toolspec, command reflection, agent CLI invocation and extensions have no
Rust module: [other ports](other-ports.md).
