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
| `Kind` | `KindChoice`, `KindScore`, `KindYesNo`, `KindGrounding` (preview) |
| `Question` | `Name`, `Instructions`, `Kind`, `Options` (choice), `Anchors` (score), `Abstain` |
| `Case` | `State`, `Questions`, `Assets []llm.ContentPart` |
| `Answer` | `Value`, `Abstained`, `Score`, `Box`, `Probs`, `Confidence`, `Why`, `Source` |
| `Box` | `Top`, `Bottom`, `Left`, `Right` fractions of the image; `Valid()`, `IoU(o)` |
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

### Grounding (preview)

`KindGrounding` is visual grounding (referring-expression localization):
locate what `Question.Instructions` name ("the mug in `photo`") in an
image and answer with a bounding box. Jev calls it `box`.

- `Case.Assets` needs at least one image part (`llm.PartTypeImage`).
  Name an image with `Metadata[judge.AssetNameKey]` (`"name"`); unnamed
  images are `image`, or `image1`, `image2`, ... among several
  (`judge.ImageNames`). Names must be unique and not collide with `State`
  keys. `Instructions` must be set; no options or anchors.
- `Answer.Box` edges are fractions in [0,1]: `Top`, `Bottom` of the image
  height, `Left`, `Right` of its width, origin top-left, `Top < Bottom`,
  `Left < Right`. A located answer has `Value == ""`, no `Probs`, no
  `Score`.
- `Confidence` is the backend's confidence in the box; 0 when it gives none
  (unknown, not "certainly wrong"). It is not a probability distribution.
- With `Question.Abstain` the target may be absent: `Abstained`, `Value ==
  "abstain"`, `Box == nil`.
- `Labels(q)` is empty for grounding, `abstain` included; test `q.Abstain`.
- Backends: Jev; llmjudge `Verbalized` with a vision model. `Votes(n > 1)`
  and `Logprobs` refuse with `ErrCapabilityNotSupported`. Local vision
  models ground in their own coordinates; see the conventions below.

Box conventions by model family (what the model emits; llmjudge converts
to `Answer.Box` fractions):

| Model family | Native convention | `llmjudge.BoxFormat` |
|--------------|-------------------|----------------------|
| Jev, schema-following hosted models | fractions under a JSON schema | `Fractions` (default) |
| Qwen3-VL, Qwen2-VL | 0-1000 grid, `[x1, y1, x2, y2]`, origin top-left | `Grid1000` |
| Qwen2.5-VL | absolute pixels of its (resized) input, same array | `Pixels` |
| PaliGemma | `<locNNNN>` tokens, 0-1023, y first (`ymin xmin ymax xmax`) | none yet; parse the tokens yourself |

Recommend Qwen3-VL (`qwen3-vl:8b` on Ollama located a test rectangle at
IoU above 0.7 with `Grid1000`). Qwen2.5-VL is less accurate and its pixel
coordinates refer to the resized image, so `Pixels` drifts on large inputs.
- eval scores boxes by IoU; calibrate skips them (see below).

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
`Choice`, `Score`, `Abstain`, `Grounding`, `GroundingAbstain` produce answers
that pass `ValidateAnswer`.

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
abstained answers, grounding answers (a box, no distribution), and
questions without params pass through.

## llmjudge

Chat-model backend (`hop.top/kit/go/ai/judge/llmjudge`).

```go
j := llmjudge.New(client,
    llmjudge.Model("m"),
    llmjudge.Mode(llmjudge.Votes(5)),
    llmjudge.Temperature(0.8),
    llmjudge.Price(llm.Price{Input: 0.15, Output: 0.6}))
v, err := j.Judge(ctx, c)
```

`client` is any `llm.Completer` (`*llm.Client` qualifies). One structured
call per sample answers every question of the case. Reply schema, per
question name: `{value: enum of judge.Labels(q), confidence: [0,1], why}`.
Schema violations go through the llm package's repair loop (`MaxRepairs`);
still invalid returns `llm.ErrSchemaInvalid`.

| Mode | `Value` / `Confidence` | `Probs` | `Source` |
|------|------------------------|---------|----------|
| `Verbalized` (default) | model's value and stated confidence | none | `verbalized` |
| `Votes(n)`, n > 1 | most voted value; share of samples | share of samples per label | `votes` |
| `Logprobs` | argmax label; its probability | full distribution from top_logprobs | `logprobs` |

