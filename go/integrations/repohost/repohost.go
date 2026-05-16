package repohost

import (
	"context"
	"time"
)

// Host is the read-side interface every driver implements. Drivers
// honor ctx by passing it to every underlying API call.
type Host interface {
	// ListPullRequests returns pull requests matching f for the
	// repository identified as "owner/name". A zero-value Filter is
	// treated as open-only.
	ListPullRequests(ctx context.Context, repo string, f Filter) ([]PullRequest, error)
	// ListIssues returns issues matching f for the repository
	// identified as "owner/name". A zero-value Filter is treated as
	// open-only.
	ListIssues(ctx context.Context, repo string, f Filter) ([]Issue, error)
	// GetCommit fetches commit metadata for the given SHA in the
	// repository.
	GetCommit(ctx context.Context, repo, sha string) (Commit, error)
	// GetRepo returns repository metadata.
	GetRepo(ctx context.Context, repo string) (Repo, error)
}

// MutableHost extends Host with the comment write surface. The
// PostComment write surface is the v1 cap; admin operations land
// when adopters surface the need.
type MutableHost interface {
	Host
	// PostComment posts a comment on a pull request or issue
	// identified by number. Drivers that need to distinguish PR
	// from issue dispatch internally (e.g. via a 404 fallback).
	PostComment(ctx context.Context, repo string, number int, body string) (Comment, error)
}

// Filter narrows list operations.
//
// The zero value is treated as open-only: when both Open and Closed
// are false, drivers MUST behave as if Open were true. Callers that
// want both states should set Open and Closed to true.
type Filter struct {
	// Open selects open items. Defaults to true via the zero-value
	// rule above.
	Open bool
	// Closed selects closed (and merged, where applicable) items.
	Closed bool
	// Author filters by username. Empty means any author.
	Author string
	// Label filters by a single label name. Empty means any label.
	Label string
	// Limit caps the result count. Zero means driver default.
	Limit int
}

// PullRequest is the unified pull-request shape. Provider-specific
// fields go in Raw to avoid lowest-common-denominator API loss.
type PullRequest struct {
	// Number is the per-repo PR number (e.g. 42). Drivers use the
	// per-project IID where applicable (GitLab) — never the global
	// row id.
	Number int
	// Title is the PR title.
	Title string
	// Author is the PR author's username.
	Author string
	// State is one of "open", "closed", "merged" (closed enum).
	// Drivers normalize their native state strings into this set.
	State string
	// HeadRef is the source branch.
	HeadRef string
	// BaseRef is the target branch.
	BaseRef string
	// URL is the canonical web URL of the PR.
	URL string
	// CreatedAt is the PR creation time.
	CreatedAt time.Time
	// UpdatedAt is the last-update time.
	UpdatedAt time.Time
	// Labels is the set of label names applied. Always non-nil;
	// empty slice when no labels are set.
	Labels []string
	// Raw holds provider-specific extras (e.g. mergeable, draft,
	// milestone). Always non-nil; empty map when no extras.
	Raw map[string]any
}

// Issue is the unified issue shape. Mirrors PullRequest minus the
// head/base branch fields.
type Issue struct {
	Number    int
	Title     string
	Author    string
	State     string
	URL       string
	CreatedAt time.Time
	UpdatedAt time.Time
	Labels    []string
	Raw       map[string]any
}

// Commit holds commit metadata.
type Commit struct {
	SHA       string
	Author    string
	Email     string
	Message   string
	URL       string
	CreatedAt time.Time
	Raw       map[string]any
}

// Comment is a posted comment on a PR or issue.
type Comment struct {
	ID        int64
	Author    string
	Body      string
	URL       string
	CreatedAt time.Time
	Raw       map[string]any
}

// Repo holds repository metadata.
type Repo struct {
	Owner         string
	Name          string
	DefaultBranch string
	Private       bool
	HTMLURL       string
	Raw           map[string]any
}

// ParsedURL is the result of [ParseURL]. The Provider field is set
// from the host heuristic; callers may override when wrapping a
// host that cannot be auto-detected.
type ParsedURL struct {
	// Provider is one of "github", "gitlab", "gitea", "bitbucket".
	Provider string
	// BaseURL is the host root (scheme + host), e.g.
	// "https://github.example.com".
	BaseURL string
	// Owner is the repository owner. For GitLab sub-groups, the
	// joined sub-group path (e.g. "grp/sub").
	Owner string
	// Repo is the repository name (last path segment).
	Repo string
	// Kind is one of "pull", "issue", "commit", "repo", or "".
	Kind string
	// Number is the PR or issue number (0 when Kind != pull|issue).
	Number int
	// SHA is the commit SHA (populated when Kind == "commit").
	SHA string
}
