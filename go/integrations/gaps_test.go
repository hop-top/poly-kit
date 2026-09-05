package integrations_test

// Closed-gap test for the kit/integrations/repo-host adapter. The
// adapter shipped as go/integrations/repohost, so the Skip+pin form
// is retired in favor of a real assertion against the public API.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"hop.top/kit/go/integrations/repohost"
	_ "hop.top/kit/go/integrations/repohost/github/mock"
)

// Closed: kit/integrations/repo-host adapter exists.
//
// rsx shipped ~482 LOC of GitHub client and tlc 8 sync plugins for
// the same set of repo hosts before repohost unified them behind a
// driver SPI: unified types (PullRequest, Issue, Commit, Repo,
// Comment), the Host/MutableHost interfaces, and a
// Config+RegisterDriver+Open registry with drivers for github,
// gitlab, gitea, gitee and bitbucket.
//
// This test pins the shape the gap asked for: Open() a provider by
// name and list its pull requests through the unified types.
func TestGap_RepoHostAdapter_Missing(t *testing.T) {
	host, err := repohost.Open(context.Background(), repohost.Config{
		Provider: "github-mock",
		Token:    "test-token",
	})
	require.NoError(t, err)
	require.NotNil(t, host)

	prs, err := host.ListPullRequests(context.Background(), "hop-top/kit", repohost.Filter{Open: true})
	require.NoError(t, err)
	require.NotEmpty(t, prs)
}
