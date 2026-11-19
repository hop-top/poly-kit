// Package eval replays a recorded set of cases through a candidate judge and
// scores it against references: exact agreement (abstain handled
// explicitly), coverage and agreement above confidence cut-offs, Brier
// score, ECE, score MAE and confusion counts, per question.
//
// A case set is JSONL of Entry (a judge.Case and an optional Reference: a
// recorded verdict, per-question labels, or both). Compare runs the
// candidate (and a reference judge for entries without a recorded
// reference) through judge.Batch. Report renders as JSON or Markdown,
// Report.Pass gates it, and Report.Samples feed calibrate.Fit.
//
// It is the gate for swapping one judge for another: record the incumbent's
// verdicts once (Record), replay the candidate, require agreement and
// calibration to hold.
package eval
