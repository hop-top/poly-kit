package repohost_test

import (
	"testing"

	"hop.top/kit/go/integrations/repohost"
)

func TestParseURL_GitHub(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want repohost.ParsedURL
	}{
		{
			name: "repo",
			raw:  "https://github.com/foo/bar",
			want: repohost.ParsedURL{Provider: "github", BaseURL: "https://github.com", Owner: "foo", Repo: "bar", Kind: "repo"},
		},
		{
			name: "pull",
			raw:  "https://github.com/foo/bar/pull/42",
			want: repohost.ParsedURL{Provider: "github", BaseURL: "https://github.com", Owner: "foo", Repo: "bar", Kind: "pull", Number: 42},
		},
		{
			name: "issue",
			raw:  "https://github.com/foo/bar/issues/7",
			want: repohost.ParsedURL{Provider: "github", BaseURL: "https://github.com", Owner: "foo", Repo: "bar", Kind: "issue", Number: 7},
		},
		{
			name: "commit",
			raw:  "https://github.com/foo/bar/commit/abc123",
			want: repohost.ParsedURL{Provider: "github", BaseURL: "https://github.com", Owner: "foo", Repo: "bar", Kind: "commit", SHA: "abc123"},
		},
		{
			name: "ghe-self-hosted",
			raw:  "https://github.example.com/foo/bar/pull/9",
			want: repohost.ParsedURL{Provider: "github", BaseURL: "https://github.example.com", Owner: "foo", Repo: "bar", Kind: "pull", Number: 9},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repohost.ParseURL(tc.raw)
			if err != nil {
				t.Fatalf("ParseURL: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseURL_GitLab(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want repohost.ParsedURL
	}{
		{
			name: "repo",
			raw:  "https://gitlab.com/foo/bar",
			want: repohost.ParsedURL{Provider: "gitlab", BaseURL: "https://gitlab.com", Owner: "foo", Repo: "bar", Kind: "repo"},
		},
		{
			name: "subgroup-repo",
			raw:  "https://gitlab.com/grp/sub/bar",
			want: repohost.ParsedURL{Provider: "gitlab", BaseURL: "https://gitlab.com", Owner: "grp/sub", Repo: "bar", Kind: "repo"},
		},
		{
			name: "merge-request",
			raw:  "https://gitlab.com/foo/bar/-/merge_requests/12",
			want: repohost.ParsedURL{Provider: "gitlab", BaseURL: "https://gitlab.com", Owner: "foo", Repo: "bar", Kind: "pull", Number: 12},
		},
		{
			name: "subgroup-merge-request",
			raw:  "https://gitlab.com/grp/sub/bar/-/merge_requests/3",
			want: repohost.ParsedURL{Provider: "gitlab", BaseURL: "https://gitlab.com", Owner: "grp/sub", Repo: "bar", Kind: "pull", Number: 3},
		},
		{
			name: "issue",
			raw:  "https://gitlab.com/foo/bar/-/issues/7",
			want: repohost.ParsedURL{Provider: "gitlab", BaseURL: "https://gitlab.com", Owner: "foo", Repo: "bar", Kind: "issue", Number: 7},
		},
		{
			name: "commit",
			raw:  "https://gitlab.com/foo/bar/-/commit/abc123",
			want: repohost.ParsedURL{Provider: "gitlab", BaseURL: "https://gitlab.com", Owner: "foo", Repo: "bar", Kind: "commit", SHA: "abc123"},
		},
		{
			name: "self-hosted",
			raw:  "https://gitlab.example.com/foo/bar/-/merge_requests/1",
			want: repohost.ParsedURL{Provider: "gitlab", BaseURL: "https://gitlab.example.com", Owner: "foo", Repo: "bar", Kind: "pull", Number: 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repohost.ParseURL(tc.raw)
			if err != nil {
				t.Fatalf("ParseURL: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseURL_Gitea(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want repohost.ParsedURL
	}{
		{
			name: "repo-saas",
			raw:  "https://gitea.com/foo/bar",
			want: repohost.ParsedURL{Provider: "gitea", BaseURL: "https://gitea.com", Owner: "foo", Repo: "bar", Kind: "repo"},
		},
		{
			name: "self-hosted-pull",
			raw:  "https://gitea.example.com/foo/bar/pulls/1",
			want: repohost.ParsedURL{Provider: "gitea", BaseURL: "https://gitea.example.com", Owner: "foo", Repo: "bar", Kind: "pull", Number: 1},
		},
		{
			name: "issue",
			raw:  "https://gitea.example.com/foo/bar/issues/2",
			want: repohost.ParsedURL{Provider: "gitea", BaseURL: "https://gitea.example.com", Owner: "foo", Repo: "bar", Kind: "issue", Number: 2},
		},
		{
			name: "commit",
			raw:  "https://gitea.example.com/foo/bar/commit/abc123",
			want: repohost.ParsedURL{Provider: "gitea", BaseURL: "https://gitea.example.com", Owner: "foo", Repo: "bar", Kind: "commit", SHA: "abc123"},
		},
		{
			name: "vanity-domain-via-pulls-segment",
			raw:  "https://code.example.io/foo/bar/pulls/9",
			want: repohost.ParsedURL{Provider: "gitea", BaseURL: "https://code.example.io", Owner: "foo", Repo: "bar", Kind: "pull", Number: 9},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repohost.ParseURL(tc.raw)
			if err != nil {
				t.Fatalf("ParseURL: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseURL_Gitee(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want repohost.ParsedURL
	}{
		{
			name: "repo",
			raw:  "https://gitee.com/foo/bar",
			want: repohost.ParsedURL{Provider: "gitee", BaseURL: "https://gitee.com", Owner: "foo", Repo: "bar", Kind: "repo"},
		},
		{
			name: "pull",
			raw:  "https://gitee.com/foo/bar/pulls/1",
			want: repohost.ParsedURL{Provider: "gitee", BaseURL: "https://gitee.com", Owner: "foo", Repo: "bar", Kind: "pull", Number: 1},
		},
		{
			name: "issue-numeric",
			raw:  "https://gitee.com/foo/bar/issues/7",
			want: repohost.ParsedURL{Provider: "gitee", BaseURL: "https://gitee.com", Owner: "foo", Repo: "bar", Kind: "issue", Number: 7},
		},
		{
			name: "issue-alphanumeric",
			// Gitee issue ids may be alphanumeric (e.g. "I12ABC"). The
			// parser sets Number=0 and leaves the raw id discoverable
			// from the URL path.
			raw:  "https://gitee.com/foo/bar/issues/I12ABC",
			want: repohost.ParsedURL{Provider: "gitee", BaseURL: "https://gitee.com", Owner: "foo", Repo: "bar", Kind: "issue"},
		},
		{
			name: "commit",
			raw:  "https://gitee.com/foo/bar/commit/abc123",
			want: repohost.ParsedURL{Provider: "gitee", BaseURL: "https://gitee.com", Owner: "foo", Repo: "bar", Kind: "commit", SHA: "abc123"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repohost.ParseURL(tc.raw)
			if err != nil {
				t.Fatalf("ParseURL: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestParseURL_GiteeNotMatchedAsGitea pins the precedence rule:
// gitee.com must NOT be classified as "gitea" by the host-substring
// check.
func TestParseURL_GiteeNotMatchedAsGitea(t *testing.T) {
	got, err := repohost.ParseURL("https://gitee.com/foo/bar/pulls/9")
	if err != nil {
		t.Fatalf("ParseURL: %v", err)
	}
	if got.Provider != "gitee" {
		t.Errorf("provider = %q, want gitee", got.Provider)
	}
}

func TestParseURL_Bitbucket(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want repohost.ParsedURL
	}{
		{
			name: "repo",
			raw:  "https://bitbucket.org/foo/bar",
			want: repohost.ParsedURL{Provider: "bitbucket", BaseURL: "https://bitbucket.org", Owner: "foo", Repo: "bar", Kind: "repo"},
		},
		{
			name: "pull-requests",
			raw:  "https://bitbucket.org/foo/bar/pull-requests/3",
			want: repohost.ParsedURL{Provider: "bitbucket", BaseURL: "https://bitbucket.org", Owner: "foo", Repo: "bar", Kind: "pull", Number: 3},
		},
		{
			name: "issues",
			raw:  "https://bitbucket.org/foo/bar/issues/5",
			want: repohost.ParsedURL{Provider: "bitbucket", BaseURL: "https://bitbucket.org", Owner: "foo", Repo: "bar", Kind: "issue", Number: 5},
		},
		{
			name: "commits",
			raw:  "https://bitbucket.org/foo/bar/commits/abc123",
			want: repohost.ParsedURL{Provider: "bitbucket", BaseURL: "https://bitbucket.org", Owner: "foo", Repo: "bar", Kind: "commit", SHA: "abc123"},
		},
		{
			name: "self-hosted",
			raw:  "https://bitbucket.example.com/foo/bar/pull-requests/2",
			want: repohost.ParsedURL{Provider: "bitbucket", BaseURL: "https://bitbucket.example.com", Owner: "foo", Repo: "bar", Kind: "pull", Number: 2},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repohost.ParseURL(tc.raw)
			if err != nil {
				t.Fatalf("ParseURL: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseURL_Errors(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"empty", ""},
		{"no-scheme", "github.com/foo/bar"},
		{"no-path", "https://github.com"},
		{"unrecognized-host", "https://example.com/foo/bar"},
		{"github-bad-pull-number", "https://github.com/foo/bar/pull/abc"},
		{"gitlab-no-owner", "https://gitlab.com/foo"},
		{"gitlab-bad-mr-number", "https://gitlab.com/foo/bar/-/merge_requests/abc"},
		{"gitea-bad-pulls-number", "https://gitea.com/foo/bar/pulls/abc"},
		{"bitbucket-bad-pr-number", "https://bitbucket.org/foo/bar/pull-requests/abc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := repohost.ParseURL(tc.raw); err == nil {
				t.Fatalf("expected error for %q", tc.raw)
			}
		})
	}
}
