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

| Code | Meaning |
|------|---------|
| 0 | verdict=pass |
| 2 | verdict=fail or ungradable |
| 3 | usage error, manifest parse, or rejected by the service |
| 4 | service unavailable or retry budget exhausted |
| 5 | auth failure or cassette pack error |

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
