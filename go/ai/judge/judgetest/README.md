# judgetest

## What it answers

How to test code that calls a `judge.Judge` without a model or a network:
a deterministic fake that returns scripted verdicts and records its calls.
Wrong package for production judging.

## Use it when

- you need the same verdict every call → `judgetest.Fixed(v)`
- you need verdicts in order → `judgetest.Scripted(v1, v2, ...)`; running out returns `ErrScriptExhausted`
- you need a batch-capable fake → `judgetest.NewBatch(f)`
- you build valid answers quickly → `judgetest.Choice(probs, order...)`, `judgetest.Score(probs, n)`, `judgetest.Abstain()`
- you assert what was asked → `fake.Calls()`, `batch.Batches()`

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`judgetest.go`](judgetest.go) | `Fake`, `BatchFake`, answer builders | you need a new fake behaviour |
