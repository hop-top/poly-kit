package svc

import (
	"context"
	"fmt"

	"hop.top/kit/go/conformance/scenario"
)

// LibGrader grades uploads through the scenario library. It is the
// production ScenarioGrader the serve command wires; every verdict it
// returns comes from scenario.Grade's assertion walk — nothing is
// synthesized at the service layer.
type LibGrader struct{}

// Grade converts the service-side GradeInput into scenario.Input and
// delegates. Hard grading failures (story hash mismatch, nil
// scenario) surface as the library's populated ungradable Result —
// the verdict is the wire truth — so the error return fires only when
// no result exists at all.
func (LibGrader) Grade(ctx context.Context, in GradeInput) (*Result, error) {
	if in.Scenario == nil || in.Scenario.Doc == nil {
		return nil, fmt.Errorf("%s: scenario document not loaded", CodeGraderInternal)
	}
	res, err := scenario.Grade(ctx, scenario.Input{
		Scenario:            in.Scenario.Doc,
		StoryContent:        in.StoryContent,
		StepCaptures:        in.StepCaptures,
		Judge:               in.Judge,
		JudgePromptResolver: in.PromptResolver,
	})
	if res == nil {
		return nil, err
	}
	return res, nil
}

// Compile-time seam check.
var _ ScenarioGrader = LibGrader{}
