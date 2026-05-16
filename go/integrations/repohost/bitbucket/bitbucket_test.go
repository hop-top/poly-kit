package bitbucket_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hop.top/kit/go/integrations/repohost"
	_ "hop.top/kit/go/integrations/repohost/bitbucket"
)

const fixturePR = `{
  "id": 1,
  "title": "Add feature",
  "description": "body text",
  "state": "OPEN",
  "links": {
    "html": {"href": "https://bitbucket.org/foo/bar/pull-requests/1"},
    "diff": {"href": "https://api.bitbucket.org/foo/bar/diff/1"}
  },
  "source": {
    "branch": {"name": "feature"},
    "commit": {"hash": "deadbeef"},
    "repository": {"full_name": "foo/bar"}
  },
  "destination": {
    "branch": {"name": "main"},
    "commit": {"hash": "cafef00d"}
  },
  "author": {"display_name": "alice", "account_id": "A1"},
  "created_on": "2026-01-01T00:00:00Z",
  "updated_on": "2026-01-02T00:00:00Z"
}`

const fixturePRMerged = `{
  "id": 2,
  "title": "Old feature",
  "state": "MERGED",
  "links": {"html": {"href": "https://bitbucket.org/foo/bar/pull-requests/2"}},
  "source": {"branch": {"name": "feature2"}},
  "destination": {"branch": {"name": "main"}},
  "merge_commit": {"hash": "abcdef"},
  "author": {"display_name": "bob", "account_id": "B1"},
  "created_on": "2026-01-01T00:00:00Z",
  "updated_on": "2026-01-03T00:00:00Z"
}`

const fixturePRDeclined = `{
  "id": 3,
  "title": "Abandoned",
  "state": "DECLINED",
  "links": {"html": {"href": "https://bitbucket.org/foo/bar/pull-requests/3"}},
  "source": {"branch": {"name": "feature3"}},
  "destination": {"branch": {"name": "main"}},
  "author": {"display_name": "carol", "account_id": "C1"},
  "created_on": "2026-01-01T00:00:00Z",
  "updated_on": "2026-01-04T00:00:00Z"
}`

const fixtureCommit = `{
  "hash": "abc123",
  "links": {
    "html": {"href": "https://bitbucket.org/foo/bar/commits/abc123"}
  },
  "message": "first commit",
  "author": {
    "raw": "alice <alice@example.test>",
    "user": {"display_name": "alice", "nickname": "alice"}
  },
  "date": "2026-01-01T00:00:00Z"
}`

const fixtureRepo = `{
  "uuid": "{abcd-1234}",
  "name": "bar",
  "full_name": "foo/bar",
  "description": "an example",
  "is_private": false,
  "links": {
    "html": {"href": "https://bitbucket.org/foo/bar"},
    "clone": [
      {"name": "https", "href": "https://bitbucket.org/foo/bar.git"},
      {"name": "ssh", "href": "git@bitbucket.org:foo/bar.git"}
    ]
  },
  "mainbranch": {"name": "main"}
}`

const fixturePRComment = `{
  "id": 999,
  "content": {"raw": "looks good"},
  "user": {"display_name": "alice", "nickname": "alice"},
  "created_on": "2026-01-01T00:00:00Z"
}`

func newServer(t *testing.T, h http.HandlerFunc) (repohost.MutableHost, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	host, err := repohost.Open(context.Background(), repohost.Config{
		Provider: "bitbucket",
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
		if !strings.Contains(r.URL.Path, "/repositories/foo/bar/pullrequests") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"values": [%s]}`, fixturePR)
	})
	prs, err := host.ListPullRequests(context.Background(), "foo/bar", repohost.Filter{})
	if err != nil {
		t.Fatalf("ListPullRequests: %v", err)
	}
	if len(prs) != 1 || prs[0].Number != 1 || prs[0].State != "open" {
		t.Errorf("PR fields wrong: %+v", prs)
	}
	if prs[0].HeadRef != "feature" || prs[0].BaseRef != "main" {
		t.Errorf("PR refs wrong: %+v", prs[0])
	}
}

func TestHost_ListPullRequests_StateFilters(t *testing.T) {
	cases := []struct {
		name      string
		f         repohost.Filter
		wantState string
	}{
		// bitbucket encoder: open-only emits no state; closed-only "closed";
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
				fmt.Fprintf(w, `{"values": []}`)
			})
			if _, err := host.ListPullRequests(context.Background(), "foo/bar", tc.f); err != nil {
				t.Fatalf("ListPullRequests: %v", err)
			}
		})
	}
}

func TestHost_ListPullRequests_MergedState(t *testing.T) {
	host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"values": [%s, %s]}`, fixturePRMerged, fixturePRDeclined)
	})
	prs, err := host.ListPullRequests(context.Background(), "foo/bar", repohost.Filter{Closed: true})
	if err != nil {
		t.Fatalf("ListPullRequests: %v", err)
	}
	if len(prs) != 2 || prs[0].State != "merged" || prs[1].State != "closed" {
		t.Errorf("states wrong: %+v", prs)
	}
}

func TestHost_ListPullRequests_AuthorFilter(t *testing.T) {
	host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"values": [%s, %s]}`, fixturePR, fixturePRMerged)
	})
	// Author lookup uses display_name fallback (Bitbucket has no Login).
	prs, err := host.ListPullRequests(context.Background(), "foo/bar", repohost.Filter{Open: true, Closed: true, Author: "bob"})
	if err != nil {
		t.Fatalf("ListPullRequests: %v", err)
	}
	if len(prs) != 1 || prs[0].Author != "bob" {
		t.Errorf("expected only bob's PR, got %+v", prs)
	}
}

func TestHost_ListPullRequests_Limit(t *testing.T) {
	host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("pagelen"); got != "1" {
			t.Errorf("pagelen = %q, want 1", got)
		}
		fmt.Fprintf(w, `{"values": [%s, %s]}`, fixturePR, fixturePRMerged)
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

func TestHost_ListIssues_NotSupported(t *testing.T) {
	// Server is never hit because go-scm short-circuits with
	// ErrNotSupported on Bitbucket Cloud's issue routes.
	host, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {})
	issues, err := host.ListIssues(context.Background(), "foo/bar", repohost.Filter{})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if issues == nil || len(issues) != 0 {
		t.Errorf("want empty slice, got %+v", issues)
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
	if c.SHA != "abc123" {
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
		fmt.Fprint(w, fixturePRComment)
	})
	c, err := host.PostComment(context.Background(), "foo/bar", 1, "looks good")
	if err != nil {
		t.Fatalf("PostComment: %v", err)
	}
	if c.ID != 999 || c.Body != "looks good" {
		t.Errorf("comment fields wrong: %+v", c)
	}
}

func TestHost_PostComment_NotFound(t *testing.T) {
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
	_, err := repohost.Open(context.Background(), repohost.Config{Provider: "bitbucket-bogus"})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestOpen_TokenFromEnv(t *testing.T) {
	t.Setenv("BITBUCKET_TOKEN", "envtoken")
	host, err := repohost.Open(context.Background(), repohost.Config{Provider: "bitbucket"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if host == nil {
		t.Fatal("expected non-nil host")
	}
}
