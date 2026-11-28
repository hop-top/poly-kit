# AI and agents

Open when a tool calls an LLM, routes between models, exposes a tool spec, or
must pass the 12-factor check. Reference:
[Python SDK reference](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/py-sdk.md) (LLM messages,
provider adapters, tool names).

## Decisions

- Client: `hop_top_kit.llm.create_llm("openai://gpt-4", fallback=[...])`
  returns a `Client`; hooks `on_request`, `on_response`, `on_error`,
  `on_fallback`. `hermetic=True` reads nothing ambient. Adapters speak HTTP
  directly, with no vendor SDK dependency.
- Providers: `anthropic://` (`hop_top_kit.llm_anthropic`), `openai://` and
  OpenAI-compatible schemes such as `openrouter`, `groq`, `lmstudio`
  (`hop_top_kit.llm_openai`), `routellm://`
  (`hop_top_kit.routellm_adapter`). No Google or Ollama adapter: use Go or
  Rust for those.
- Provider tool-name rules and the reversible sanitizer:
  `hop_top_kit.llm_toolnames`. Metadata-only llm events:
  `hop_top_kit.llm_events`.
- 12-factor check: `hop_top_kit.compliance` (static pass).

## Check coverage first

Cell `partial`: LLM client and LLM providers. Cell `unverified`: LLM routing
(`routellm_adapter`, `routellm_grpc`) and `hop_top_kit.toolspec`.

Command reflection, agent CLI invocation and extensions have no module here:
[other ports](other-ports.md).
