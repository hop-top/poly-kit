package repohost_test

import (
	"context"
	"testing"

	"hop.top/kit/go/integrations/repohost"

	// Driver mock registrations.
	_ "hop.top/kit/go/integrations/repohost/bitbucket/mock"
	_ "hop.top/kit/go/integrations/repohost/gitea/mock"
	_ "hop.top/kit/go/integrations/repohost/gitee/mock"
	_ "hop.top/kit/go/integrations/repohost/github/mock"
	_ "hop.top/kit/go/integrations/repohost/gitlab/mock"
)

// TestConformance_AllDrivers exercises each registered driver mock
// against the same scenarios and asserts every driver normalizes to
// the unified [repohost] surface — same closed state enum, non-nil
// Labels and Raw, populated Number/Title/URL, etc.
//
// Each driver mock returns [repohost.Baseline] values by default;
// this test relies on that contract to avoid stage-specific knob
// setup. The mocks live in sibling sub-packages and are registered
// via blank imports above.
//
// The conformance test is the safety net catching cross-driver drift
// (e.g. one driver leaking a non-normalized state value, or
// returning nil where the contract requires a non-nil empty slice).
func TestConformance_AllDrivers(t *testing.T) {
	drivers := []string{"github-mock", "gitlab-mock", "gitea-mock", "gitee-mock", "bitbucket-mock"}

	for _, name := range drivers {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			cfg := repohost.Config{Provider: name, BaseURL: "https://example.test"}
			h, err := repohost.Open(ctx, cfg)
			if err != nil {
				t.Fatalf("open %s: %v", name, err)
			}

			// ListPullRequests
			prs, err := h.ListPullRequests(ctx, "kit-test/fixture", repohost.Filter{})
			if err != nil {
				t.Fatalf("list prs: %v", err)
			}
			if len(prs) == 0 {
				t.Fatal("expected at least one PR from baseline")
			}
			for _, pr := range prs {
				assertUnifiedPR(t, pr)
			}

			// ListIssues
			issues, err := h.ListIssues(ctx, "kit-test/fixture", repohost.Filter{})
			if err != nil {
				t.Fatalf("list issues: %v", err)
			}
			if len(issues) == 0 {
				t.Fatal("expected at least one issue from baseline")
			}
			for _, is := range issues {
				assertUnifiedIssue(t, is)
			}

			// GetCommit
			c, err := h.GetCommit(ctx, "kit-test/fixture", "abc123def456abc123def456abc123def456abcd")
			if err != nil {
				t.Fatalf("get commit: %v", err)
			}
			assertUnifiedCommit(t, c)

			// GetRepo
			r, err := h.GetRepo(ctx, "kit-test/fixture")
			if err != nil {
				t.Fatalf("get repo: %v", err)
			}
			assertUnifiedRepo(t, r)

			// PostComment
			cm, err := h.PostComment(ctx, "kit-test/fixture", 1, "conformance comment")
			if err != nil {
				t.Fatalf("post comment: %v", err)
			}
			assertUnifiedComment(t, cm)
		})
	}
}

// assertUnifiedPR validates that pr conforms to the unified
// [repohost.PullRequest] contract — populated required fields, the
// closed state enum, and non-nil Labels/Raw.
//
// Note on URL parse: [repohost.ParseURL] is a heuristic over known
// host substrings (github./gitlab./gitea./bitbucket.). The mock
// drivers return baseline URLs of the form
// "https://example.test/...", which won't match any provider host
// pattern. We do not assert that ParseURL succeeds on baseline URLs;
// per-driver tests cover that surface separately.
func assertUnifiedPR(t *testing.T, pr repohost.PullRequest) {
	t.Helper()
	if pr.Number == 0 {
		t.Error("Number must be non-zero")
	}
	if pr.Title == "" {
		t.Error("Title must be non-empty")
	}
	if pr.URL == "" {
		t.Error("URL must be non-empty")
	}
	if pr.Labels == nil {
		t.Error("Labels must be non-nil (may be empty)")
	}
	if pr.Raw == nil {
		t.Error("Raw must be non-nil (may be empty)")
	}
	switch pr.State {
	case "open", "closed", "merged":
	default:
		t.Errorf("State must be one of {open,closed,merged}, got %q", pr.State)
	}
}

// assertUnifiedIssue mirrors assertUnifiedPR for [repohost.Issue].
// Issues have a smaller state space — open or closed.
func assertUnifiedIssue(t *testing.T, is repohost.Issue) {
	t.Helper()
	if is.Number == 0 {
		t.Error("Number must be non-zero")
	}
	if is.Title == "" {
		t.Error("Title must be non-empty")
	}
	if is.URL == "" {
		t.Error("URL must be non-empty")
	}
	if is.Labels == nil {
		t.Error("Labels must be non-nil (may be empty)")
	}
	if is.Raw == nil {
		t.Error("Raw must be non-nil (may be empty)")
	}
	switch is.State {
	case "open", "closed":
	default:
		t.Errorf("State must be one of {open,closed}, got %q", is.State)
	}
}

// assertUnifiedCommit validates that c conforms to the unified
// [repohost.Commit] contract.
func assertUnifiedCommit(t *testing.T, c repohost.Commit) {
	t.Helper()
	if c.SHA == "" {
		t.Error("SHA must be non-empty")
	}
	if c.URL == "" {
		t.Error("URL must be non-empty")
	}
	if c.Raw == nil {
		t.Error("Raw must be non-nil (may be empty)")
	}
}

// assertUnifiedRepo validates that r conforms to [repohost.Repo].
func assertUnifiedRepo(t *testing.T, r repohost.Repo) {
	t.Helper()
	if r.Owner == "" {
		t.Error("Owner must be non-empty")
	}
	if r.Name == "" {
		t.Error("Name must be non-empty")
	}
	if r.HTMLURL == "" {
		t.Error("HTMLURL must be non-empty")
	}
	if r.Raw == nil {
		t.Error("Raw must be non-nil (may be empty)")
	}
}

// assertUnifiedComment validates a [repohost.Comment].
func assertUnifiedComment(t *testing.T, c repohost.Comment) {
	t.Helper()
	if c.ID == 0 {
		t.Error("ID must be non-zero")
	}
	if c.Body == "" {
		t.Error("Body must be non-empty")
	}
	if c.URL == "" {
		t.Error("URL must be non-empty")
	}
	if c.Raw == nil {
		t.Error("Raw must be non-nil (may be empty)")
	}
}
