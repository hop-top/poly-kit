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

### Toolchain versions

Two mise config files pin every tool this repo builds, lints and tests
with, and CI installs the same versions from them. With
[mise](https://mise.jdx.dev) installed, `mise trust && mise install` at
the repo root gives you what CI runs.

- `mise.toml` holds the tools kit's scaffolds share (Go, Node.js, pnpm,
  Python, uv, Rust, golangci-lint, ruff, lychee, ...). Its pins sit in a
  block emitted from `templates/shared/tool-versions.toml`, so bump a
  tool there and in `mise.toml` together, then run `make
  sync-managed-assets builtins-sync`.
- `.config/mise.toml` holds tools only this repo's CI uses (buf,
  markdownlint-cli2). Bump them there; they never go in the scaffold
  manifest, or every generated project would install them.
- CI picks up either file; no workflow edit needed.
- A few files carry their own copy and must move with it: the `go`
  directive in `go.mod`, `channel` in
  `sdk/experimental/rs/rust-toolchain.toml`, the locked `ruff` in the
  `uv.lock` files, and the devcontainer's Nix attributes.
  `make check-toolchain-parity` names each one that disagrees; it runs
  in CI (the `toolchain-parity` job) and in the pre-push hook.
- PHP is the exception: mise would build it from source, so CI installs
  the version `sdk/experimental/php/composer.json` requires, and the
  check holds each workflow's `php-version` to that.
- The check also fails when `mise.toml` and the scaffold manifest
  disagree, when the manifest lists a `.config/mise.toml`-only tool, or
  when the two mise files pin one tool differently.

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
(Go embed mirror used by `kit init` at runtime) must stay in sync: the
mirror is exactly what `make builtins-sync` produces, a verbatim copy
except that a relative Markdown link leaving a template tree (say
`../../docs/...` in `templates/shared/README.md`) is rebased so it
resolves from the mirror as well. The `mirror-sync` workflow enforces
this on every PR touching either path.

Scaffolder-only files (e.g. `build.sh`, `scaffold.sh`, `test-*.sh`,
`lib.sh`, `tests/`, `dist/`) live in `templates/` and are intentionally
NOT mirrored.

When editing `templates/cli-*/...` or `templates/shared/...`, regenerate
the mirror rather than editing `internal/template/builtins/...` by hand.
Locally:

```sh
make check-mirror-sync     # verify
make builtins-sync         # regenerate the embed mirror from templates/
```

## Protobuf stubs

Generated stubs for `contracts/proto/{cmdsurface,crud,routellm}/v1` are
committed. After editing a `.proto`, run `make proto` and commit the
result; `make proto-check` (the `proto-check` CI job) lints, regenerates
and fails on drift.

Code generation uses remote plugins on the Buf Schema Registry, pinned
by version in each `buf.gen.yaml`. The registry rate-limits anonymous
callers, so two runs in a row can fail with `resource_exhausted: too
many requests`. Authenticate to lift the limit:

- locally: `buf registry login`, or export `BUF_TOKEN` for one shell
- in CI: the `proto-check` job reads a repository secret named
  `BUF_TOKEN`. A maintainer creates a Buf API token and adds it under
  Settings → Secrets and variables → Actions. Without the secret (and on
  pull requests from forks, which never see secrets) the job runs
  anonymously and can hit the limit; re-run it.

Remote plugins stay because the TypeScript stubs need
`protoc-gen-connect-es` 1.x, whose peers (`@connectrpc/connect` 1.x,
`@bufbuild/protobuf` 1.x) conflict with the 2.x runtime `sdk/ts`
ships, so pinning it locally would mean a second, conflicting Node
toolchain just for code generation.

## Issues

- Search existing issues before opening a new one
- Use the issue forms in `.github/ISSUE_TEMPLATE/`
- Provide reproduction steps for bugs

## Code of Conduct

See [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