- Score kind: `value` is the anchor index. `Score` = `ScoreFromIndex` when
  verbalized, `ScoreEV` over vote shares; 0 on abstain.
- Votes run sequentially; any failed sample fails the whole case. Ties go
  to the label earlier in `judge.Labels` order.
- Temperature: provider default for `Verbalized`; 0.7 for votes unless
  `Temperature` is set (0 allowed).
- `Pattern(p)` replaces the built-in system message with `p.System`; the
  reply schema and the case rendering stay.
- `Price(p)` sets `Verdict.Cost.USD` from the summed `Usage`; `Cost.Tokens`
  is always set.
- `Verdict.Model` is the configured `Model`; responses carry no model name.
  `Verdict.Usage` sums every sample and repair; `Backend` is `"llm"`.
- `Case.Assets` ride on the user message as extra parts.
- Grounding (preview): `Verbalized` only, with a vision model that takes
  image parts and structured output. Reply per question:
  `{box: {top, bottom, left, right}, confidence, why}`, plus a required
  `abstain` boolean when `Question.Abstain` (box ignored when true).
  Each image follows a text part "Image `name`:" so instructions can point
  at it. `Source` is `verbalized`, `Value` empty, no `Probs`. An invalid
  box fails with `judge.ErrInvalid`. `Votes(n > 1)` and `Logprobs` refuse
  with `ErrCapabilityNotSupported`. Small vision models often miss the
  fractions convention (a 3B model answered 0.5 for every edge), so check
  `Box.IoU` against known cases before relying on one.
- `BoxFormat(Grid1000 | Pixels)` (`BoxFormatFor(model)` picks by name
  prefix: `qwen3-vl`, `qwen2-vl` to `Grid1000`; `qwen2.5vl`, `qwen2.5-vl`
  to `Pixels`; else `Fractions`) asks in free text, with no response
  schema, because a schema `format` breaks local VLM grounding: `{"bbox_2d":
  [x1, y1, x2, y2]}` plus `confidence` and `why` (and `{"abstain": true}`
  when allowed). Every question of the case must be grounding (else
  `ErrCapabilityNotSupported`); one call per question. The parse is
  lenient: Markdown fence or prose around the JSON, an array of objects
  (first box wins), numbers as strings, a string-encoded array, or any key
  holding four numbers. `Grid1000` divides by 1000; `Pixels` divides by the
  PNG or JPEG's decoded size and needs exactly one image. The box is
  clamped to [0,1] and reordered (min before max per axis). No box in the
  reply: sent back with a corrective message up to `MaxRepairs` (default 2),
  then `llm.ErrSchemaInvalid`. A box with no area, or covering the whole
  image (a model fallback), is not repaired: `judge.ErrInvalid`, so a
  genuinely full-image target needs `Fractions`.
- Other kinds fail with
  `llmerrors.ErrCapabilityNotSupported`; malformed cases with
  `judge.ErrInvalid`. Every answer passes `judge.ValidateAnswer`.
- Not a `BatchJudge`; use `judge.Batch` for concurrent fan-out.
- Stated confidence is uncalibrated; verbalized answers carry no `Probs`,
  so `calibrate.Calibrated` passes them through. Use `Votes` to calibrate.

### Logprobs mode

`llmjudge.Mode(llmjudge.Logprobs)` reads probabilities off the model's
token log-probabilities instead of asking for a stated confidence.

- Labels become single-token keys under an enum schema: `A`, `B`, ... for
  choice options and score anchors in order; `Y` / `N` for yes/no; `X` for
  abstain. The prompt shows `key: label`; `Answer.Probs` maps keys back to
  labels.
- The request sets `Logprobs{TopK: llm.MaxTopLogprobs}` (20, the cap every
  kit adapter and Ollama enforce). Probability per label = softmax over the
  keys' log-probs found in the answer token's top-k (every token spelling of
  a key adds up, the sampled token counts even if absent from the list),
  renormalised.
- A key missing from the top-k gets `llmjudge.LogprobFloor` (1e-6) before
  renormalising: tiny, never 0, so the distribution stays valid and
  `calibrate` can still scale it.
- `Value` = argmax, `Confidence` = `Probs[Value]`, `Why` empty. Score kind:
  `Score` = `ScoreEV` over `Probs`; 0 on abstain.
