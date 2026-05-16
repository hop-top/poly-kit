package repohost_test

import (
	"context"
	"testing"
	"time"

	"hop.top/kit/go/integrations/repohost"
)

func TestBaseline_Deterministic(t *testing.T) {
	r1, p1, i1, c1, cm1 := repohost.Baseline()
	r2, p2, i2, c2, cm2 := repohost.Baseline()

	if r1.Owner != r2.Owner || r1.Name != r2.Name || r1.HTMLURL != r2.HTMLURL {
		t.Errorf("repo not deterministic")
	}
	if p1.Number != p2.Number || p1.Title != p2.Title || !p1.CreatedAt.Equal(p2.CreatedAt) {
		t.Errorf("pull request not deterministic")
	}
	if i1.Number != i2.Number || !i1.CreatedAt.Equal(i2.CreatedAt) {
		t.Errorf("issue not deterministic")
	}
	if c1.SHA != c2.SHA || !c1.CreatedAt.Equal(c2.CreatedAt) {
		t.Errorf("commit not deterministic")
	}
	if cm1.ID != cm2.ID || !cm1.CreatedAt.Equal(cm2.CreatedAt) {
		t.Errorf("comment not deterministic")
	}

	want := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if !p1.CreatedAt.Equal(want) {
		t.Errorf("baseline timestamp = %v, want %v", p1.CreatedAt, want)
	}
}

func TestBaseline_Invariants(t *testing.T) {
	repo, pr, issue, commit, comment := repohost.Baseline()

	if repo.Owner == "" || repo.Name == "" || repo.DefaultBranch == "" {
		t.Errorf("repo missing required fields: %+v", repo)
	}
	if repo.Raw == nil {
		t.Errorf("repo.Raw must be non-nil")
	}
	if pr.State != "open" {
		t.Errorf("baseline PR state = %q, want %q", pr.State, "open")
	}
	if len(pr.Labels) == 0 {
		t.Errorf("baseline PR labels must be populated")
	}
	if pr.Raw == nil {
		t.Errorf("pr.Raw must be non-nil")
	}
	if issue.State != "open" {
		t.Errorf("baseline issue state = %q, want %q", issue.State, "open")
	}
	if issue.Labels == nil {
		t.Errorf("baseline issue labels must be non-nil")
	}
	if issue.Raw == nil {
		t.Errorf("issue.Raw must be non-nil")
	}
	if commit.SHA == "" || commit.Email == "" {
		t.Errorf("baseline commit missing required fields: %+v", commit)
	}
	if commit.Raw == nil {
		t.Errorf("commit.Raw must be non-nil")
	}
	if comment.ID == 0 || comment.Body == "" {
		t.Errorf("baseline comment missing required fields: %+v", comment)
	}
	if comment.Raw == nil {
		t.Errorf("comment.Raw must be non-nil")
	}
}

func TestOpen_UnknownProvider(t *testing.T) {
	_, err := repohost.Open(context.Background(), repohost.Config{Provider: "does-not-exist"})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestRegisterDriver_RoundTrip(t *testing.T) {
	called := false
	repohost.RegisterDriver("test-roundtrip", func(cfg repohost.Config) (repohost.MutableHost, error) {
		called = true
		if cfg.Provider != "test-roundtrip" {
			t.Errorf("opener got provider %q", cfg.Provider)
		}
		return nil, nil
	})

	if _, err := repohost.Open(context.Background(), repohost.Config{Provider: "test-roundtrip"}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !called {
		t.Fatalf("registered opener was not invoked")
	}
}
