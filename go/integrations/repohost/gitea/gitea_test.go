package gitea_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hop.top/kit/go/integrations/repohost"
	_ "hop.top/kit/go/integrations/repohost/gitea"
)

const fixturePR = `{
  "id": 1,
  "number": 1,
  "user": {"login": "alice", "username": "alice"},
  "title": "Add feature",
  "body": "body text",
  "labels": [{"name": "bug", "color": "ff0000"}],
  "state": "open",
  "html_url": "https://gitea.example.com/foo/bar/pulls/1",
  "diff_url": "https://gitea.example.com/foo/bar/pulls/1.diff",
  "head": {"ref": "feature", "sha": "deadbeef"},
  "base": {"ref": "main"},
  "merged": false,
  "created_at": "2026-01-01T00:00:00Z",
  "updated_at": "2026-01-02T00:00:00Z"
}`

const fixturePRMerged = `{
  "id": 2,
  "number": 2,
  "user": {"login": "bob", "username": "bob"},
  "title": "Old feature",
  "labels": [],
  "state": "closed",
  "html_url": "https://gitea.example.com/foo/bar/pulls/2",
  "head": {"ref": "feature2"},
  "base": {"ref": "main"},
  "merged": true,
  "merged_at": "2026-01-03T00:00:00Z",
  "created_at": "2026-01-01T00:00:00Z",
  "updated_at": "2026-01-03T00:00:00Z"
}`

const fixturePRClosed = `{
  "id": 3,
  "number": 3,
  "user": {"login": "carol", "username": "carol"},
  "title": "Abandoned",
  "labels": [],
  "state": "closed",
  "html_url": "https://gitea.example.com/foo/bar/pulls/3",
  "head": {"ref": "feature3"},
  "base": {"ref": "main"},
  "merged": false,
  "created_at": "2026-01-01T00:00:00Z",
  "updated_at": "2026-01-04T00:00:00Z"
}`

const fixtureIssue = `{
  "id": 1,
  "number": 10,
  "user": {"login": "alice", "username": "alice"},
  "title": "Bug found",
  "labels": ["bug"],
  "state": "open",
  "html_url": "https://gitea.example.com/foo/bar/issues/10",
  "pull_request": null,
  "created_at": "2026-01-01T00:00:00Z",
  "updated_at": "2026-01-02T00:00:00Z"
}`

const fixtureCommit = `{
  "sha": "abc123",
  "html_url": "https://gitea.example.com/foo/bar/commit/abc123",
  "commit": {
    "message": "first commit",
    "author": {"name": "alice", "email": "alice@example.test", "date": "2026-01-01T00:00:00Z"}
  },
  "author": {"login": "alice", "username": "alice"}
}`

const fixtureRepo = `{
  "id": 42,
  "owner": {"login": "foo", "username": "foo"},
  "name": "bar",
  "full_name": "foo/bar",
  "default_branch": "main",
  "private": false,
  "html_url": "https://gitea.example.com/foo/bar",
  "clone_url": "https://gitea.example.com/foo/bar.git"
}`

const fixtureComment = `{
  "id": 999,
  "body": "looks good",
  "user": {"login": "alice", "username": "alice"},
  "html_url": "https://gitea.example.com/foo/bar/issues/1#issuecomment-999",
  "created_at": "2026-01-01T00:00:00Z"
}`

func newServer(t *testing.T, h http.HandlerFunc) (repohost.MutableHost, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	host, err := repohost.Open(context.Background(), repohost.Config{
		Provider: "gitea",
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
		if !strings.Contains(r.URL.Path, "/repos/foo/bar/pulls") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, "[%s]", fixturePR)
	})
	prs, err := host.ListPullRequests(context.Background(), "foo/bar", repohost.Filter{})
	if err != nil {
		t.Fatalf("ListPullRequests: %v", err)
	}
	if len(prs) != 1 || prs[0].Number != 1 || prs[0].State != "open" || prs[0].Author != "alice" {
		t.Errorf("PR fields wrong: %+v", prs)
	}
	if prs[0].HeadRef != "feature" || prs[0].BaseRef != "main" {
		t.Errorf("PR refs wrong: %+v", prs[0])
	}
	if len(prs[0].Labels) != 1 || prs[0].Labels[0] != "bug" {
		t.Errorf("PR labels = %v", prs[0].Labels)
	}
}

