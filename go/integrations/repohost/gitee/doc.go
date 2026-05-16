// Package gitee implements the [repohost.MutableHost] surface for
// Gitee.com (and self-hosted Gitee instances) as a thin facade over
// github.com/drone/go-scm.
//
// Provider name: "gitee". BaseURL defaults to https://gitee.com/api/v5
// (Gitee SaaS); set Config.BaseURL for self-hosted Gitee Enterprise.
//
// Token resolution order: Config.Token, then GITEE_TOKEN. Tokens are
// applied as Bearer auth via go-scm's transport package.
//
// Gitee's issue numbers are alphanumeric (e.g. "I12ABC"). go-scm's
// Gitee driver normalizes Issue.Number when it's purely numeric;
// alphanumeric IDs are preserved in Raw["issue_id"] for adopters
// that need them.
package gitee
