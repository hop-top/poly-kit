package github_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hop.top/kit/go/integrations/repohost"
	_ "hop.top/kit/go/integrations/repohost/github"
)

// fixturePR is the canonical open PR fixture in GitHub's REST shape.
const fixturePR = `{
  "number": 1,
  "state": "open",
  "title": "Add feature",
  "body": "body text",
  "user": {"login": "alice"},
  "html_url": "https://github.com/foo/bar/pull/1",
  "diff_url": "https://github.com/foo/bar/pull/1.diff",
  "head": {"ref": "feature", "sha": "deadbeef"},
  "base": {"ref": "main"},
  "labels": [{"name": "bug", "color": "ff0000"}],
  "draft": false,
  "created_at": "2026-01-01T00:00:00Z",
  "updated_at": "2026-01-02T00:00:00Z"
}`

// fixturePRClosedMerged is a closed-merged PR. go-scm flips Merged
// when merged_at is non-empty.
const fixturePRClosedMerged = `{
  "number": 2,
  "state": "closed",
  "title": "Old feature",
  "user": {"login": "bob"},
  "html_url": "https://github.com/foo/bar/pull/2",
  "head": {"ref": "feature2"},
  "base": {"ref": "main"},
  "labels": [{"name": "enhancement"}],
  "merged_at": "2026-01-03T00:00:00Z",
  "created_at": "2026-01-01T00:00:00Z",
  "updated_at": "2026-01-03T00:00:00Z"
}`

// fixturePRClosedNotMerged is closed but not merged.
const fixturePRClosedNotMerged = `{
  "number": 3,
  "state": "closed",
  "title": "Abandoned",
  "user": {"login": "carol"},
  "html_url": "https://github.com/foo/bar/pull/3",
  "head": {"ref": "feature3"},
  "base": {"ref": "main"},
  "labels": [],
  "created_at": "2026-01-01T00:00:00Z",
  "updated_at": "2026-01-04T00:00:00Z"
}`

const fixtureIssue = `{
  "number": 10,
  "state": "open",
  "title": "Bug found",
  "user": {"login": "alice"},
  "html_url": "https://github.com/foo/bar/issues/10",
  "labels": [{"name": "bug"}],
  "created_at": "2026-01-01T00:00:00Z",
  "updated_at": "2026-01-02T00:00:00Z"
}`

const fixtureCommit = `{
  "sha": "abc123",
  "html_url": "https://github.com/foo/bar/commit/abc123",
  "commit": {
    "message": "first commit",
    "author": {"name": "alice", "email": "alice@example.test", "date": "2026-01-01T00:00:00Z"}
  },
  "author": {"login": "alice"}
}`

const fixtureRepo = `{
  "id": 42,
  "name": "bar",
  "full_name": "foo/bar",
  "owner": {"login": "foo"},
  "default_branch": "main",
  "private": false,
  "html_url": "https://github.com/foo/bar",
  "clone_url": "https://github.com/foo/bar.git",
  "ssh_url": "git@github.com:foo/bar.git",
  "visibility": "public"
}`

const fixtureComment = `{
  "id": 999,
  "body": "looks good",
  "user": {"login": "alice"},
  "html_url": "https://github.com/foo/bar/issues/1#issuecomment-999",
  "created_at": "2026-01-01T00:00:00Z"
}`

// newServer returns an httptest server with the provided handler and
// a configured Host pointed at it.
func newServer(t *testing.T, h http.HandlerFunc) (repohost.MutableHost, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	host, err := repohost.Open(context.Background(), repohost.Config{
		Provider: "github",
		BaseURL:  srv.URL,
		Token:    "test",
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return host, srv
}

func TestHost_ListPullRequests_Open(t *testing.T) {
	host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/repos/foo/bar/pulls") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		// Zero-value Filter (treated as open-only) → go-scm sets
		// Open=true Closed=false → encoder omits the state param.
		if got := r.URL.Query().Get("state"); got != "" {
			t.Errorf("state = %q, want empty (open-only default)", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, "[%s]", fixturePR)
	})

	prs, err := host.ListPullRequests(context.Background(), "foo/bar", repohost.Filter{})
	if err != nil {
		t.Fatalf("ListPullRequests: %v", err)
	}
	if len(prs) != 1 {
		t.Fatalf("want 1 PR, got %d", len(prs))
	}
	got := prs[0]
	if got.Number != 1 || got.Title != "Add feature" || got.Author != "alice" || got.State != "open" {
		t.Errorf("PR fields wrong: %+v", got)
	}
	if got.HeadRef != "feature" || got.BaseRef != "main" {
		t.Errorf("PR refs wrong: head=%q base=%q", got.HeadRef, got.BaseRef)
	}
	if len(got.Labels) != 1 || got.Labels[0] != "bug" {
		t.Errorf("PR labels = %v, want [bug]", got.Labels)
	}
	if got.Raw == nil || got.Raw["state_raw"] != "open" {
		t.Errorf("PR raw = %+v", got.Raw)
	}
	if got.Raw["body"] != "body text" {
		t.Errorf("PR raw body = %v", got.Raw["body"])
	}
}

func TestHost_ListPullRequests_StateFilters(t *testing.T) {
	cases := []struct {
		name      string
		f         repohost.Filter
		wantState string
	}{
		// go-scm encoder: Open-only emits no state param;
		// Closed-only emits "closed"; Both emit "all".
		{"open-zero", repohost.Filter{}, ""},
		{"open-explicit", repohost.Filter{Open: true}, ""},
		{"closed", repohost.Filter{Closed: true}, "closed"},
		{"all", repohost.Filter{Open: true, Closed: true}, "all"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
				if got := r.URL.Query().Get("state"); got != tc.wantState {
					t.Errorf("state = %q, want %q", got, tc.wantState)
				}
				fmt.Fprintf(w, "[]")
			})
			if _, err := host.ListPullRequests(context.Background(), "foo/bar", tc.f); err != nil {
				t.Fatalf("ListPullRequests: %v", err)
			}
		})
	}
}

