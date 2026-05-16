# repohost authentication

> kit's repohost is a thin facade over [`github.com/drone/go-scm`];
> per-provider authentication conventions match what go-scm expects.
> The kit-public surface (`Host` / `MutableHost`, `Filter`,
> `PullRequest` / `Issue` / `Commit` / `Repo` / `Comment`, `Config`,
> sentinel errors, `Baseline()`, `ParseURL`) is preserved — only the
> driver implementations changed to use go-scm.

[`github.com/drone/go-scm`]: https://github.com/drone/go-scm

Each `go/integrations/repohost/` driver authenticates via a token
passed through `repohost.Config.Token`. When `Token` is empty, the
driver falls back to provider-specific environment variables. When
those are also empty, the driver proceeds unauthenticated and is
rate-limited by the provider.

This page lists the minimum scopes, env-var fallbacks, where to mint
tokens, and the rate-limit story per provider. Wire any of these via
the unified [`repohost.Config`](../../../go/integrations/repohost/config.go).

## GitHub

- **Token type**: PAT (classic or fine-grained), GitHub App
  installation token, or OAuth token.
- **Required scopes**: `repo` (read PRs/issues, post comments).
  For public-only access, no scopes needed (rate-limited).
- **Env var chain**: `GITHUB_TOKEN` → `GH_TOKEN` (matches the gh
  CLI fallback).
- **Self-hosted**: GitHub Enterprise Server (GHE) — set
  `Config.BaseURL` to the GHE root (driver appends `/api/v3` if
  missing).
- **Where to create**: <https://github.com/settings/tokens>
- **Rate limits**: 5,000 req/hour authenticated; 60/hour unauth.
  See breaker integration in
  [`docs/contributors/audits/breaker-primitives-audit.md`](../../audits/breaker-primitives-audit.md).
- **Provider docs**: <https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens>

## GitLab

- **Token type**: Personal Access Token (PAT). Project Access
  Tokens and Group Access Tokens also work for the same surface.
- **Required scopes**: `api` (full read+write); for read-only use
  `read_api` + `read_repository` (PostComment will fail without
  `api`).
- **Env var**: `GITLAB_TOKEN`.
- **Self-hosted**: GitLab CE/EE — set `Config.BaseURL` (driver
  appends `/api/v4`).
- **Where to create**: <https://gitlab.com/-/user_settings/personal_access_tokens>
  (or `<host>/-/user_settings/personal_access_tokens` for
  self-hosted instances).
- **Rate limits**: vary by GitLab edition; see
  <https://docs.gitlab.com/ee/user/gitlab_com/index.html#gitlabcom-specific-rate-limits>.
- **Provider docs**: <https://docs.gitlab.com/ee/user/profile/personal_access_tokens.html>

## Gitea

- **Token type**: API token (per-user).
- **Required scopes**: `read:repository`, `write:issue` (the latter
  also covers PR comments — Gitea uses the issue endpoint for
  both).
- **Env var**: `GITEA_TOKEN`.
- **Self-hosted**: Gitea is ALWAYS self-hosted — `Config.BaseURL`
  is REQUIRED (no SaaS default). Open() returns an error when
  BaseURL is empty.
- **Where to create**: `<your-gitea-host>/user/settings/applications`.
- **Rate limits**: instance-configured; no SaaS default.
- **Provider docs**: <https://docs.gitea.com/development/api-usage#authentication>

## Gitee

- **Token type**: Personal Access Token (PAT).
- **Required scopes**: `pull_requests`, `issues`, `notes` for the
  comment surface; `projects` for repo metadata. The kit driver
  uses the read+comment surface, so a PAT with these four scopes
  covers the v1 cap.
- **Env var**: `GITEE_TOKEN`.
- **Self-hosted**: Gitee Enterprise — set `Config.BaseURL` to the
  enterprise API root (`https://your-gitee-host/api/v5`). When
  `BaseURL` is empty, the driver targets `https://gitee.com/api/v5`
  (SaaS).
