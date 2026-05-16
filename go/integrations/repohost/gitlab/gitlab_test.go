package gitlab_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hop.top/kit/go/integrations/repohost"
	_ "hop.top/kit/go/integrations/repohost/gitlab"
)

// fixtureMR is GitLab's native merge-request shape (project IID = 1).
const fixtureMR = `{
  "id": 100,
  "iid": 1,
  "title": "Add feature",
  "description": "body text",
  "state": "opened",
  "web_url": "https://gitlab.com/foo/bar/-/merge_requests/1",
  "source_branch": "feature",
  "target_branch": "main",
  "labels": ["bug"],
  "work_in_progress": false,
  "author": {"username": "alice", "name": "Alice"},
  "created_at": "2026-01-01T00:00:00Z",
  "updated_at": "2026-01-02T00:00:00Z"
}`

// fixtureMRMerged uses GitLab's native "merged" state.
const fixtureMRMerged = `{
  "id": 101,
  "iid": 2,
  "title": "Old feature",
  "state": "merged",
  "web_url": "https://gitlab.com/foo/bar/-/merge_requests/2",
  "source_branch": "feature2",
  "target_branch": "main",
  "labels": [],
  "author": {"username": "bob"},
  "created_at": "2026-01-01T00:00:00Z",
  "updated_at": "2026-01-03T00:00:00Z"
}`

const fixtureMRClosed = `{
  "id": 102,
  "iid": 3,
  "title": "Abandoned",
  "state": "closed",
  "web_url": "https://gitlab.com/foo/bar/-/merge_requests/3",
  "source_branch": "feature3",
  "target_branch": "main",
  "labels": [],
  "author": {"username": "carol"},
  "created_at": "2026-01-01T00:00:00Z",
  "updated_at": "2026-01-04T00:00:00Z"
}`

const fixtureGLIssue = `{
  "id": 200,
  "iid": 10,
  "title": "Bug found",
  "state": "opened",
  "web_url": "https://gitlab.com/foo/bar/-/issues/10",
  "labels": ["bug"],
  "author": {"username": "alice"},
  "created_at": "2026-01-01T00:00:00Z",
  "updated_at": "2026-01-02T00:00:00Z"
}`

const fixtureGLCommit = `{
  "id": "abc123def",
  "short_id": "abc123",
  "title": "first commit",
  "message": "first commit",
  "author_name": "alice",
  "author_email": "alice@example.test",
  "authored_date": "2026-01-01T00:00:00Z",
  "web_url": "https://gitlab.com/foo/bar/-/commit/abc123def"
}`

const fixtureGLProject = `{
  "id": 42,
  "name": "bar",
  "path": "bar",
  "path_with_namespace": "foo/bar",
  "default_branch": "main",
  "web_url": "https://gitlab.com/foo/bar",
  "ssh_url_to_repo": "git@gitlab.com:foo/bar.git",
  "http_url_to_repo": "https://gitlab.com/foo/bar.git",
  "visibility": "public",
  "archived": false,
  "namespace": {
    "id": 1, "name": "foo", "path": "foo", "kind": "user", "full_path": "foo"
  }
}`

const fixtureGLNote = `{
  "id": 555,
  "body": "looks good",
  "author": {"username": "alice", "name": "Alice"},
  "created_at": "2026-01-01T00:00:00Z"
}`

func newServer(t *testing.T, h http.HandlerFunc) (repohost.MutableHost, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	host, err := repohost.Open(context.Background(), repohost.Config{
		Provider: "gitlab",
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
		// go-scm encodes "foo/bar" as foo%2Fbar in the URL path.
		if !strings.Contains(r.URL.RawPath+r.URL.Path, "merge_requests") {
			t.Errorf("path missing merge_requests: %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, "[%s]", fixtureMR)
	})
	prs, err := host.ListPullRequests(context.Background(), "foo/bar", repohost.Filter{})
	if err != nil {
		t.Fatalf("ListPullRequests: %v", err)
	}
	if len(prs) != 1 {
		t.Fatalf("want 1 MR, got %d", len(prs))
	}
	got := prs[0]
	if got.Number != 1 || got.Title != "Add feature" || got.Author != "alice" || got.State != "open" {
		t.Errorf("MR fields wrong: %+v", got)
	}
	if got.HeadRef != "feature" || got.BaseRef != "main" {
		t.Errorf("MR refs wrong: head=%q base=%q", got.HeadRef, got.BaseRef)
	}
	if len(got.Labels) != 1 || got.Labels[0] != "bug" {
		t.Errorf("MR labels = %v", got.Labels)
	}
}

func TestHost_ListPullRequests_StateFilters(t *testing.T) {
	cases := []struct {
		name      string
		f         repohost.Filter
		wantState string
	}{
		// go-scm gitlab encoder: open-only emits "opened";
		// closed-only emits "closed"; both emit "all".
		{"open-zero", repohost.Filter{}, "opened"},
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
		fmt.Fprintf(w, "[%s, %s]", fixtureMRMerged, fixtureMRClosed)
	})
	prs, err := host.ListPullRequests(context.Background(), "foo/bar", repohost.Filter{Closed: true})
	if err != nil {
		t.Fatalf("ListPullRequests: %v", err)
	}
	if len(prs) != 2 {
		t.Fatalf("want 2 MRs, got %d", len(prs))
	}
	if prs[0].State != "merged" {
		t.Errorf("MR0 state = %q, want merged", prs[0].State)
	}
	if prs[1].State != "closed" {
		t.Errorf("MR1 state = %q, want closed", prs[1].State)
	}
}

func TestHost_ListPullRequests_AuthorLabelFilter(t *testing.T) {
	host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "[%s, %s]", fixtureMR, fixtureMRMerged)
	})
	prs, err := host.ListPullRequests(context.Background(), "foo/bar", repohost.Filter{Open: true, Closed: true, Author: "bob"})
	if err != nil {
		t.Fatalf("ListPullRequests: %v", err)
	}
	if len(prs) != 1 || prs[0].Author != "bob" {
		t.Errorf("expected only bob's MR, got %+v", prs)
	}

	host2, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "[%s, %s]", fixtureMR, fixtureMRClosed)
	})
	prs2, err := host2.ListPullRequests(context.Background(), "foo/bar", repohost.Filter{Open: true, Closed: true, Label: "bug"})
	if err != nil {
		t.Fatalf("ListPullRequests: %v", err)
	}
	if len(prs2) != 1 || prs2[0].Number != 1 {
		t.Errorf("expected only MR with bug label, got %+v", prs2)
	}
}

