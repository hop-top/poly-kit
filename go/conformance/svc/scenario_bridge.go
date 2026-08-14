// scenario_bridge.go binds svc to the scenario library. The grading
// contract — captures, judge surface, result envelope — is defined by
// hop.top/kit/go/conformance/scenario and re-exported here as type
// aliases so the service layer and the library cannot drift.
//
// The one svc-owned shape is Scenario: the store-level envelope that
// carries the parsed scenario document plus its ref coordinates
// (namespace / id / version live in the store layout, not the DSL).

package svc

import (
	"context"
	"time"

	"hop.top/kit/go/conformance/scenario"
	"hop.top/kit/go/conformance/scenario/judge"
)

// ScenarioGrader is the seam svc relies on to grade an uploaded
// cassette against a loaded scenario. LibGrader is the production
// implementation; tests substitute fakes.
type ScenarioGrader interface {
	Grade(ctx context.Context, in GradeInput) (*Result, error)
}

// GradeInput carries everything Grade needs. StepCaptures and Judge
// use the scenario library's types directly.
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
// prompt_ref (e.g. "prompts/launch-dry-run.md").
type JudgePromptResolver = judge.PromptResolver

// Scenario is the store-level envelope for one scenario version. Doc
// is the parsed + validated DSL document the grader consumes; Raw is
// the original YAML (audit-only).
type Scenario struct {
	SchemaVersion string
	Namespace     string
	ID            string
	Version       string
	Tier          int
	Doc           *scenario.Scenario
	Raw           []byte
}

// Capture is a per-step recorded outcome, as the scenario library
// defines it.
type Capture = scenario.Capture

// AIJudge is the contract svc supplies to scenario.Grade for
// AI-judged scenarios.
type AIJudge = judge.AIJudge

// JudgeRequest carries a single judge invocation.
type JudgeRequest = judge.Request

// JudgeResponse carries one judge result.
type JudgeResponse = judge.Response

// Result is the grader output envelope.
type Result = scenario.Result

// AssertionResult is one entry in the Tier-3 trace.
type AssertionResult = scenario.AssertionResult

// FactorFacet is the per-factor rollup surfaced at Tier 2.
type FactorFacet = scenario.FactorFacet

// JudgeTrace records one AIJudge invocation (Tier 3 only).
type JudgeTrace = scenario.JudgeTrace
