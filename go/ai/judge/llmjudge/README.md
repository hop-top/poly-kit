# llmjudge

## What it answers

How to get a typed judgment (choice, score on anchors, yes / no / abstain)
from any chat model behind `llm.Completer`: the model returns a value, a
confidence and a reason in schema-constrained JSON, which becomes a
validated `judge.Answer`. Optional self-consistency votes turn N samples
into probabilities; `Logprobs` mode reads them off the answer token's
top_logprobs. Wrong package when you need a hosted judgment service.

## Use it when

- you judge with a chat model → `llmjudge.New(client)`, then `Judge(ctx, c)`
- you want sample-based probabilities → `llmjudge.Mode(llmjudge.Votes(n))`; `Source` is `"votes"`, samples run one after another, any failed sample fails the case
- you have a logprob-capable model (OpenAI-compatible, Ollama) → `llmjudge.Mode(llmjudge.Logprobs)`; options become keys A..N (Y/N, X abstain), one call per question, `Source` is `"logprobs"`, at most 20 labels, no repair; calibrate after with `calibrate.Calibrated`
- you locate something in an image (preview) → `judge.KindGrounding` with a vision model, `Verbalized` only; local Qwen-VL models need `llmjudge.BoxFormat(Grid1000|Pixels)` (free text, native coordinates; `BoxFormatFor(model)` picks one), default `Fractions` uses a schema; `Votes(n > 1)` and `Logprobs` refuse with `llmerrors.ErrCapabilityNotSupported`
- you pick the model or sampling → `llmjudge.Model(m)`, `llmjudge.Temperature(t)` (votes default 0.7)
- you want `Cost.USD` → `llmjudge.Price(llm.Price{...})`
- you keep the system prompt in a pattern → `llmjudge.Pattern(p)` (`prompt.Pattern`)
- you bound schema repair retries → `llmjudge.MaxRepairs(n)`
- you judge many cases → `judge.Batch(ctx, j, cases, parallel)`; no provider batch call here

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`llmjudge.go`](llmjudge.go) | `Judge`, `New`, options, parsing, vote aggregation | you change how answers are built |
| [`prompt.go`](prompt.go) | prompt text and reply JSON Schema | you change what the model is asked |
| [`logprobs.go`](logprobs.go) | Logprobs mode: keys, answer-token lookup, softmax | you change how probabilities are read |
| [`llmjudge_test.go`](llmjudge_test.go) | hermetic tests with a fake completer | you add a behaviour |
| [`boxformat.go`](boxformat.go) | `BoxFormat` conventions: free-text prompt, lenient parse, pixel/grid scaling | you add a model's box convention |
| [`grounding_test.go`](grounding_test.go) | grounding tests (+ `boxformat_test.go`, Qwen3-VL cassette in `grounding_xrr_test.go`) | you change grounding |
| [`logprobs_test.go`](logprobs_test.go) | Logprobs tests, fake completer; Ollama cassette replay | you change Logprobs |

## Related

- [`go/ai/judge`](../README.md)
- [`docs/adopters/reference/judge.md`](../../../../docs/adopters/reference/judge.md)
