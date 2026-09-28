# kit/redact — Performance budgets + baseline

Last measured: 2026-09-28 (baseline 2026-05-01)
Hardware: Apple M1 Pro (10-core), darwin/arm64, Go 1.26.1
Workload: `go test -bench=. -benchtime=1s -benchmem ./go/core/redact/`

## Budgets (from plan)

| Benchmark               | Target          | Notes                                        |
|-------------------------|-----------------|----------------------------------------------|
| BenchmarkApplyClean     | < 50µs / op     | 4KB log line, 0 secrets, ~250-rule policy    |
| BenchmarkApplyDirty     | < 100µs / op    | 4KB log line, 5 secrets sprinkled            |
| BenchmarkApplyLargePayload | < 20ms / op  | 1MB JSON-shape, 20 secrets                   |
| BenchmarkRuleAdd        | < 1ms / op      | regexp.Compile-bound                         |

## Measured

`before` is the 2026-05-01 baseline (every rule on every input);
`after` adds the literal prefilter (2026-09-28, M1 Pro, Go 1.26, with
the machine under load: best of 3 runs).

| Benchmark                  | before ns/op   | after ns/op | after B/op | allocs | vs target |
|----------------------------|---------------:|------------:|-----------:|-------:|-----------|
| BenchmarkApplyClean-10     |     43,936,812 |   1,584,421 |     60,037 |     22 | 32× over  |
| BenchmarkApplyDirty-10     |     47,696,023 |   3,159,039 |    126,656 |     46 | 32× over  |
| BenchmarkApplyLargePayload | 11,458,387,334 | 593,982,729 | 27,389,120 |    283 | 30× over  |
| BenchmarkRuleAdd-10        |          1,715 |      28,150 |     64,472 |    373 | 35× under |
| BenchmarkScan-10           |     54,716,500 |   2,513,560 |      2,968 |     14 | n/a       |

The audit hot path (`go/transport/cmdsurface`,
`BenchmarkSinkSetEmit_Redacted`: one invocation record, ~12 short
fields, secrets in most) went from 2.6ms to 45µs with output and
27µs output-blind, against a 100µs target. A short field costs a few
microseconds.

`BenchmarkRuleAdd` grew because adding a rule now also derives its
screen (a parse of the pattern) and the first rule allocates the
Redactor's literal index (~60KB). Rules are added once; still far
under budget.

## Diagnosis

Every Apply ran ~220 gitleaks regexes and 11 PII regexes in sequence,
each scanning the entire input. RE2 is linear per regex; the policy
size dominated wall-clock.

The prefilter (`prefilter.go`) removes rules that cannot match before
any regex runs:

- **Literal clauses.** From each rule's syntax tree, sets of literals
  such that every match contains a member of each set (for
  generic-api-key: a keyword, and an assignment operator). The input
  is scanned once, literals indexed by their first two ASCII-folded
  bytes; a rule runs only when every clause has a hit. Derived from
  the regex, not from gitleaks `keywords`: keywords are a heuristic
  upstream, and a rule can match without one.
- **Minimum match length.** A rule does not run on an input shorter
  than its shortest match.

Both are sound by construction (case folding, non-ASCII fold partners
such as the Kelvin sign, and U+FFFD matching invalid UTF-8 are
handled) and tested against the rules alone: generated matches for
every default rule, mixed documents compared through Apply,
ApplyBytes, Scan, observers and Stats, and a fuzz target
(`FuzzPrefilterSound`). 231 of 232 default rules get a literal screen;
sourcegraph-access-token (a bare 40-hex-digit alternative) runs on
every input of 40 bytes or more.

What remains on 4KB inputs is regex time for rules whose literals do
occur: the benchmark line contains `api`, `=` and digits, so
generic-api-key and the digit-based PII rules run over all 4KB.

## Optimization follow-ups

1. ~~Literal pre-screen~~ — done, as above (a two-byte index rather
   than Aho-Corasick; one pass per screening).
2. ~~Rule sharding by first chars~~ — done, it is the index.
3. **Windowed evaluation.** For rules whose matches have a bounded
   width, run the regex only on windows around literal hits instead
   of the whole input. The fix for the remaining 4KB cost.
4. **Byte-class run screen.** "Needs a run of 40 hex digits" would
   screen sourcegraph-access-token, the one rule without a literal.
5. **Single-pass matcher.** Combine all rule regexes into one
   alternation; the obstacle is attributing a match back to its rule.

Guidance until (3) lands:

- Audit records, short log lines, error messages: fine, microseconds.
- Heavyweight egress (full LLM responses, telemetry batches): fine,
  about 0.4 to 0.6ms per KB.
- Multi-KB text per call on a latency-critical path: measure first.

## CI regression guard

Two tests run in the ordinary `go test` pass:

- `TestSinkSetEmit_RedactionBudget` (`go/transport/cmdsurface`)
  times the audit benchmark record: 7 rounds of 50 emits, the fastest
  round's mean per emit must stay under 500µs. That is 5× the 100µs
  target, for slow or busy CI runners, and still 5× under the 2.6ms
  cost without the prefilter, so losing the screen fails it. Skipped
  under `-race`.
- `TestPrefilter_DefaultCorpusIsScreened` (this package) fails when
  more than 2 default rules lack a literal screen. Deterministic: it
  catches a corpus update or analysis change that would erode the
  speedup before it shows up as time.

## How to re-run

```
go test -bench=. -benchtime=1s -benchmem -run=NONE ./go/core/redact/
go test -bench=SinkSetEmit -benchmem -run=NONE ./go/transport/cmdsurface/
go test -run 'Budget|CorpusIsScreened' -v ./go/transport/cmdsurface/ ./go/core/redact/
```

Numbers are sensitive to hardware, macOS power state and other load.
Re-measure on the canonical M1 Pro before bumping the baseline.
