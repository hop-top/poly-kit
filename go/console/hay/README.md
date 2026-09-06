# hay

## What it answers

Which corpus item did the user mean by a partial, abbreviated or
misspelled name, and what to do when two items are equally close. Generic
fuzzy resolution with pluggable scoring, staged lookup and configurable
ambiguity handling; zero external deps, stdlib only. Wrong package when
you need shell completion candidates rather than resolution
(`hop.top/kit/go/console/cli`), or a scored table for debugging
(`stack/`, below).

## Use it when

- you resolve one name against a string slice → `hay.Resolve(query, corpus, hay.Options[string]{Score: hay.StringScore(identity, hay.Combined)})`
- you want exact matches to win before fuzzy ones → `hay.ResolveStaged(query, []hay.Stage[T]{...}, opts)`
- ties must fail loudly → `Policy: hay.Policy{Action: hay.ActionList, Fail: true}` plus `TieMargin`
- the corpus is typed, not strings → `hay.StringScore(keyFn, scorer)` adapts any string scorer to `ScoreFn[T]`
- typos matter more than abbreviations → `hay.Levenshtein`; otherwise `hay.Combined` (`max(Subsequence, Substring)`)

## Quick start

```go
corpus := []string{"production", "preview", "staging"}
result, err := hay.Resolve("prod", corpus, hay.Options[string]{
    Score: hay.StringScore(identity, hay.Combined),
})
// result.Winner == "production"
```

## Contract

- Candidates scoring within `TieMargin` of the top are ambiguous; the
  `Policy` decides: `list`+`Fail` returns `ErrAmbiguous[T]`, `list` alone
  sets `Result.Ambiguous`, `pick` returns the top (flagging ambiguity only
  when `Fail` is set). Full matrix:
  [Ambiguity policy matrix](../../../docs/adopters/reference/hay.md#ambiguity-policy-matrix).
- `ErrNoMatch` when zero candidates score above 0 (reports the stale
  count); `ErrVanished` when a matched file disappeared during lookup.
- `StaleFn` filters before scoring; `BonusFn` adds after scoring.
- `ResolveStaged` stops at the first stage returning a non-empty set.

## Neighbours

- [`stack/`](stack/README.md): `package main` scorer debugger (corpus on
  stdin, query as argument, `-e` for the per-scorer breakdown); not
  importable
- `hop.top/kit/go/console/cli`: the CLI factory whose commands call
  `Resolve` on user input

## See also

- [Hay API reference](../../../docs/adopters/reference/hay.md): functions,
  types, errors, scorer table, policy matrix, `hay stack` flags
