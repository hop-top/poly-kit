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
- Kinds other than choice, score, yes/no fail with
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
j, err := jev.New() // key via llm.SecretFor("openrouter"): OPENROUTER_API_KEY
if err != nil { /* llm.ErrMissingKey, llm.ErrKeyEndpoint */ }
v, err := j.Judge(ctx, c)
```

Options: `WithModel` (default `typesafe/jev-1.14`), `WithURL` (default
`https://openrouter.ai/api/v1/systemone`), `WithAPIKey`, `WithTransport`
(any `http.RoundTripper`, as `llm.WithTransport`).

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
- Box (image localization) questions and `Case.Assets` fail with
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

- Backends live in their own packages; the Jev backend is cloud only and has no box or image support.
- Calibration needs labeled samples; Platt is binary only.
