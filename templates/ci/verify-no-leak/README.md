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
- `--paths=<p>[,<p>...]` — scan an explicit list; directories recurse
- `--allow-missing-paths` — downgrade an unresolvable `--paths` entry
  from an error to a skip (off by default; see below)
- `--commit-range=<base>..HEAD` — additionally scan commit messages
- `--pr-body=<n>` — scan PR body via `gh api` (requires `GH_TOKEN`)
- `--format=human|json` — JSON for CI artifacts, human for local

## `--paths` resolution and why it can fail the build

A gate that scans nothing passes. `--paths` therefore separates three
outcomes rather than collapsing them:

| Situation | Exit | Behavior |
|---|---|---|
| Entry does not resolve (typo, moved dir) | 67 (config error) | fails the job |
| Directory resolves, holds nothing scannable | 0 | warns `0 files scanned` on stderr |
| Directory resolves with scannable files | 0 / findings | recursive walk |

The first row matters in CI: `io_error` is excluded from the
conformance action's fail-on set, so a silently-skipped path would let
a job go green having scanned nothing. Pinning `--paths` at a directory
that later moves is exactly how that happens.

`--allow-missing-paths` opts into the older pass-through behavior,
turning row 1 into row 2. It trades a loud failure for a silent one —
only pass it when scanning nothing is an acceptable outcome.
