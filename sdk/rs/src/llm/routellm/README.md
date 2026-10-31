# routellm

## What it answers

How the llm core reaches a RouteLLM server: through the
[`openai`](../openai/README.md) adapter pointed at it, as Go
`ai/llm/routellm`. Registers `routellm`. Feature `llm-routellm`, native
only.

## Use it when

- you pick the router and threshold → `routellm://mf:0.7`, sent to the server as `router-mf-0.7`
- the server owns the choice → `routellm://private` (a tier name), sent verbatim
- the router config lives in llm.yaml → a `routellm:` extras block (`base_url`, `strong_model`, `weak_model`, `routers`, ...); `RouterConfig::parse`

## Contract

- Model field: the text after the last colon decides. A finite float means a router pair, threshold in `[0, 1]`; anything else (`mf:abc`, `mf:`, `:0.5`, `mf:NaN`) is a tier, passed through. `Adapter::is_tier` tells them apart.
- Base URL, highest first: provider `base_url`, `ROUTELLM_BASE_URL`, extras `routellm.base_url`, `http://localhost:6060`; `/v1` appended unless already there. Hermetic reads no `ROUTELLM_*`. `ROUTELLM_API_KEY` is optional.
- Params `tool_names`, `max_body_bytes`, `max_event_bytes` reach the inner adapter; its errors name the `routellm` scheme.
- Not ported: the YAML `ConfigWatcher`.

## See also

- [`go/ai/llm/routellm`](../../../../../../go/ai/llm/routellm), the Go reference
- [`contracts/llm-v1`](../../../../../../contracts/llm-v1/README.md): `routellm_base_url`
