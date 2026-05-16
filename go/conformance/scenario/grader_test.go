package scenario_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/kit/go/conformance/scenario"
	"hop.top/kit/go/conformance/scenario/judge"
)

// hashOf returns the "sha256:<hex>" hash of b.
func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// buildMinimalScenario constructs a passing scenario whose story
// content matches the supplied bytes.
func buildMinimalScenario(t *testing.T, storyContent []byte) *scenario.Scenario {
	t.Helper()
	return &scenario.Scenario{
		ScenarioID:     "round-trip",
		SchemaVersion:  "1",
		Binary:         "spaced",
		FactorCoverage: []int{1, 2},
		Tier:           3,
		StoryRef: scenario.StoryRef{
			StoryID:     "round-trip-story",
			StoryPath:   "stories/round-trip.yaml",
			ContentHash: hashOf(storyContent),
		},
		Steps: []scenario.Step{{ID: "launch", Invoke: []string{"launch"}}},
		Assertions: []scenario.Assertion{
			{ID: "exits-ok", Kind: "exit_code_equals", On: "launch", Factor: 1,
				Args: map[string]any{"value": 0}},
			{ID: "stream-ok", Kind: "stream_discipline_pass", On: "launch", Factor: 2},
		},
	}
}

func TestGrade_RoundTripPass(t *testing.T) {
	story := []byte("story_id: round-trip-story\ntitle: round trip\n")
	sc := buildMinimalScenario(t, story)
	in := scenario.Input{
		Scenario:     sc,
		StoryContent: story,
		StepCaptures: map[string]scenario.Capture{
			"launch": {ExitCode: 0, Stdout: []byte(`{"ok": true}`), Stderr: nil},
		},
	}
	res, err := scenario.Grade(context.Background(), in)
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	if res.Verdict != scenario.VerdictPass {
		t.Errorf("Verdict = %s, want pass; assertions: %+v", res.Verdict, res.Assertions)
	}
	if res.RulesVersion == "" {
		t.Errorf("expected non-empty RulesVersion")
	}
	if res.GraderVersion != scenario.GraderVersion {
		t.Errorf("GraderVersion = %s, want %s", res.GraderVersion, scenario.GraderVersion)
	}
}

func TestGrade_StoryHashMismatch(t *testing.T) {
	sc := buildMinimalScenario(t, []byte("original"))
	in := scenario.Input{
		Scenario:     sc,
		StoryContent: []byte("tampered"),
		StepCaptures: map[string]scenario.Capture{
			"launch": {ExitCode: 0, Stdout: []byte(`{"ok": true}`)},
		},
	}
	res, err := scenario.Grade(context.Background(), in)
	if err == nil {
		t.Fatalf("expected story-hash-mismatch error")
	}
	if res.Verdict != scenario.VerdictUngradable {
		t.Errorf("Verdict = %s, want ungradable", res.Verdict)
	}
	if !strings.Contains(err.Error(), "STORY_HASH_MISMATCH") {
		t.Errorf("error should contain STORY_HASH_MISMATCH; got %v", err)
	}
}

func TestGrade_ExitCodeFail(t *testing.T) {
	story := []byte("x")
	sc := buildMinimalScenario(t, story)
	in := scenario.Input{
		Scenario:     sc,
		StoryContent: story,
		StepCaptures: map[string]scenario.Capture{
			"launch": {ExitCode: 1, Stdout: []byte(`{"ok": false}`)},
		},
	}
	res, _ := scenario.Grade(context.Background(), in)
	if res.Verdict != scenario.VerdictFail {
		t.Errorf("Verdict = %s, want fail", res.Verdict)
	}
}

func TestGrade_TierRedaction(t *testing.T) {
	story := []byte("x")
	sc := buildMinimalScenario(t, story)
	in := scenario.Input{
		Scenario:     sc,
		StoryContent: story,
		StepCaptures: map[string]scenario.Capture{
			"launch": {ExitCode: 0, Stdout: []byte(`{"ok": true}`)},
		},
	}
	res, _ := scenario.Grade(context.Background(), in)

	t1 := res.ToTier(1)
	if len(t1.Facets) != 0 || len(t1.Assertions) != 0 || len(t1.JudgeTraces) != 0 {
		t.Errorf("Tier 1 should strip body; got facets=%d, assertions=%d, traces=%d",
			len(t1.Facets), len(t1.Assertions), len(t1.JudgeTraces))
	}
	if t1.Verdict != res.Verdict {
		t.Errorf("Tier 1 must preserve Verdict")
	}

	t2 := res.ToTier(2)
	if len(t2.Facets) == 0 {
		t.Errorf("Tier 2 should retain facets")
	}
	if len(t2.Assertions) != 0 || len(t2.JudgeTraces) != 0 {
		t.Errorf("Tier 2 should strip assertions/traces; got %d/%d", len(t2.Assertions), len(t2.JudgeTraces))
	}

	t3 := res.ToTier(3)
	if len(t3.Assertions) == 0 {
		t.Errorf("Tier 3 should retain assertions")
	}
}

func TestGrade_JudgePass(t *testing.T) {
	story := []byte("x")
	sc, err := scenario.ParseFile(filepath.Join("testdata", "ok-judge.yaml"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	sc.StoryRef.ContentHash = hashOf(story)

	in := scenario.Input{
		Scenario:     sc,
		StoryContent: story,
		StepCaptures: map[string]scenario.Capture{
			"report": {ExitCode: 0, Stdout: []byte(`{"summary": "ok"}`)},
		},
		Judge: judge.NewCanned(map[string]float64{
			"clarity-judge": 0.9,
		}),
	}
	res, gerr := scenario.Grade(context.Background(), in)
	if gerr != nil {
		t.Fatalf("Grade: %v", gerr)
	}
	if res.Verdict != scenario.VerdictPass {
		t.Errorf("Verdict = %s, want pass; assertions: %+v", res.Verdict, res.Assertions)
	}
	if len(res.JudgeTraces) != 1 {
		t.Errorf("want 1 judge trace; got %d", len(res.JudgeTraces))
	}
}

func TestGrade_JudgeFail(t *testing.T) {
	story := []byte("x")
	sc, _ := scenario.ParseFile(filepath.Join("testdata", "ok-judge.yaml"))
	sc.StoryRef.ContentHash = hashOf(story)
	in := scenario.Input{
		Scenario:     sc,
		StoryContent: story,
		StepCaptures: map[string]scenario.Capture{
			"report": {ExitCode: 0, Stdout: []byte(`{"summary": "ok"}`)},
		},
		Judge: judge.NewCanned(map[string]float64{
			"clarity-judge": 0.1, // below 0.5 threshold
		}),
	}
	res, _ := scenario.Grade(context.Background(), in)
	if res.Verdict != scenario.VerdictFail {
		t.Errorf("Verdict = %s, want fail", res.Verdict)
	}
}

func TestGrade_NoJudgeAvailable(t *testing.T) {
	story := []byte("x")
	sc, _ := scenario.ParseFile(filepath.Join("testdata", "ok-judge.yaml"))
	sc.StoryRef.ContentHash = hashOf(story)
	in := scenario.Input{
		Scenario:     sc,
		StoryContent: story,
		StepCaptures: map[string]scenario.Capture{
			"report": {ExitCode: 0, Stdout: []byte(`{"summary": "ok"}`)},
		},
		// Judge intentionally nil
	}
	res, _ := scenario.Grade(context.Background(), in)
	if res.Verdict != scenario.VerdictUngradable {
		t.Errorf("Verdict = %s, want ungradable (no judge)", res.Verdict)
	}
}
