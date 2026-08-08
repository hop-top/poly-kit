package svc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"hop.top/kit/go/conformance/scenario"
)

// libGraderInput builds a GradeInput around a one-step scenario that
// asserts exit code 0, with the recorded exit code supplied by the
// caller.
func libGraderInput(t *testing.T, recordedExit int) GradeInput {
	t.Helper()
	story := []byte("story: hello\n")
	sum := sha256.Sum256(story)
	doc := &scenario.Scenario{
		ScenarioID:     "widget",
		SchemaVersion:  "1",
		Binary:         "example",
		FactorCoverage: []int{11},
		Tier:           3,
		StoryRef: scenario.StoryRef{
			StoryID:     "example.story",
			StoryPath:   "stories/example.yaml",
			ContentHash: "sha256:" + hex.EncodeToString(sum[:]),
		},
		Steps: []scenario.Step{{ID: "step-1", Invoke: []string{"example", "run"}}},
		Assertions: []scenario.Assertion{{
			ID: "exits-zero", Kind: "exit_code_equals", On: "step-1", Factor: 11,
			Args: map[string]any{"value": 0},
		}},
	}
	return GradeInput{
		Scenario:     &Scenario{Namespace: "acme", ID: "widget", Version: "v1", Tier: 3, Doc: doc},
		StoryContent: story,
		StepCaptures: map[string]Capture{
			"step-1": {ExitCode: recordedExit, Stdout: []byte("{}\n")},
		},
	}
}

func TestLibGrader_PassReflectsCaptures(t *testing.T) {
	res, err := LibGrader{}.Grade(context.Background(), libGraderInput(t, 0))
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	if res.Verdict != scenario.VerdictPass {
		t.Fatalf("verdict: got %s, want pass; %+v", res.Verdict, res.Assertions)
	}
	if res.GraderVersion != scenario.GraderVersion {
		t.Errorf("grader_version: got %q, want %q", res.GraderVersion, scenario.GraderVersion)
	}
	if len(res.Assertions) != 1 || res.Assertions[0].Status != scenario.StatusPass {
		t.Errorf("assertion trace: %+v", res.Assertions)
	}
}

// TestLibGrader_FailingCaptureDoesNotPass is the svc-layer
// hardcoded-pass regression guard: a capture violating the scenario's
// assertion must never yield a pass verdict.
func TestLibGrader_FailingCaptureDoesNotPass(t *testing.T) {
	res, err := LibGrader{}.Grade(context.Background(), libGraderInput(t, 3))
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	if res.Verdict == scenario.VerdictPass {
		t.Fatalf("failing capture graded pass — verdict fabricated: %+v", res)
	}
	if res.Verdict != scenario.VerdictFail {
		t.Errorf("verdict: got %s, want fail", res.Verdict)
	}
}

func TestLibGrader_StoryTamperUngradable(t *testing.T) {
	in := libGraderInput(t, 0)
	in.StoryContent = []byte("tampered\n")
	res, err := LibGrader{}.Grade(context.Background(), in)
	if err != nil {
		t.Fatalf("Grade must surface the verdict, not an error: %v", err)
	}
	if res.Verdict != scenario.VerdictUngradable {
		t.Errorf("verdict: got %s, want ungradable", res.Verdict)
	}
}

func TestLibGrader_MissingDocErrors(t *testing.T) {
	_, err := LibGrader{}.Grade(context.Background(), GradeInput{Scenario: &Scenario{}})
	if err == nil || !strings.Contains(err.Error(), CodeGraderInternal) {
		t.Fatalf("want %s error for missing document, got %v", CodeGraderInternal, err)
	}
}
