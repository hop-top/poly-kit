# kit/redact — Performance budgets + baseline

Last measured: 2026-05-01
Hardware: Apple M1 Pro (10-core), darwin/arm64, Go 1.26.1
Workload: `go test -bench=. -benchtime=1s -benchmem ./go/core/redact/`

## Budgets (from plan)

| Benchmark               | Target          | Notes                                        |
|-------------------------|-----------------|----------------------------------------------|
| BenchmarkApplyClean     | < 50µs / op     | 4KB log line, 0 secrets, ~250-rule policy    |
| BenchmarkApplyDirty     | < 100µs / op    | 4KB log line, 5 secrets sprinkled            |
| BenchmarkApplyLargePayload | < 20ms / op  | 1MB JSON-shape, 20 secrets                   |
| BenchmarkRuleAdd        | < 1ms / op      | regexp.Compile-bound                         |

## Baseline (M1 Pro)

| Benchmark                  | ns/op       | B/op        | allocs/op | vs target |
|----------------------------|------------|-------------|-----------|-----------|
| BenchmarkApplyClean-10     | 43,936,812 |   2,006,252 |       795 | 880× over |
| BenchmarkApplyDirty-10     | 47,696,023 |   2,415,773 |     1,002 | 477× over |
| BenchmarkApplyLargePayload | 11,458,387,334 | 494,622,112 | 6,315 | 573× over |
| BenchmarkRuleAdd-10        |      1,715 |       4,616 |        33 | 583× under (good) |
| BenchmarkScan-10           | 54,716,500 |      26,941 |        69 | n/a       |

## Diagnosis

The clean-payload Apply is ~44ms — three orders of magnitude over the 50µs
budget. Cause is structural, not algorithmic: every Apply runs `~211
gitleaks regexes + 11 PII regexes` via `regexp.ReplaceAllStringFunc` in
sequence. Each regex scans the entire 4KB input. RE2 is linear-time per
regex but the policy size dominates wall-clock.

`BenchmarkScan` confirms the same shape (54ms — slightly worse because it
calls FindAllStringIndex, which allocates per match position).

`BenchmarkRuleAdd` is comfortably under budget (1.7µs vs 1ms target).

## Optimization follow-ups (not in this track)

The plan named these explicitly as "consider only if budget missed".
Budget is missed by 2-3 orders of magnitude — they are now mandatory for
production use. Filed as out-of-scope follow-ups (track here, future
work):

1. **Aho-Corasick literal pre-screen.** Build a single combined
   literal-prefix matcher across all rules (extract literal prefixes from
   each compiled regex's `Prog`, or from the gitleaks `keywords` field
   — kit/redact would need to ingest that field, currently dropped).
   Skip regex evaluation for rules whose literal prefix is absent in
   the input. Estimated speedup: 50-100× on clean payloads (most rules
   short-circuit).
2. **Rule sharding by likely first-3 chars.** Bucket rules by the
   literal prefix of their pattern; only run the bucket that overlaps
   with input substrings. Pure Go, no deps. Estimated speedup: 5-10×
   when combined with (1).
3. **Single-pass matcher.** Combine all rule regexes into one
   alternation and run one pass. RE2 supports this efficiently;
   kicker is matching the result back to the originating rule for
   observer/Stats attribution. Estimated speedup: 10-30×.

Until these land, kit/redact is **NOT safe for use in hot egress paths
(per-log-line, per-LLM-token)**. Recommended interim usage:

- Audit-mode (Scan + report at end of session): yes, fine.
- Wrap heavyweight egress (full LLM responses, error reports, telemetry
  batches): yes, the per-payload cost is amortised.
- Per-log-line via slog handler: NO, will add ~50ms per log call.

## CI regression guard

The benchmarks above run on every CI build. Any benchmark whose ns/op
regresses by >20% versus the committed baseline (this file) should
block merge. CI integration via `golang.org/x/perf/cmd/benchstat` or
hand-rolled regex extraction is a follow-up.

## How to re-run

```
go test -bench=. -benchtime=1s -benchmem -run=NONE ./go/core/redact/
```

Numbers are sensitive to hardware + macOS power state. Re-measure on
the canonical M1 Pro before bumping the baseline.
