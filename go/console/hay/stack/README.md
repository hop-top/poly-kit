# hay/stack

## What it answers

Why did `hay` rank these candidates this way for this query. A `package main`
debugger over `hop.top/kit/go/console/hay`: corpus on stdin, query as the
argument, scored table on stdout. Not importable. Resolution inside a tool
uses `hay.Resolve` from the parent package directly.

## Use it when

- a fuzzy match picked the wrong winner → pipe the same corpus through
  `go run ./go/console/hay/stack <query>` and read the scores
- you want the per-scorer breakdown → add `-e` (columns SUB-SEQ, SUB-STR, LEV)
- you want to compare scorers → `-s combined|subsequence|substring|levenshtein`
- you want to see ambiguity handling → `-m <margin>` with
  `-p list-fail|list-ok|pick-fail|pick-ok`
- you want fewer rows → `-n <max>` (default 20)

## Quick start

```sh
printf 'production\npreview\nstaging\n' | go run ./go/console/hay/stack -e stag
```

Prints:

```text
SCORE  SUB-SEQ  SUB-STR  LEV  PATH
11     6        11       4    staging
```

## Contract

- Flags come before the query (stdlib `flag` parsing).
- Empty stdin lines are dropped; no corpus at all exits 1.
- Missing query or unknown scorer exits 2.
- Under `list-fail` an ambiguous result still prints the candidate table,
  then exits 1.

## Neighbours

- `hop.top/kit/go/console/hay`: the library being debugged; scorers,
  `Options`, `Policy`, `Resolve`, `ResolveStaged`

## See also

- [hay README](../README.md), section "hay stack (debug CLI)"