func TestHost_ListPullRequests_MergedState(t *testing.T) {
	host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "[%s, %s]", fixturePRClosedMerged, fixturePRClosedNotMerged)
	})
	prs, err := host.ListPullRequests(context.Background(), "foo/bar", repohost.Filter{Closed: true})
	if err != nil {
		t.Fatalf("ListPullRequests: %v", err)
	}
	if len(prs) != 2 {
		t.Fatalf("want 2 PRs, got %d", len(prs))
	}
	if prs[0].State != "merged" {
		t.Errorf("PR0 state = %q, want merged", prs[0].State)
	}
	if prs[1].State != "closed" {
		t.Errorf("PR1 state = %q, want closed", prs[1].State)
	}
}

func TestHost_ListPullRequests_AuthorLabelFilter(t *testing.T) {
	host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "[%s, %s]", fixturePR, fixturePRClosedMerged)
	})
	prs, err := host.ListPullRequests(context.Background(), "foo/bar", repohost.Filter{Open: true, Closed: true, Author: "bob"})
	if err != nil {
		t.Fatalf("ListPullRequests: %v", err)
	}
	if len(prs) != 1 || prs[0].Author != "bob" {
		t.Errorf("expected only bob's PR, got %+v", prs)
	}

	host2, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "[%s, %s]", fixturePR, fixturePRClosedNotMerged)
	})
	prs2, err := host2.ListPullRequests(context.Background(), "foo/bar", repohost.Filter{Open: true, Closed: true, Label: "bug"})
	if err != nil {
		t.Fatalf("ListPullRequests: %v", err)
	}
	if len(prs2) != 1 || prs2[0].Number != 1 {
		t.Errorf("expected only PR with bug label, got %+v", prs2)
	}
}

func TestHost_ListPullRequests_Limit(t *testing.T) {
	host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("per_page"); got != "1" {
			t.Errorf("per_page = %q, want 1", got)
		}
		fmt.Fprintf(w, "[%s, %s]", fixturePR, fixturePRClosedMerged)
	})
	prs, err := host.ListPullRequests(context.Background(), "foo/bar", repohost.Filter{Open: true, Limit: 1})
	if err != nil {
		t.Fatalf("ListPullRequests: %v", err)
	}
	if len(prs) != 1 {
		t.Errorf("limit not honored: got %d PRs", len(prs))
	}
}

func TestHost_ListPullRequests_NotFound(t *testing.T) {
	host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"message": "Not Found"}`)
	})
	_, err := host.ListPullRequests(context.Background(), "foo/bar", repohost.Filter{})
	if !errors.Is(err, repohost.ErrRepoNotFound) {
		t.Errorf("err = %v, want ErrRepoNotFound", err)
	}
}

func TestHost_ListPullRequests_NetworkError(t *testing.T) {
	host, srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {})
	srv.Close()
	_, err := host.ListPullRequests(context.Background(), "foo/bar", repohost.Filter{})
	if err == nil {
		t.Fatal("expected error on closed server")
	}
	if !strings.Contains(err.Error(), "github: list prs") {
		t.Errorf("error not wrapped: %v", err)
	}
}

func TestHost_ListPullRequests_BadRepo(t *testing.T) {
	host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {})
	_, err := host.ListPullRequests(context.Background(), "no-slash", repohost.Filter{})
	if err == nil {
		t.Fatal("expected error for bad repo string")
	}
}

func TestHost_ListIssues(t *testing.T) {
	host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/repos/foo/bar/issues") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		fmt.Fprintf(w, "[%s]", fixtureIssue)
	})
	issues, err := host.ListIssues(context.Background(), "foo/bar", repohost.Filter{})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("want 1 issue, got %d", len(issues))
	}
	got := issues[0]
	if got.Number != 10 || got.Title != "Bug found" || got.Author != "alice" || got.State != "open" {
		t.Errorf("issue fields wrong: %+v", got)
	}
}

func TestHost_ListIssues_FiltersOutPRs(t *testing.T) {
	const issueIsPR = `{
		"number": 11,
		"state": "open",
		"title": "this is a PR really",
		"user": {"login": "alice"},
		"html_url": "https://github.com/foo/bar/pull/11",
		"pull_request": {"html_url": "https://github.com/foo/bar/pull/11"},
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-02T00:00:00Z"
	}`
	host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "[%s, %s]", fixtureIssue, issueIsPR)
	})
	issues, err := host.ListIssues(context.Background(), "foo/bar", repohost.Filter{})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 1 {
		t.Errorf("want 1 issue (PR filtered), got %d", len(issues))
	}
}

func TestHost_ListIssues_NotFound(t *testing.T) {
	host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"message": "Not Found"}`)
	})
	_, err := host.ListIssues(context.Background(), "foo/bar", repohost.Filter{})
	if !errors.Is(err, repohost.ErrRepoNotFound) {
		t.Errorf("err = %v, want ErrRepoNotFound", err)
	}
}