- Answer token: the token holding the first character of the reply's last
  `"answer"` string value (last, so a reasoning model's quoted JSON in its
  thinking tokens is skipped), trimmed of quotes, braces, colons and
  whitespace, must equal a key. Otherwise the call fails with an error that
  says repair is not applicable.
- One call per question, not one per case: with several questions in one
  reply each has its own value position, and a mislocated token would
  silently skew another question's distribution. `Usage` and `Cost` sum
  the calls.
- No repair loop (`MaxRepairs` is ignored): logprobs describe one reply.
  Schema-invalid output returns `llm.ErrSchemaInvalid`.
- More than 20 labels (abstain included) in one question fails with
  `llmerrors.ErrCapabilityNotSupported`, never truncated; so does a
  completer that does not return logprobs.
- Needs a `llm.LogprobCompleter` (OpenAI-compatible, Ollama) and a server
  that applies structured output. Calibrate after the fact:
  `calibrate.Calibrated(j, params)`; nothing is calibrated inside llmjudge.
- Test with `llmxrr` cassettes; the package replays a real Ollama exchange.

## Jev

TypeSafe's Jev model through OpenRouter (`hop.top/kit/go/ai/judge/jev`).
Always cloud: every `Judge` call sends the case's state and questions to
the service. Needs an OpenRouter key.

```go
j, err := jev.New() // key: llm.yaml providers.openrouter, OPENROUTER_API_KEY, LLM_API_KEY
if err != nil { /* llm.ErrMissingKey, llm.ErrKeyEndpoint */ }
v, err := j.Judge(ctx, c)
```

Options: `WithModel` (default `typesafe/jev-1.14`), `WithURL` (default
`https://openrouter.ai/api/v1/systemone`), `WithAPIKey`, `WithTransport`
(any `http.RoundTripper`, as `llm.WithTransport`), `Hermetic`.

Key resolution: `WithAPIKey` first; else llm.yaml `providers.openrouter`
(`api_key`, then the variable `api_key_env` names), then
`OPENROUTER_API_KEY`, then `LLM_API_KEY`; none fails with
`llm.ErrMissingKey`. The key goes only to the default endpoint's origin,
loopback, or an origin llm.yaml `base_url` / `LLM_BASE_URL` names; any
other fails with `llm.ErrKeyEndpoint`. `Hermetic` reads neither the
environment nor llm.yaml: it requires `WithAPIKey` and trusts only the
default origin and loopback. The package registers no llm adapter and
links no provider SDK, so a tool that passes its own keys can import it.

- One request answers every question. Mapping: choice to
  `{type: choice, criteria: {value: description}}` (a bare value stands in
  for a missing description); score to `{type: score, criteria: anchors}`,
  at most 10 anchors; yes/no to `{type: noul}`.
- Answers are native: `Source` is `"native"`, `Why` empty. Choice and score
  `probabilities` become `Answer.Probs` (score keys are anchor indexes),
  renormalised since the service rounds; `Value` is the argmax and
  `Confidence` is `Probs[Value]`, not the service's own confidence figure.
  Score kind: `Score` is `judge.ScoreEV`. Yes/no: the service returns only
  P(yes); `Probs` is `{yes: p, no: 1-p}`, `Value` the likelier side (yes on
  a tie). Jev never abstains.
- With empty `probabilities` the reported choice or score (rounded to an
  anchor index) stands, with the reported confidence and no `Probs`.
- `usage.cost` becomes `Verdict.Cost.USD`; `usage.input_tokens` and
  `output_tokens` become `Verdict.Usage`, their sum `Cost.Tokens`.
  `Backend` is `"jev"`, `Model` the model the service names.
