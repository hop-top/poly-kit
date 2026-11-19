# Judge Reference

> Typed judgments over a Case: questions in, probability-bearing answers
> out (`hop.top/kit/go/ai/judge`). Core types, validation, batching, a
> fake, and calibration math. Backends live elsewhere.

## Who this is for

Go authors who need a model (or rule engine) to answer typed questions
about some state: route this, score that, yes/no with abstain. Code
consumes `Answer.Value`, `Probs`, `Confidence`, `Score`; it never parses
model text.

## Types

| Type | Role |
|------|------|
| `Kind` | `KindChoice`, `KindScore`, `KindYesNo` |
| `Question` | `Name`, `Instructions`, `Kind`, `Options` (choice), `Anchors` (score), `Abstain` |
| `Case` | `State`, `Questions`, `Assets []llm.ContentPart` |
| `Answer` | `Value`, `Abstained`, `Score`, `Probs`, `Confidence`, `Why`, `Source` |
| `Verdict` | `Answers` by question name, `Usage`, `Cost`, `Backend`, `Model` |
| `Judge` | `Judge(ctx, Case) (Verdict, error)` |
| `BatchJudge` | optional: `JudgeBatch(ctx, []Case) ([]Verdict, error)` |

## Labels and rules

| Kind | Labels |
|------|--------|
| choice | `Option.Value` (>= 2, unique) |
| yesno | `yes`, `no` |
| score | anchor indices `"0".."n-1"`, anchors ordered low to high (>= 2) |

- `abstain` is a label only when `Question.Abstain`; `Abstained` iff
  `Value == "abstain"`. `"abstain"` is reserved as an option value.
- `Probs` (when set): keys are labels, each in [0,1], sum 1 within
  `ProbEpsilon` (1e-6).
- `Confidence` = `Probs[Value]` when `Probs` is set.
- `Score` (score kind): expected anchor index over `Probs`, scaled to
  [0,1] by `n-1`; 0 when abstained. `ScoreEV` excludes abstain mass and
  divides the rest by `1-P(abstain)`.

Validators: `Validate(Case)`, `ValidateAnswer(Question, Answer)`,
`ValidateVerdict(Case, Verdict)`. All wrap `ErrInvalid` and join every
problem.

## Batch

`Batch(ctx, j, cases, parallel)`: validates all cases first; uses
`JudgeBatch` when `j` implements it (one call, `parallel` ignored);
otherwise up to `parallel` (min 1) concurrent `Judge` calls. Results keep
input order. First error cancels the rest; no partial results. Cancelled
ctx returns `ctx.Err()`.

## judgetest

`Fixed(v)`, `Scripted(v...)`, or `Fake{Fn / Script}`; `Calls()` returns the
call log. `NewBatch(f)` adds `BatchJudge` and `Batches()`. Builders
`Choice`, `Score`, `Abstain` produce answers that pass `ValidateAnswer`.

## calibrate

Per-question post-hoc calibration (`hop.top/kit/go/ai/judge/calibrate`):

| Calibrator | Applies to | Form |
|------------|-----------|------|
| `Temperature{T}` | any label count | `softmax(log p / T)` |
| `Platt{A, B, Positive}` | binary (+abstain) | `sigmoid(A*logit(P(pos)) + B)` |

`Fit(samples, FitOptions)` fits each question from `Sample{Question, Probs,
Label}`; `FitTemperature`, `FitPlatt` fit one. `Params` is JSON
(`Marshal`, `Parse`):

```json
{"questions": {"label": {"temperature": {"t": 1.8}},
               "ok": {"platt": {"a": 0.6, "b": -0.4, "positive": "yes"}}}}
```

`Calibrated(j, params)` wraps a `Judge` (and `BatchJudge` when present):
`Probs` replaced, `Value` re-picked as argmax (ties keep the original),
`Confidence` = `P(Value)`, `Score` recomputed. Answers without `Probs`,
abstained answers, and questions without params pass through.

## Limitations

- No backend here; see the llm and jev backends.
- Calibration needs labeled samples; Platt is binary only.
