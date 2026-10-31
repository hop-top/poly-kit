# router

## What it answers

How a tool sends each request to a strong or a weak model by scoring
the prompt: a port of Go `ai/llm/router`, the native routing engine.
Feature `llm-router` (builds on `llm-triton` and `llm-picker`), native
only.

## Use it when

- you route by model string → register routers on a `Registry`, build a `Controller::builder(registry, ModelPair { strong, weak }).provider(p).build()`, then `controller.complete(&req)` with `req.model = "router-mf-0.116"` (`router-<name>-<threshold>`, the name may hold dashes)
- you route yourself → `route(&UserSignal, "mf", 0.5)` or `route_from_model`; the strong model wins at a score at or above the threshold
- you need a scorer → `RandomRouter` (baseline), `MfRouter` (embedder plus Triton scorer), `BertRouter` (tokenizer plus Triton scorer; `score_from_logits` for raw logits), `SwRankingRouter` (similarity-weighted Elo over arena battles)
- the model pair depends on the prompt → `IntentDetector` with example prompts and an `Embedder`, wired as `IntentModelSelector` middleware
- a router should steer the picker → `ScoreClassifier` or `IntentClassifier` as the picker's `PromptClassifier`
- a router routes on modality → override `Router::score_signal`

## Contract

- Errors read `routing: <message>`; a scoring failure is `router "<name>" scoring failed: ...`. Thresholds outside `[0, 1]` (NaN included) are refused.
- Middleware runs in order, each seeing the prompt; an error or `None` keeps the pair so far. `model_counts` snapshots routes per router per model.
- `complete` needs a provider with the complete capability and sends the request with the selected model.
- `last_user_signal` reads the last user message (else the last message): text of parts concatenated, image/audio/video flags.
- Embedders and tokenizers are seams; none ships. Embedding fits are deterministic here: models are visited in name order (Go iterates a map), ties in tiers break by name.
- Not ported: Go's OpenAI-compatible HTTP `Server` and its config types.

## See also

- [`go/ai/llm/router`](../../../../../go/ai/llm/router), the Go reference
- [`picker`](../picker/README.md): the classifier seam
- [`triton`](../triton/README.md): the scorer
