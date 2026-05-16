// Package gitea implements the [repohost.MutableHost] interface for
// self-hosted Gitea instances using the official code.gitea.io/sdk/gitea
// SDK.
//
// Auth: Config.Token takes priority. When empty, the driver reads the
// GITEA_TOKEN environment variable. When no token is found, the driver
// proceeds unauthenticated (limited to public repos and rate-limited).
//
// Self-hosted only: Gitea has no SaaS offering at a stable endpoint
// the driver can default to, so Config.BaseURL is REQUIRED and Open
// returns an error when it is empty.
//
// Reference: https://pkg.go.dev/code.gitea.io/sdk/gitea
package gitea
