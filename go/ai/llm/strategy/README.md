# strategy

## What it answers

How to call a model several times and combine the answers: a majority vote,
the best of n by a score, or a cheap model first with escalation to a
stronger one. Strategies take a `Sampler` (one call), not a client, so they
work over any backend and answer shape. Wrong package when you want one
call (`hop.top/kit/go/ai/llm`) or probabilities over a label set
(`go/ai/judge/llmjudge`, which has its own votes mode).

## Use it when

- you vote over n samples → `strategy.SelfConsistency(ctx, s, n)`; majority `Key`, ties to the first seen, `Shares` are vote probabilities
- you already hold the candidates → `strategy.Vote(cs)`
- you pick by score → `strategy.BestOfN(ctx, s, n, score)`; max score, ties to the first
- you try cheap first → `strategy.Cascade(ctx, stages...)`; stops at the first `Accept`, `ErrNoneAccepted` when none does
- you sample schema-checked JSON → `strategy.StructuredSampler(c, req, rf, key)`
- you read a `strategy:` config string → `strategy.Lookup("self-consistency:5")`, `"cascade:3:0.8"`
- you account for spend → `Result.Usage` sums every call, failed ones too

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`strategy.go`](strategy.go) | `Candidate`, `Sampler`, `Result`, `SelfConsistency`, `Vote`, `BestOfN` | you change how answers are combined |
| [`cascade.go`](cascade.go) | `Stage`, `Cascade` | you change escalation |
| [`structured.go`](structured.go) | `StructuredSampler` | you sample structured output |
| [`lookup.go`](lookup.go) | `Lookup` | you add a config name |
| [`strategy_test.go`](strategy_test.go) | hermetic tests with fake samplers | you add a behaviour |

## Related

- [`docs/adopters/reference/llm.md`](../../../../docs/adopters/reference/llm.md#strategies)