- Grounding (preview; Jev's `box`) maps to `{type: box, instructions}`,
  no criteria. Each image in `Case.Assets` (PNG or JPEG, at most 20 MiB)
  goes base64 under `assets` as `{type: image, format: png|jpg,
  data_base64}`, keyed `asset-<name>` and referenced from `state[<name>]`
  (see `judge.ImageNames`); the request then carries
  `TypeSafe-Danger-Zone-Images: true`, only when images are present. Any
  question kind may ride with an image. Answer `{type: box, box: {top,
  bottom, left, right}, confidence}`: edges are fractions of the image,
  `confidence` null or absent is 0, no probabilities. A null or missing box
  is an abstention when `Question.Abstain`, else `jev.ErrMalformed`. The
  service marks images and box preview.
- Other asset types, other image formats and unknown kinds fail with
  `llmerrors.ErrCapabilityNotSupported` before anything is sent.
- The key goes only to the default endpoint's origin, a loopback host, or an
  origin llm.yaml `base_url` / `LLM_BASE_URL` names; `New` fails with
  `llm.ErrKeyEndpoint` otherwise. Redirects are not followed.
- HTTP failures map to llm errors: 401 `ErrAuth`, 403 `ErrPermissionDenied`,
  404 `ErrModel`, 429 `ErrRateLimit` (Retry-After seconds), 5xx
  `HTTPStatusError`. No retry inside the backend. A response that does not
  answer the case fails with `jev.ErrMalformed`. Every answer passes
  `judge.ValidateAnswer`.
- Not a `BatchJudge`; use `judge.Batch`.
- Tests: hermetic fake `http.RoundTripper` over the documented shapes; no
  live call, no cassette.

## Limitations

- Backends live in their own packages; the Jev backend is cloud only, and its image and box support is preview.
- Grounding needs an image in `Case.Assets`; an `eval` case-set file (JSONL) cannot carry image assets, so grounding sets live in code.
- Calibration needs labeled samples; Platt is binary only.

## eval

`hop.top/kit/go/ai/judge/eval` replays a recorded set of cases through a
candidate judge and scores it against references: the gate for swapping one
judge for another (for example a cloud judge for a local model).

- Case set: JSONL, one `eval.Entry` per line,
  `{"case": judge.Case, "reference": {"verdict": ..., "labels": {question: label}, "boxes": {question: box}}}`.
  `eval.Load` / `eval.Save` / `LoadFile` / `SaveFile`; unknown fields fail,
  errors name the line. `eval.Record(ctx, j, cases, parallel)` builds a set
  whose reference is `j`'s verdicts.
- `eval.Compare(ctx, ref, cand, entries, eval.Options{Parallel, Thresholds, Bins, IoU})`.
  An entry's recorded `reference` wins; entries without one are judged by
  `ref` (nil `ref` plus an unrecorded entry is `eval.ErrNoReference`). In a
  reference, `labels` override `verdict` answers per question; label-only
  references may skip questions. Both judges run through `judge.Batch`.
  Reference and candidate must answer exactly the case's questions, else
  `eval.ErrMismatch`; a question name must keep one kind across the set.
- Per question: `N`; `Agreement` = exact `Value` match (abstain matches only
  abstain); `RefAbstain`, `CandAbstain`; `AgreementAnswered` over cases
  neither side abstained on; `Thresholds` (default 0.5 0.7 0.8 0.9 0.95):
  coverage and agreement keeping only answers with candidate
  `Confidence >= t`, abstentions never covered; `Brier` (multi-class, over
  the question's labels) and `ECE` (top-label confidence vs accuracy, `Bins`
  default 10) from answers carrying `Probs`, with the reference label as
  truth; `ScoreMAE` of `Score` for score questions (a label `"2"` scales as
  `ScoreFromIndex`); `Confusion[ref][cand]`.
- Grounding questions (preview): a match is both abstaining, or both boxed
  with IoU >= `Options.IoU` (default 0.5, `eval.DefaultIoU`). The reference
  is a recorded verdict, `boxes: {question: box}`, or label `abstain` when
  allowed. `NIoU` and `MeanIoU` cover cases with a box on both sides. No
  `Probs`: no Brier, ECE, confusion or calibration samples; `Confidence` 0
  (unknown) is never covered by a threshold above 0.
- `Report.Markdown()`, `Report.JSON()`.
- `Report.Pass(eval.Thresholds{...})` returns `(ok, reasons)`; zero fields
  are unchecked. `MinAgreement` and `MinCoverage` read at `AtConfidence`
  (must be one of the report's thresholds; 0 = overall agreement and the
  share not abstained). Every question must pass; no questions fails.
- `Report.Samples` are `calibrate.Sample`s (candidate `Probs`, reference
  label), so `calibrate.Fit(report.Samples, ...)` calibrates the candidate
  from the same run. A sample whose true label has probability 0 (confident
  wrong) is kept: `Fit` floors probabilities at 1e-12; NaN, negative, or
  non-normalised probs are still rejected.