func TestHost_ListPullRequests_Limit(t *testing.T) {
	host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("per_page"); got != "1" {
			t.Errorf("per_page = %q, want 1", got)
		}
		fmt.Fprintf(w, "[%s, %s]", fixtureMR, fixtureMRMerged)
	})
	prs, err := host.ListPullRequests(context.Background(), "foo/bar", repohost.Filter{Open: true, Limit: 1})
	if err != nil {
		t.Fatalf("ListPullRequests: %v", err)
	}
	if len(prs) != 1 {
		t.Errorf("limit not honored: got %d MRs", len(prs))
	}
}

func TestHost_ListPullRequests_NotFound(t *testing.T) {
	host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"message": "404 Not Found"}`)
	})
	_, err := host.ListPullRequests(context.Background(), "foo/bar", repohost.Filter{})
	if !errors.Is(err, repohost.ErrRepoNotFound) {
		t.Errorf("err = %v, want ErrRepoNotFound", err)
	}
}

func TestHost_ListIssues(t *testing.T) {
	host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "[%s]", fixtureGLIssue)
	})
	issues, err := host.ListIssues(context.Background(), "foo/bar", repohost.Filter{})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 1 || issues[0].Number != 10 {
		t.Errorf("issues wrong: %+v", issues)
	}
	if issues[0].State != "open" {
		t.Errorf("issue state = %q, want open", issues[0].State)
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
		fmt.Fprint(w, fixtureGLCommit)
	})
	c, err := host.GetCommit(context.Background(), "foo/bar", "abc123def")
	if err != nil {
		t.Fatalf("GetCommit: %v", err)
	}
	if c.SHA != "abc123def" || c.Author != "alice" || c.Email != "alice@example.test" {
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
		fmt.Fprint(w, fixtureGLProject)
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
		fmt.Fprint(w, fixtureGLNote)
	})
	c, err := host.PostComment(context.Background(), "foo/bar", 1, "looks good")
	if err != nil {
		t.Fatalf("PostComment: %v", err)
	}
	if c.ID != 555 || c.Body != "looks good" {
		t.Errorf("comment fields wrong: %+v", c)
	}
	if !strings.Contains(c.URL, "#note_555") {
		t.Errorf("comment URL missing note anchor: %q", c.URL)
	}
}

func TestHost_PostComment_FallsBackToIssue(t *testing.T) {
	var hits int
	host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		if strings.Contains(r.URL.Path, "merge_requests") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, fixtureGLNote)
	})
	c, err := host.PostComment(context.Background(), "foo/bar", 1, "looks good")
	if err != nil {
		t.Fatalf("PostComment: %v", err)
	}
	if c.ID != 555 {
		t.Errorf("comment ID = %d", c.ID)
	}
	if hits < 2 {
		t.Errorf("expected fallback to issue endpoint; hits = %d", hits)
	}
	if !strings.Contains(c.URL, "/issues/") {
		t.Errorf("expected issue-flavored URL, got %q", c.URL)
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

func TestOpen_ProviderMismatch(t *testing.T) {
	_, err := repohost.Open(context.Background(), repohost.Config{Provider: "gitlab-bogus"})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestOpen_TokenFromEnv(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "envtoken")
	host, err := repohost.Open(context.Background(), repohost.Config{Provider: "gitlab"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if host == nil {
		t.Fatal("expected non-nil host")
	}
}
