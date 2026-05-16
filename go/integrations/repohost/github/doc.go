// Package github implements the [repohost.MutableHost] interface
// for GitHub.com and GitHub Enterprise Server (GHE) using the
// go-github SDK.
//
// Auth: Config.Token takes priority. When empty, the driver reads
// GITHUB_TOKEN, then GH_TOKEN (matching the gh CLI fallback). When
// no token is found, the driver proceeds unauthenticated and is
// rate-limited by GitHub.
//
// Self-hosted: set Config.BaseURL to your GHE host root (e.g.
// "https://github.example.com"); the driver appends /api/v3 if not
// already present.
package github
