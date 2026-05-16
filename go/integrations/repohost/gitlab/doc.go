// Package gitlab implements the [repohost.MutableHost] interface
// for GitLab.com and self-hosted GitLab instances using the
// go-gitlab SDK.
//
// Auth: Config.Token takes priority. When empty, the driver reads
// GITLAB_TOKEN. When no token is found, the driver proceeds
// unauthenticated and is rate-limited by GitLab.
//
// Self-hosted: set Config.BaseURL to your GitLab host root (e.g.
// "https://gitlab.example.com"); the driver appends /api/v4 to
// reach the REST endpoint.
package gitlab
