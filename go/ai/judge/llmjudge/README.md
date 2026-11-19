# llmjudge

## What it answers

How to get a typed judgment (choice, score on anchors, yes / no / abstain)
from any chat model behind `llm.Completer`: the model states a probability
per label in schema-constrained JSON, the result is normalised into a
validated `judge.Answer`. Optional self-consistency votes turn N samples
into probabilities. Wrong package when you have a logprob-capable model and
want token probabilities, or need a hosted judgment service.

## Use it when

- you judge with a chat model → `llmjudge.New(client)`, then `Judge(ctx, c)`
- you want sample-based probabilities → `llmjudge.WithVotes(n)`; `Source` is `"votes"`
- you tune sampling → `llmjudge.WithTemperature(t)` (votes default 0.7), `llmjudge.WithModel(m)`
- you bound schema repair retries → `llmjudge.WithMaxRepairs(n)`
- you judge many cases → `judge.Batch(ctx, j, cases, parallel)`; no provider batch call here

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`llmjudge.go`](llmjudge.go) | `Judge`, `New`, options, parsing, vote aggregation | you change how answers are built |
| [`prompt.go`](prompt.go) | prompt text and reply JSON Schema | you change what the model is asked |
| [`llmjudge_test.go`](llmjudge_test.go) | hermetic tests with a fake completer | you add a behaviour |

## Related

- [`go/ai/judge`](../README.md)
- [`docs/adopters/reference/judge.md`](../../../../docs/adopters/reference/judge.md)
