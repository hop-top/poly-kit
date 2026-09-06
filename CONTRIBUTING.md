# Contributing to kit

Thanks for your interest in contributing!

Org-wide policy — Conventional Commit types, the release model, and
sign-off expectations — lives in
[hop-top/.github `CONTRIBUTING.md`](https://github.com/hop-top/.github/blob/main/CONTRIBUTING.md).
This file covers only what is specific to this repository.

## Getting Started

1. Fork the repository
2. Clone your fork locally
3. Create a feature branch: `git checkout -b feat/my-change`
4. Make your changes
5. Run tests: `make test`
6. Push and open a Pull Request

Commit and PR titles follow the org convention; see
[Commit messages](#commit-messages) below.

## Development Setup

See [.devcontainer/README.md](.devcontainer/README.md) for detailed
instructions on setting up your environment — devcontainer, manual
toolchain versions, and per-language dependency bootstrap.

Quick start:

```sh
make setup
```

## Git Hooks

This repo ships Git hooks in `.githooks/` to catch common mistakes locally before they hit CI:

- `pre-push` — refuses direct pushes to `main`/`master` (open a PR instead) and runs affected linters/tests against the changes being pushed. Bypass with `git push --no-verify` for emergencies.

To install (per-clone, idempotent):

```sh
bash scripts/install-hooks.sh
```

This sets `core.hooksPath=.githooks` for the current clone. CI does not require it.

## Code Style

- Follow existing conventions in the codebase
- Run linters before submitting: `make lint`
- Keep changes focused; one concern per PR

## Tests

`make test` is the everyday gate. Two Go test targets run in CI as
separate jobs, and you can run either locally:

| Target | Covers |
| --- | --- |
| `make test-go-integration` | Every Go module, testcontainer suites included. The `go-test` job. |
| `make test-go-integration PROPERTY_ITERATIONS=100` | Same, with the `engine/store` property tests at 100 iterations instead of 1000: what the `go-test` job runs on pull requests. Pushes and the nightly schedule keep the full count. |
| `make test-go-race` | The `go/` tree under `-race`. The `go-race` job. |

`test-go-race` exists because a concurrency guard is invisible to a
plain `go test`: the parallel-invocation and flag-isolation tests behind
`serve` pass whether or not the code they guard is still correct, and
only the race detector tells them apart. Add a test that spawns a
goroutine and it is covered the moment it lands — the target takes the
whole `go/` tree, so there is no list to add yourself to.

It runs alongside `test-go-integration` rather than after it, and takes
about three minutes from a cold cache. One package, `go/core/projects`,
is excluded because it races today for a reason of its own; the Makefile
target carries the detail and the condition for dropping the exclusion.

## Commit messages

Conventional Commits, per the
[org-wide policy](https://github.com/hop-top/.github/blob/main/CONTRIBUTING.md#conventional-commits) —
including which types are user-facing and the `ci:` rule. Nothing in
this repo overrides it.

## Releases

Branch model, prerelease channels, and how a stable version is cut are
documented in [RELEASING.md](RELEASING.md). The org-wide shape of the
release process is in the
[org-wide policy](https://github.com/hop-top/.github/blob/main/CONTRIBUTING.md#release-model).

## Pull Requests

- Fill in the PR template (`.github/PULL_REQUEST_TEMPLATE.md`)
- Reference related issues in the PR description
- Keep PRs small and reviewable
- Ensure CI passes before requesting review
- Update documentation if behavior changes

## Templates Mirror Sync

The `templates/` tree (canonical source) and `internal/template/builtins/`
(Go embed mirror used by `kit init` at runtime) must stay byte-identical
for every file that exists in both. The `mirror-sync` workflow enforces
this on every PR touching either path.

Scaffolder-only files (e.g. `build.sh`, `scaffold.sh`, `test-*.sh`,
`lib.sh`, `tests/`, `dist/`) live in `templates/` and are intentionally
NOT mirrored.

When editing `templates/cli-*/...` or `templates/shared/...`, mirror your
change to `internal/template/builtins/...`. Locally:

```sh
make check-mirror-sync     # verify
make builtins-sync         # regenerate the embed mirror from templates/
```

## Issues

- Search existing issues before opening a new one
- Use the issue forms in `.github/ISSUE_TEMPLATE/`
- Provide reproduction steps for bugs

## Code of Conduct

See [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