func TestHost_ListPullRequests_StateFilters(t *testing.T) {
	cases := []struct {
		name      string
		f         repohost.Filter
		wantState string
	}{
		// gitea encoder: open-only emits no state; closed-only "closed";
		// both "all".
		{"open-zero", repohost.Filter{}, ""},
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
		fmt.Fprintf(w, "[%s, %s]", fixturePRMerged, fixturePRClosed)
	})
	prs, err := host.ListPullRequests(context.Background(), "foo/bar", repohost.Filter{Closed: true})
	if err != nil {
		t.Fatalf("ListPullRequests: %v", err)
	}
	if len(prs) != 2 || prs[0].State != "merged" || prs[1].State != "closed" {
		t.Errorf("states wrong: %+v", prs)
	}
}

func TestHost_ListPullRequests_AuthorLabelFilter(t *testing.T) {
	host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "[%s, %s]", fixturePR, fixturePRMerged)
	})
	prs, err := host.ListPullRequests(context.Background(), "foo/bar", repohost.Filter{Open: true, Closed: true, Author: "bob"})
	if err != nil {
		t.Fatalf("ListPullRequests: %v", err)
	}
	if len(prs) != 1 || prs[0].Author != "bob" {
		t.Errorf("expected only bob's PR, got %+v", prs)
	}

	host2, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "[%s, %s]", fixturePR, fixturePRClosed)
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
		if got := r.URL.Query().Get("limit"); got != "1" {
			t.Errorf("limit = %q, want 1", got)
		}
		fmt.Fprintf(w, "[%s, %s]", fixturePR, fixturePRMerged)
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
	})
	_, err := host.ListPullRequests(context.Background(), "foo/bar", repohost.Filter{})
	if !errors.Is(err, repohost.ErrRepoNotFound) {
		t.Errorf("err = %v, want ErrRepoNotFound", err)
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
		fmt.Fprintf(w, "[%s]", fixtureIssue)
	})
	issues, err := host.ListIssues(context.Background(), "foo/bar", repohost.Filter{})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 1 || issues[0].Number != 10 {
		t.Errorf("issues wrong: %+v", issues)
	}
}

func TestHost_ListIssues_NotFound(t *testing.T) {
	host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	_, err := host.ListIssues(context.Background(), "foo/bar", repohost.Filter{})
	if !errors.Is(err, repohost.ErrRepoNotFound) {
		t.Errorf("err = %v, want ErrRepoNotFound", err)
	}
}

func TestHost_GetCommit(t *testing.T) {
	host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, fixtureCommit)
	})
	c, err := host.GetCommit(context.Background(), "foo/bar", "abc123")
	if err != nil {
		t.Fatalf("GetCommit: %v", err)
	}
	if c.SHA != "abc123" || c.Message != "first commit" {
		t.Errorf("commit fields wrong: %+v", c)
	}
}

func TestHost_GetCommit_NotFound(t *testing.T) {
	host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	_, err := host.GetCommit(context.Background(), "foo/bar", "abc")
	if !errors.Is(err, repohost.ErrCommitNotFound) {
		t.Errorf("err = %v, want ErrCommitNotFound", err)
	}
}

func TestHost_GetRepo(t *testing.T) {
	host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, fixtureRepo)
	})
	r, err := host.GetRepo(context.Background(), "foo/bar")
	if err != nil {
		t.Fatalf("GetRepo: %v", err)
	}
	if r.Owner != "foo" || r.Name != "bar" || r.DefaultBranch != "main" {
		t.Errorf("repo fields wrong: %+v", r)
	}
}

func TestHost_GetRepo_NotFound(t *testing.T) {
	host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
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
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, fixtureComment)
	})
	c, err := host.PostComment(context.Background(), "foo/bar", 1, "looks good")
	if err != nil {
		t.Fatalf("PostComment: %v", err)
	}
	if c.ID != 999 || c.Body != "looks good" {
		t.Errorf("comment fields wrong: %+v", c)
	}
}

func TestHost_PostComment_BothNotFound(t *testing.T) {
	host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
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

func TestOpen_BaseURLRequired(t *testing.T) {
	_, err := repohost.Open(context.Background(), repohost.Config{Provider: "gitea"})
	if err == nil {
		t.Fatal("expected error when BaseURL is empty")
	}
}

func TestOpen_TokenFromEnv(t *testing.T) {
	t.Setenv("GITEA_TOKEN", "envtoken")
	host, err := repohost.Open(context.Background(), repohost.Config{Provider: "gitea", BaseURL: "https://gitea.example.com"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if host == nil {
		t.Fatal("expected non-nil host")
	}
}
