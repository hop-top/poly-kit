package verbs

import (
	"context"

	"hop.top/kit/go/conformance/scenario/judge"
)

// Status mirrors scenario.Status (declared here as the leaf type to
// avoid import cycles). Scenario translates verbs.Status 1:1 to its
// own type at result-assembly time.
type Status string

const (
	StatusPass           Status = "pass"
	StatusFail           Status = "fail"
	StatusNotImplemented Status = "not_implemented"
	StatusUngradable     Status = "ungradable"
)

// Capture is the per-step data a verb evaluator consumes. Mirrors
// scenario.Capture. Pure value type so verbs don't reach back into
// the scenario package.
type Capture struct {
	ExitCode    int
	Stdout      []byte
	Stderr      []byte
	DurationMS  int64
	CassetteDir string
}

// AssertionSpec is the verb-level view of one scenario assertion.
// The grader builds this from scenario.Assertion + the on-step's
// capture before calling Evaluate.
type AssertionSpec struct {
	ID     string
	Kind   string
	On     string
	Factor int
	Args   map[string]any
}

// JudgeBlockSpec mirrors scenario.JudgeBlock. Verbs that operate on
// judges (judge_score_above) consume this; the grader populates it
// from the resolved JudgeBlock + the assertion's judge_id.
type JudgeBlockSpec struct {
	ID             string
	On             string
	Prompt         string
	PromptRef      string
	Model          string
	ModelAllowlist []string
	RequiredScore  float64
}

// JudgeTraceOut is the verb's contribution to the result's judge
// trace list. The grader appends Evaluate's returned trace (if any)
// to the result before tier redaction.
type JudgeTraceOut struct {
	JudgeID     string
	AssertionID string
	Model       string
	Score       float64
	Rationale   string
	TokensIn    int
	TokensOut   int
}

// VerbContext is the read-only bundle every verb evaluator sees.
// Capture is the on-step's recorded data; OtherCaptures lets a verb
// reach sibling steps (cassette_diff_equals uses this).
type VerbContext struct {
	Capture       Capture
	OtherCaptures map[string]Capture
	CassetteRoot  string
	Judge         judge.AIJudge
	PromptRes     judge.PromptResolver
	JudgeBlock    *JudgeBlockSpec
}

// EvalResult is the per-verb outcome. Status drives the assertion's
// Status; Observed/Expected/Message surface in the Tier-3 trace.
// JudgeTrace is set only by judge_score_above and is appended to the
// result by the grader.
type EvalResult struct {
	Status     Status
	Observed   any
	Expected   any
	Message    string
	JudgeTrace *JudgeTraceOut
}

// Evaluator is the function shape every implemented verb conforms
// to. Pure: receives ctx + spec + verb context, returns a result.
// No I/O beyond reading the cassette dir if needed.
type Evaluator func(ctx context.Context, spec AssertionSpec, vctx VerbContext) EvalResult

// Pass is the canonical pass result. Verbs return Pass when they
// have nothing else to surface.
func Pass() EvalResult { return EvalResult{Status: StatusPass} }

// Fail is the canonical fail constructor.
func Fail(observed, expected any, msg string) EvalResult {
	return EvalResult{
		Status:   StatusFail,
		Observed: observed,
		Expected: expected,
		Message:  msg,
	}
}

// Ungradable is the canonical ungradable constructor.
func Ungradable(msg string) EvalResult {
	return EvalResult{Status: StatusUngradable, Message: msg}
}

// NotImplemented is the canonical not_implemented constructor (used
// only by auth_lifecycle_clean in v1).
func NotImplemented(msg string) EvalResult {
	return EvalResult{Status: StatusNotImplemented, Message: msg}
}