- **Where to create**: <https://gitee.com/profile/personal_access_tokens>
  (or `<host>/profile/personal_access_tokens` for self-hosted).
- **Rate limits**: 1,800 req/hour authenticated; 60/hour
  unauthenticated.
- **Issue numbers**: Gitee issue ids may be alphanumeric (e.g.
  `I12ABC`). go-scm encodes alphanumeric ids into a numeric `Number`
  by concatenating the ASCII codepoints; adopters that need the
  raw id should consult `Issue.Raw["issue_id"]` or parse the URL
  via [`ParseURL`].
- **Provider docs**: <https://gitee.com/api/v5/swagger>

[`ParseURL`]: ../../../go/integrations/repohost/url.go

## Bitbucket

- **Token type**: Atlassian API token (modern; replaces the
  deprecated app passwords) or OAuth token. Workspace tokens and
  Repository Access Tokens also work.
- **Required scopes**: `repository:read`, `pullrequest:write`
  (write covers comments).
- **Env var**: `BITBUCKET_TOKEN`.
- **Auth**: tokens are applied as Bearer auth via go-scm's
  transport package. Callers only set `Config.Token`.
- **Self-hosted**: Bitbucket Server is OUT OF SCOPE — Bitbucket
  Cloud only. Bitbucket Server has a different API surface (v1.0
  not v2.0) and would need a separate driver name (e.g.
  `bitbucket-server`) delegating to go-scm's `stash` driver.
- **Issues**: go-scm's bitbucket driver does not implement issue
  endpoints; `ListIssues` returns an empty slice + nil error
  (graceful — adopters that need issues should use `GetRepo` to
  detect the repo's existence).
- **Where to create**:
  <https://bitbucket.org/account/settings/app-passwords/> (legacy
  app passwords) or workspace settings (modern API tokens):
  <https://support.atlassian.com/bitbucket-cloud/docs/access-tokens/>.
- **Rate limits**: 1,000 req/hour authenticated.
- **Provider docs**: <https://support.atlassian.com/bitbucket-cloud/docs/access-tokens/>

## Local development

Export tokens via `direnv` (`.envrc` in your project root). The
example below pulls each token from 1Password via the `op` CLI; any
secret store works.

```sh
export GITHUB_TOKEN=$(op read "op://Personal/GitHub PAT/token")
export GITLAB_TOKEN=$(op read "op://Personal/GitLab PAT/token")
export GITEA_TOKEN=$(op read "op://Personal/Gitea token/token")
export GITEE_TOKEN=$(op read "op://Personal/Gitee PAT/token")
export BITBUCKET_TOKEN=$(op read "op://Personal/Bitbucket token/token")
```

For CI: use the platform-native secret store (GitHub Actions
secrets, GitLab CI variables, etc.). Don't commit tokens, even
fine-grained ones.

## Resolution order

Each driver applies the same precedence:

1. `Config.Token` (programmatic) wins.
2. Provider env var (`GITHUB_TOKEN`/`GH_TOKEN`, `GITLAB_TOKEN`,
   `GITEA_TOKEN`, `GITEE_TOKEN`, `BITBUCKET_TOKEN`).
3. No auth — driver issues unauthenticated requests, subject to the
   provider's anonymous rate limit.

For Gitea, step 3 still requires `Config.BaseURL` (no SaaS default).

## See also

- [`docs/contributors/specs/integrations-repo-host.md`](../../specs/integrations-repo-host.md)
  — full spec, including the unified-types contract.
- [`go/integrations/repohost/config.go`](../../../go/integrations/repohost/config.go)
  — `Config` shape and field semantics.
- [`docs/contributors/audits/breaker-primitives-audit.md`](../../audits/breaker-primitives-audit.md)
  — how the breaker integration handles 429s across drivers.
