package scenario

import "hop.top/kit/go/conformance/scenario/judge"

// Capture is one step's recorded output. The grader receives a map
// of step ID to Capture from the caller (typically the svc
// service after the adopter has uploaded their cassette bundle).
//
// CassetteDir is relative to Input.CassetteRoot; the grader resolves
// absolute paths internally so verbs that re-read cassettes get a
// clean filesystem location.
type Capture struct {
	ExitCode    int
	Stdout      []byte
	Stderr      []byte
	DurationMS  int64
	CassetteDir string
}

// Input is the bundle a grader call needs. Scenario is the parsed
// (and validated) DSL; StoryContent is the raw bytes of the
// referenced story file for the SHA-256 hash check; StepCaptures
// is keyed by Step.ID.
//
// Judge is consulted only when a scenario contains
// judge_score_above assertions; nil is safe when no judges are
// required. JudgePromptResolver is consulted only when a JudgeBlock
// declares prompt_ref instead of inline prompt.
type Input struct {
	Scenario            *Scenario
	StoryContent        []byte
	CassetteRoot        string
	StepCaptures        map[string]Capture
	Judge               judge.AIJudge
	JudgePromptResolver judge.PromptResolver
}

// Env is the per-evaluation context passed to each verb's Eval
// function. It bundles the scenario, the captured run, and the
// resolved judge/prompt machinery so verbs don't need to walk back
// to the Input.
//
// Env is shared across all verb invocations in one Grade call;
// verbs treat it as read-only.
type Env struct {
	Scenario     *Scenario
	StepCaptures map[string]Capture
	CassetteRoot string
	Judge        judge.AIJudge
	PromptRes    judge.PromptResolver

	// JudgeTraces accumulates traces from judge verbs. The grader
	// owns this slice; verbs append.
	JudgeTraces []JudgeTrace
}

// CaptureFor returns the capture for the named step, or false if no
// such capture was recorded. Verbs use this to drive their on-step
// check.
func (e *Env) CaptureFor(stepID string) (Capture, bool) {
	if e == nil || e.StepCaptures == nil {
		return Capture{}, false
	}
	c, ok := e.StepCaptures[stepID]
	return c, ok
}

// FindJudge returns the JudgeBlock with the given ID, or nil if no
// such block is declared. Used by judge_score_above evaluators.
func (e *Env) FindJudge(id string) *JudgeBlock {
	if e == nil || e.Scenario == nil {
		return nil
	}
	for i := range e.Scenario.Judges {
		if e.Scenario.Judges[i].ID == id {
			return &e.Scenario.Judges[i]
		}
	}
	return nil
}
