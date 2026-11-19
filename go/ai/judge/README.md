# judge

## What it answers

How a Go tool asks for a typed judgment (a choice, a score on ordered
anchors, or yes / no / abstain) and gets back a validated answer with
probabilities and a confidence, independent of which backend produced it.
This package holds the types, the validation rules and the batch helper;
backends live in their own packages. Wrong package when you need raw text
from a model (`hop.top/kit/go/ai/llm`).

## Use it when

- you define what to ask → `judge.Question{Kind: judge.KindChoice, Options: ...}`, `judge.Case{State, Questions}`
- you check a case before sending it → `judge.Validate(c)`
- you check what a backend returned → `judge.ValidateAnswer(q, a)`, `judge.ValidateVerdict(c, v)`
- you judge many cases → `judge.Batch(ctx, j, cases, parallel)`; order kept, first error cancels the rest
- you need the label set a backend must answer from → `judge.Labels(q)`
- you turn score probabilities into a value in [0,1] → `judge.ScoreEV(probs, n)`

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`judge.go`](judge.go) | `Kind`, `Question`, `Case`, `Answer`, `Verdict`, `Judge`, `BatchJudge`, label and score helpers | you add a question kind or an answer field |
| [`validate.go`](validate.go) | `Validate`, `ValidateAnswer`, `ValidateVerdict` | you change a validation rule |
| [`batch.go`](batch.go) | `Batch` | you change how cases are fanned out |
| [`calibrate/`](calibrate/) | temperature and Platt calibration, `Fit`, `Calibrated` | you calibrate a backend's probabilities |
| [`judgetest/`](judgetest/) | deterministic fake judge | you test code that calls a judge |
| [`jev/`](jev/) | Jev backend (TypeSafe System One via OpenRouter): native probabilities, USD cost; cloud only | you judge with Jev |
| [`llmjudge/`](llmjudge/) | chat-model backend: verbalized, votes, logprobs | you judge with a chat model |

## Related

- [`docs/adopters/reference/judge.md`](../../../docs/adopters/reference/judge.md)
