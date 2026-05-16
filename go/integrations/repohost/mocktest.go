package repohost

import "time"

// baselineTime is the deterministic timestamp used by [Baseline] so
// adopter test assertions are stable across CI runs.
var baselineTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// Baseline returns a deterministic {Repo, PullRequest, Issue, Commit,
// Comment} tuple shared by the cross-driver conformance test and
// adopter unit tests that need a quick fixture. All driver mocks
// should accept and return Baseline values to keep test assertions
// uniform.
func Baseline() (Repo, PullRequest, Issue, Commit, Comment) {
	repo := Repo{
		Owner:         "kit-test",
		Name:          "fixture",
		DefaultBranch: "main",
		Private:       false,
		HTMLURL:       "https://example.test/kit-test/fixture",
		Raw:           map[string]any{"baseline": true},
	}
	pr := PullRequest{
		Number:    1,
		Title:     "Baseline PR",
		Author:    "alice",
		State:     "open",
		HeadRef:   "feature",
		BaseRef:   "main",
		URL:       "https://example.test/kit-test/fixture/pull/1",
		CreatedAt: baselineTime,
		UpdatedAt: baselineTime,
		Labels:    []string{"bug"},
		Raw:       map[string]any{"baseline": true},
	}
	issue := Issue{
		Number:    1,
		Title:     "Baseline issue",
		Author:    "alice",
		State:     "open",
		URL:       "https://example.test/kit-test/fixture/issues/1",
		CreatedAt: baselineTime,
		UpdatedAt: baselineTime,
		Labels:    []string{"bug"},
		Raw:       map[string]any{"baseline": true},
	}
	commit := Commit{
		SHA:       "abc123def456abc123def456abc123def456abcd",
		Author:    "alice",
		Email:     "alice@example.test",
		Message:   "baseline commit",
		URL:       "https://example.test/kit-test/fixture/commit/abc123def456abc123def456abc123def456abcd",
		CreatedAt: baselineTime,
		Raw:       map[string]any{"baseline": true},
	}
	comment := Comment{
		ID:        1,
		Author:    "alice",
		Body:      "baseline comment",
		URL:       "https://example.test/kit-test/fixture/pull/1#comment-1",
		CreatedAt: baselineTime,
		Raw:       map[string]any{"baseline": true},
	}
	return repo, pr, issue, commit, comment
}
