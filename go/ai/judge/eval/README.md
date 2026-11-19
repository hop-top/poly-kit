# eval

## What it answers

Whether a candidate judge can stand in for another on the same inputs: it
replays a recorded set of cases through the candidate and scores it against
references (another judge's recorded verdicts, or human labels) with
agreement, coverage at confidence cut-offs, Brier score, ECE and score
error. Wrong package when you need one judge's output validated on its own
(`judge.ValidateVerdict`) or calibrated without a reference set
(`calibrate`).

## Use it when

- you keep a recorded day of cases → `eval.Save(w, entries)`, `eval.LoadFile(path)`; JSONL, one `{case, reference}` per line
- you record the incumbent's verdicts as the reference → `eval.Record(ctx, j, cases, parallel)`
- you replay a candidate against them → `eval.Compare(ctx, ref, cand, entries, eval.Options{})`; `ref` may be nil when every entry is recorded
- you read the result → `Report.Markdown()`, `Report.JSON()`, `Report.Questions[i]`
- you gate a swap → `ok, reasons := report.Pass(eval.Thresholds{AtConfidence: 0.9, MinAgreement: 0.95, MinCoverage: 0.8, MaxECE: 0.05})`
- you calibrate the candidate from the same run → `calibrate.Fit(report.Samples, opts)`

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`eval.go`](eval.go) | `Entry`, `Reference`, `Options`, `Compare`, `Record` | you change how references resolve or judges run |
| [`metrics.go`](metrics.go) | per-question accumulation: agreement, coverage, Brier, ECE, MAE, confusion | you change a metric |
| [`report.go`](report.go) | `Report`, `QuestionReport`, JSON and Markdown rendering | you change the output |
| [`gate.go`](gate.go) | `Thresholds`, `Report.Pass` | you change a gate rule |
| [`caseset.go`](caseset.go) | `Load`, `Save`, `LoadFile`, `SaveFile` | you change the file format |
