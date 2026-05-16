# hay

Generic fuzzy resolution for CLI tools. Matches user input against
a corpus using pluggable scoring, staged lookup, and configurable
ambiguity handling. Zero external deps; stdlib only.

Author: $USER

## Quick Start

Basic resolve with string slice:

```
corpus := []string{"production", "preview", "staging"}
result, err := hay.Resolve("prod", corpus, hay.Options[string]{
    Score: hay.StringScore(identity, hay.Combined),
})
// result.Winner == "production"
```

Staged lookup (exact then fuzzy):

```
stages := []hay.Stage[Entry]{
    {Name: "exact",  Lookup: exactLookup},
    {Name: "fuzzy",  Lookup: fuzzyLookup},
}
result, err := hay.ResolveStaged(query, stages, opts)
```

Policy configuration (fail on ambiguity):

```
opts := hay.Options[string]{
    Score:     hay.StringScore(identity, hay.Combined),
    Policy:   hay.Policy{Action: hay.ActionList, Fail: true},
    TieMargin: 2,
}
```

## API Reference

### Functions

| Function | Signature | Purpose |
|----------|-----------|---------|
| `Resolve` | ``Resolve[T]`(query, corpus, Options[T])` | Score + rank corpus; return best match |
| `ResolveStaged` | ``ResolveStaged[T]`(query, []Stage[T], Options[T])` | Try stages in order; first non-empty wins |

### Types

| Type | Description |
|------|-------------|
| `Options[T]` | Config: scorer, stale filter, policy, tie margin, max candidates, bonus |
| `Policy` | Ambiguity behavior: `Action` (list/pick) + `Fail` (bool) |
| `Result[T]` | Winner, scored candidates, stale items, ambiguity flag |
| `Scored[T]` | Item + score pair |
| `ScoreFn[T]` | `func(query string, item T) int` — scoring function |
| `StaleFn[T]` | `func(item T) bool` — filter stale items pre-scoring |
| `BonusFn[T]` | `func(item T) int` — additive bonus post-score |
| `Stage[T]` | Named lookup stage: `Name` + `Lookup func(query) []T` |

### Errors

| Error | When |
|-------|------|
| `ErrAmbiguous[T]` | Multiple close matches + `Policy{ActionList, Fail: true}` |
| `ErrNoMatch` | Zero candidates score > 0 (reports stale count) |
| `ErrVanished` | Matched file disappeared during lookup |

## Scorers

| Scorer | Algorithm | Best For |
|--------|-----------|----------|
| `Subsequence` | Ordered char match; start + boundary bonuses | Abbreviated input (`prd` -> `production`) |
| `Substring` | Contiguous match; prefix + boundary bonuses | Partial names (`stag` -> `staging`) |
| `Levenshtein` | Edit distance inverted; zero if dist > query len | Typo tolerance (`stagin` -> `staging`) |
| `Combined` | `max(Subsequence, Substring)` | Default choice; covers most cases |
| `StringScore` | Wraps any `func(q,c string) int` for `ScoreFn[T]` | Adapt string scorers to typed corpora |

## Ambiguity Policy Matrix

Behavior when multiple candidates score within `TieMargin`:

| Action | Fail | error | Result.Ambiguous | Behavior |
|--------|------|-------|------------------|----------|
| list | true | `ErrAmbiguous` | n/a | Hard fail; caller shows candidates |
| list | false | nil | true | Soft signal; caller decides |
| pick | true | nil | true | Pick top + flag ambiguity |
| pick | false | nil | false | Pick top silently |

## hay stack (debug CLI)

Pipe-friendly scorer debugger. Reads corpus from stdin, scores
against query, prints results.

```
echo -e "path/one
path/two" | go run ./hay/stack "query"
cat paths.txt | go run ./hay/stack -e "idea/tlc"
```

### Flags

| Flag | Short | Default | Purpose |
|------|-------|---------|---------|
| `--explain` | `-e` | false | Per-scorer breakdown (sub-seq, sub-str, lev) |
| `--scorer` | `-s` | combined | Scorer: combined, subsequence, substring, levenshtein |
| `--margin` | `-m` | 0 | Tie margin for ambiguity detection |
| `--max` | `-n` | 20 | Max results to display |
| `--policy` | `-p` | list-fail | Policy: list-fail, list-ok, pick-fail, pick-ok |

Flags MUST come before the query (stdlib `flag` convention).
