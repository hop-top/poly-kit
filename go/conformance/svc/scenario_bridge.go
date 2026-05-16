// scenario_bridge.go defines the local interface seam that bridges svc
// to the scenario library. The scenario library is being
// built in a parallel worktree; this file lets svc compile and test
// independently until scen lands and merges.
//
// When scen merges, the local stub types below are replaced with
// re-exports from hop.top/kit/go/conformance/scenario. Concretely:
//
//	type ScenarioGrader interface { ... } // stays, but parameters
//	                                       // reference scenario.Scenario,
//	                                       // scenario.Capture, etc.
//	type Scenario   = scenario.Scenario   // type alias
//	type Capture    = scenario.Capture
//	type AIJudge    = scenario.AIJudge
//	type Result     = scenario.Result
//	type GradeInput = scenario.Input
//
// And a single compile-time check is added in handler.go:
//
//	var _ ScenarioGrader = (*scenario.Grader)(nil)
//
// Until then, the in-package stubs below define the minimum shape the
// service needs to compile, test, and reason about types.

package svc

import (
	"context"
	"time"
)

// ScenarioGrader is the seam svc relies on to grade an uploaded
// cassette against a loaded scenario.
//
// This interface mirrors the contract pinned by the scenario design.
// When scen merges, scenario.Grader will satisfy this directly.
type ScenarioGrader interface {
	Grade(ctx context.Context, in GradeInput) (*Result, error)
}

// GradeInput carries everything Grade needs. Mirrors scenario.Input.
type GradeInput struct {
	Scenario       *Scenario
	StoryContent   []byte
	StepCaptures   map[string]Capture
	Judge          AIJudge
	PromptResolver JudgePromptResolver
	Tier           int
	// RequestedAt is set by the service for span/log correlation.
	RequestedAt time.Time
}

// JudgePromptResolver loads a prompt body keyed by a manifest-supplied
// prompt_ref (e.g. "prompts/launch-dry-run.md"). Mirrors
// scenario.JudgePromptResolver.
type JudgePromptResolver func(promptRef string) (string, error)

// Scenario is a stub that mirrors the shape scen.Scenario will carry.
// When scen merges this becomes a type alias.
type Scenario struct {
	SchemaVersion string
	Namespace     string
	ID            string
	Version       string
	Tier          int
	StoryRef      StoryRef
	Steps         []ScenarioStep
	Raw           []byte // raw YAML bytes (audit-only)
}

// StoryRef pins the story content.
type StoryRef struct {
	StoryID     string
	StoryPath   string
	ContentHash string // "sha256:<hex>"
}

// ScenarioStep is the per-step contract a scenario describes.
type ScenarioStep struct {
	ID         string
	BinaryArgs []string
}

// Capture is a per-step xrr cassette outcome.
type Capture struct {
	ExitCode    int
	DurationMS  int64
	Stdout      []byte
	Stderr      []byte
	CassetteDir string // unpacked cassette path on disk
}

// AIJudge is the contract svc supplies to scenario.Grade for
// AI-judged scenarios. Mirrors scenario.AIJudge.
type AIJudge interface {
	Score(ctx context.Context, req JudgeRequest) (JudgeResponse, error)
}

// JudgeRequest carries a single judge invocation.
type JudgeRequest struct {
	Model        string
	Prompt       string
	CapturedText string
	MaxTokens    int
}

// JudgeResponse carries one judge result.
type JudgeResponse struct {
	Verdict   string
	Score     float64
	Rationale string
	TokensIn  int
	TokensOut int
}

// Result is the grader output. Mirrors scenario.Result.
type Result struct {
	ScenarioID    string            `json:"scenario_id"`
	SchemaVersion string            `json:"schema_version"`
	Verdict       string            `json:"verdict"`
	Reason        string            `json:"reason,omitempty"`
	ScoredAt      time.Time         `json:"scored_at"`
	GraderVersion string            `json:"grader_version"`
	RulesVersion  string            `json:"rules_version,omitempty"`
	Tier          int               `json:"tier"`
	Facets        map[string]any    `json:"facets,omitempty"`
	Assertions    []AssertionResult `json:"assertions,omitempty"`
	JudgeTraces   []JudgeTrace      `json:"judge_traces,omitempty"`
}

// AssertionResult mirrors scenario.AssertionResult.
type AssertionResult struct {
	Verb     string `json:"verb"`
	Pass     bool   `json:"pass"`
	Observed any    `json:"observed,omitempty"`
}

// JudgeTrace mirrors scenario.JudgeTrace.
type JudgeTrace struct {
	Model     string  `json:"model"`
	Verdict   string  `json:"verdict"`
	Score     float64 `json:"score"`
	Rationale string  `json:"rationale,omitempty"`
}

// ToTier truncates the result envelope down to the requested tier. Tier
// 1 strips facets, assertions, judge traces; tier 2 keeps facets only;
// tier 3 keeps everything. Mirrors scenario.Result.ToTier(n).
//
// When scen merges this function delegates to the library's ToTier.
func (r *Result) ToTier(n int) *Result {
	if r == nil {
		return nil
	}
	out := *r
	out.Tier = n
	switch {
	case n <= 1:
		out.Facets = nil
		out.Assertions = nil
		out.JudgeTraces = nil
	case n == 2:
		out.Assertions = nil
		out.JudgeTraces = nil
	}
	return &out
}
