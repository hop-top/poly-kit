package scenario_test

import (
	"path/filepath"
	"strings"
	"testing"

	"hop.top/kit/go/conformance/scenario"
)

func TestValidate_OKMinimal(t *testing.T) {
	s, err := scenario.ParseFile(filepath.Join("testdata", "ok-minimal.yaml"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := scenario.Validate(s); err != nil {
		t.Fatalf("Validate(ok-minimal) returned error: %v", err)
	}
}

func TestValidate_OKJudge(t *testing.T) {
	s, err := scenario.ParseFile(filepath.Join("testdata", "ok-judge.yaml"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := scenario.Validate(s); err != nil {
		t.Fatalf("Validate(ok-judge) returned error: %v", err)
	}
}

func TestValidate_BadMissingID(t *testing.T) {
	s, err := scenario.ParseFile(filepath.Join("testdata", "bad-missing-id.yaml"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	err = scenario.Validate(s)
	if err == nil {
		t.Fatalf("expected validation error for missing scenario_id")
	}
	if !scenario.IsValidationError(err) {
		t.Errorf("expected ValidationErrors, got %T", err)
	}
	if !strings.Contains(err.Error(), "scenario_id") {
		t.Errorf("error should mention scenario_id; got %s", err.Error())
	}
}

func TestValidate_BadUnknownVerb(t *testing.T) {
	s, err := scenario.ParseFile(filepath.Join("testdata", "bad-unknown-verb.yaml"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	err = scenario.Validate(s)
	if err == nil {
		t.Fatalf("expected validation error for unknown verb")
	}
	if !strings.Contains(err.Error(), "this_is_not_a_verb") {
		t.Errorf("error should mention the offending verb; got %s", err.Error())
	}
}

func TestValidate_JudgeAssertionRequiresJudgeBlock(t *testing.T) {
	// Author a scenario with judge_score_above but no judge block.
	s := &scenario.Scenario{
		ScenarioID:     "no-judge-block",
		SchemaVersion:  "1",
		Binary:         "spaced",
		FactorCoverage: []int{4},
		Tier:           3,
		StoryRef: scenario.StoryRef{
			StoryID:     "x",
			StoryPath:   "x",
			ContentHash: "sha256:" + strings.Repeat("0", 64),
		},
		Steps: []scenario.Step{{ID: "report", Invoke: []string{"report"}}},
		Assertions: []scenario.Assertion{{
			ID: "a", Kind: "judge_score_above", On: "report", Factor: 4,
			Args: map[string]any{"judge_id": "missing", "value": 0.5},
		}},
	}
	err := scenario.Validate(s)
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(err.Error(), "judge") {
		t.Errorf("error should mention judge; got %s", err.Error())
	}
}

func TestValidate_BadContentHash(t *testing.T) {
	s := &scenario.Scenario{
		ScenarioID:     "bad-hash",
		SchemaVersion:  "1",
		Binary:         "spaced",
		FactorCoverage: []int{1},
		Tier:           3,
		StoryRef: scenario.StoryRef{
			StoryID:     "x",
			StoryPath:   "x",
			ContentHash: "deadbeef", // not sha256:<64hex>
		},
		Steps: []scenario.Step{{ID: "s", Invoke: []string{"a"}}},
		Assertions: []scenario.Assertion{{
			ID: "a", Kind: "exit_code_equals", On: "s", Factor: 1,
			Args: map[string]any{"value": 0},
		}},
	}
	err := scenario.Validate(s)
	if err == nil {
		t.Fatalf("expected validation error for bad content_hash")
	}
	if !strings.Contains(err.Error(), "content_hash") {
		t.Errorf("error should mention content_hash; got %s", err.Error())
	}
}
