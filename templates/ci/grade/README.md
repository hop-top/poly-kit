# conformance-grade — CI templates

Drop-in workflow templates that wire `kit conformance grade` into
common CI providers. Each file is adopter-ready; copy, rename to
match your provider's path, set the required secrets, commit.

| File | Provider | One-line |
|------|----------|----------|
| [github-actions.yml](github-actions.yml) | GitHub Actions | full workflow with PR-comment + Checks API status posting |
| [gitlab-ci.yml](gitlab-ci.yml) | GitLab CI | merge-request-only job |
| [buildkite.yml](buildkite.yml) | Buildkite | single-step pipeline fragment |
| [generic.sh](generic.sh) | any | minimal bash entrypoint |

Run `kit conformance grade --help` for the full flag surface and
exit-code contract.

## Required env / secrets

| Variable | Purpose |
|----------|---------|
| `KIT_CONFORMANCE_TOKEN` | bearer token for the grading service (secret) |
| `KIT_CONFORMANCE_SERVICE` | grade service URL — no default; the client refuses to run without it |
| `GITHUB_TOKEN` | only when `--pr-comment` / `--status-check` are passed; supplied automatically by Actions |

## Exit codes

Codes follow the 12fc taxonomy (0-6 shared classes, >6 documented
per-tool band):

| Code | Meaning |
|------|---------|
| 0 | verdict=pass |
| 1 | cassette pack error (general local failure) |
| 2 | usage error, manifest parse, or rejected by the service |
| 5 | auth failure |
| 6 | service unavailable or retry budget exhausted (transient — retry may clear) |
| 64 | rate-limited by the service (transient) |
| 68 | verdict=fail |
| 69 | verdict=ungradable |

## Per-provider gotchas

- **GitHub Actions**: fork PRs do not receive write-scoped
  `GITHUB_TOKEN` by default. `--pr-comment` / `--status-check` will
  silently soft-fail with a stderr warning; the exit code is still
  driven by the verdict.
- **GitLab CI**: mask `KIT_CONFORMANCE_TOKEN` in CI/CD Settings →
  Variables, otherwise it may appear in job logs.
- **Buildkite**: no automatic masking; use `buildkite-agent secret`
  or your secrets backend to inject the token.
- **Self-hosted svc**: point `KIT_CONFORMANCE_SERVICE` at your
  deployment. Air-gapped CI must use a self-hosted instance — there
  is no default kit-team-hosted endpoint.
