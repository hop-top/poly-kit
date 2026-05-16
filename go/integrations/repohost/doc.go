// Package repohost defines a unified driver SPI for repository-host
// providers. Drivers are thin facades over
// github.com/drone/go-scm; kit owns the unified types
// ([PullRequest], [Issue], [Commit], [Repo], [Comment]),
// the [Host] / [MutableHost] interfaces, the [Config] +
// [RegisterDriver] + [Open] registry, the [ParseURL] helper,
// and the per-driver `mock/` sub-packages used in unit tests.
//
// Supported providers: "github", "gitlab", "gitea", "gitee",
// "bitbucket" (Bitbucket Cloud only).
//
// Adopters open a host via [Open] with a [Config] selecting one of
// the registered drivers. Drivers register themselves through their
// own init() functions; users blank-import the driver they want:
//
//	import (
//	    "hop.top/kit/go/integrations/repohost"
//	    _ "hop.top/kit/go/integrations/repohost/github"
//	)
//
//	host, err := repohost.Open(ctx, repohost.Config{
//	    Provider: "github",
//	    Token:    os.Getenv("GITHUB_TOKEN"),
//	})
//	prs, err := host.ListPullRequests(ctx, "owner/repo", repohost.Filter{Open: true})
//
// The [Host] interface covers read operations (list PRs/issues, fetch
// commit + repo metadata); [MutableHost] adds the comment/label
// write surface.
//
// # Mock convention
//
// Every driver under repohost/<provider>/ ships a sibling
// repohost/<provider>/mock/ sub-package that registers itself under
// provider name "<provider>-mock" and exposes typed knobs (e.g.
// SetPullRequests, SetIssues, SetError) for adopter unit tests. The
// mock returns deterministic [Baseline] values by default and
// supports per-method overrides.
//
// # Testing policy
//
// Adopters: use <provider>/mock sub-packages for unit tests; use xrr
// cassettes for integration / e2e. The mock layer is fast and
// offline; cassettes catch real-format drift in a slower-running
// layer.
package repohost
