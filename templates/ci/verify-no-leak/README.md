# verify-no-leak — CI templates

Drop-in workflow templates that wire `kit conformance verify-no-leak`
into common CI providers. Each file is adopter-ready; copy, rename to
match your provider's path, and commit.

| File | Provider | One-line |
|------|----------|----------|
| [github-actions.yml](github-actions.yml) | GitHub Actions | canonical — scans diff + commit messages on every PR, action SHAs pinned |
| [gitlab-ci.yml](gitlab-ci.yml) | GitLab CI | merge-request-only job under the `lint` stage |
| [buildkite.yml](buildkite.yml) | Buildkite | single-step pipeline fragment |
| [generic.sh](generic.sh) | any | minimal bash entrypoint driven by `$BASE_REF` |

Run `kit conformance verify-no-leak --help` for the full flag surface
and exit-code contract. Notable flags:

- `--diff=<base>...HEAD` — scan files changed in the diff (CI default)
- `--commit-range=<base>..HEAD` — additionally scan commit messages
- `--pr-body=<n>` — scan PR body via `gh api` (requires `GH_TOKEN`)
- `--format=human|json` — JSON for CI artifacts, human for local