func TestHost_GetCommit(t *testing.T) {
	host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/repos/foo/bar/commits/abc123") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		fmt.Fprint(w, fixtureCommit)
	})
	c, err := host.GetCommit(context.Background(), "foo/bar", "abc123")
	if err != nil {
		t.Fatalf("GetCommit: %v", err)
	}
	if c.SHA != "abc123" || c.Author != "alice" || c.Email != "alice@example.test" {
		t.Errorf("commit fields wrong: %+v", c)
	}
	if c.Message != "first commit" {
		t.Errorf("commit message = %q", c.Message)
	}
}

func TestHost_GetCommit_NotFound(t *testing.T) {
	host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"message": "Not Found"}`)
	})
	_, err := host.GetCommit(context.Background(), "foo/bar", "abc123")
	if !errors.Is(err, repohost.ErrCommitNotFound) {
		t.Errorf("err = %v, want ErrCommitNotFound", err)
	}
}

func TestHost_GetRepo(t *testing.T) {
	host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/repos/foo/bar") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		fmt.Fprint(w, fixtureRepo)
	})
	r, err := host.GetRepo(context.Background(), "foo/bar")
	if err != nil {
		t.Fatalf("GetRepo: %v", err)
	}
	if r.Owner != "foo" || r.Name != "bar" || r.DefaultBranch != "main" {
		t.Errorf("repo fields wrong: %+v", r)
	}
	if r.Private {
		t.Errorf("repo should not be private")
	}
	if r.Raw["clone_url"] != "https://github.com/foo/bar.git" {
		t.Errorf("expected clone_url in Raw, got %+v", r.Raw)
	}
}

func TestHost_GetRepo_NotFound(t *testing.T) {
	host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"message": "Not Found"}`)
	})
	_, err := host.GetRepo(context.Background(), "foo/bar")
	if !errors.Is(err, repohost.ErrRepoNotFound) {
		t.Errorf("err = %v, want ErrRepoNotFound", err)
	}
}

func TestHost_PostComment(t *testing.T) {
	host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		// Either PR-comment endpoint or issue-comment endpoint is
		// acceptable — we test that one of them succeeds.
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, fixtureComment)
	})
	c, err := host.PostComment(context.Background(), "foo/bar", 1, "looks good")
	if err != nil {
		t.Fatalf("PostComment: %v", err)
	}
	if c.ID != 999 || c.Author != "alice" || c.Body != "looks good" {
		t.Errorf("comment fields wrong: %+v", c)
	}
}

func TestHost_PostComment_NotFound(t *testing.T) {
	host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"message": "Not Found"}`)
	})
	_, err := host.PostComment(context.Background(), "foo/bar", 1, "looks good")
	if !errors.Is(err, repohost.ErrRepoNotFound) {
		t.Errorf("err = %v, want ErrRepoNotFound", err)
	}
}

func TestHost_PostComment_EmptyBody(t *testing.T) {
	host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server must not be hit for empty body")
	})
	_, err := host.PostComment(context.Background(), "foo/bar", 1, "")
	if err == nil {
		t.Fatal("expected error for empty body")
	}
}

func TestOpen_ProviderMismatch(t *testing.T) {
	_, err := repohost.Open(context.Background(), repohost.Config{Provider: "github-bogus"})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestOpen_TokenFromEnv(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "envtoken")
	host, err := repohost.Open(context.Background(), repohost.Config{Provider: "github"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if host == nil {
		t.Fatal("expected non-nil host")
	}
}

func TestOpen_GHTokenFallback(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "ghtoken")
	host, err := repohost.Open(context.Background(), repohost.Config{Provider: "github"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if host == nil {
		t.Fatal("expected non-nil host")
	}
}
