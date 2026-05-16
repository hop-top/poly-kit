package svc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNullRegistry_RefusesEverything(t *testing.T) {
	r := NullRegistry{}
	if _, err := r.Resolve("any"); !errors.Is(err, ErrModelNotRegistered) {
		t.Errorf("NullRegistry.Resolve: want ErrModelNotRegistered, got %v", err)
	}
	if got := r.RegisteredModels(); len(got) != 0 {
		t.Errorf("NullRegistry.RegisteredModels: want empty, got %v", got)
	}
}

func TestStubRegistry_Scores(t *testing.T) {
	r := StubRegistry{CannedVerdict: "pass", CannedScore: 0.9}
	j, err := r.Resolve("stub")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	resp, err := j.Score(context.Background(), JudgeRequest{Prompt: "p", CapturedText: "t"})
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if resp.Verdict != "pass" || resp.Score != 0.9 {
		t.Errorf("Score: got %+v", resp)
	}
}

func TestConfigRegistry_LoadsYAML(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "judges.yaml")
	yaml := `models:
  claude-sonnet-4:
    provider: anthropic
    api_key_env: ANTHROPIC_API_KEY
  gpt-4o:
    provider: openai
    api_key_env: OPENAI_API_KEY
`
	_ = os.WriteFile(path, []byte(yaml), 0o644)

	r, err := NewConfigRegistry(path)
	if err != nil {
		t.Fatalf("NewConfigRegistry: %v", err)
	}
	got := r.RegisteredModels()
	if len(got) != 2 {
		t.Errorf("RegisteredModels: %v", got)
	}

	// Without a registered provider, Resolve returns an error.
	if _, err := r.Resolve("claude-sonnet-4"); err == nil {
		t.Error("expected error: provider not loaded")
	}

	// With a stub provider, Resolve succeeds.
	r.RegisterProvider("anthropic", func(_ string, _ ConfigModelEntry) (AIJudge, error) {
		return stubJudge{model: "claude-sonnet-4", verdict: "pass", score: 1}, nil
	})
	j, err := r.Resolve("claude-sonnet-4")
	if err != nil {
		t.Fatalf("Resolve with factory: %v", err)
	}
	resp, _ := j.Score(context.Background(), JudgeRequest{})
	if resp.Verdict != "pass" {
		t.Errorf("verdict: got %q", resp.Verdict)
	}
}

func TestConfigRegistry_EmptyPath(t *testing.T) {
	r, err := NewConfigRegistry("")
	if err != nil {
		t.Fatalf("NewConfigRegistry empty: %v", err)
	}
	if len(r.RegisteredModels()) != 0 {
		t.Errorf("expected no models")
	}
}
