package integrations_test

// Gap test for the missing kit/integrations/repo-host adapter.
// Same convention: t.Skip + pin until the gap is closed.

import "testing"

// Gap: kit/integrations/repo-host adapter does not exist.
//
// rsx ships ~482 LOC of GitHub client; tlc has 8 sync plugins for
// the same set of repo hosts (GitHub, GitLab, Gitea, Bitbucket).
// The pattern is identical: auth via token-from-env or PAT, list
// PRs/issues, post comments, fetch commit metadata. A shared
// adapter under kit/integrations/repo-host would let both tools
// drop their per-host glue.
//
// Desired API shape (sketch):
//
//	host, err := repohost.Open(ctx, repohost.Options{
//	    Provider: "github",      // or "gitlab", "gitea", "bitbucket"
//	    Token:    secret.Get(ctx, "GITHUB_TOKEN"),
//	})
//	prs, err := host.ListPullRequests(ctx, "hop-top/kit", repohost.Filter{Open: true})
//
// with a small unified type set (PullRequest, Issue, Commit) and
// per-provider drivers under repohost/{github,gitlab,gitea,bitbucket}.
func TestGap_RepoHostAdapter_Missing(t *testing.T) {
	t.Skip("gap: kit/integrations/repo-host adapter not implemented; rsx (~482 LOC GitHub client) + tlc (8 sync plugins) reimplement the same pattern")

	// Pin: this package exists as a placeholder — when the adapter
	// ships, this test should:
	//   1. import "hop.top/kit/go/integrations/repohost"
	//   2. Exercise Open() against a mock or recorded cassette
}
