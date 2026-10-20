# scripts

Repo maintenance scripts invoked by the Makefile, CI and the git hooks;
none of them ship.

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`preflight.sh`](preflight.sh) | verifies the host toolchain against the repo's declared minimums | `make preflight` fails or a tool version drifts |
| [`check-toolchain-parity.sh`](check-toolchain-parity.sh) | fails when a version declared outside the mise config (`go.mod`, `rust-toolchain.toml`, `uv.lock`, workflows) disagrees with its pin | `make check-toolchain-parity` fails or you bump a tool |
| [`toolchain-pins.sh`](toolchain-pins.sh) | prints the tool versions pinned in `mise.toml` and `.config/mise.toml`, one `name=version` per line | CI, the Makefile or a script needs a pinned version |
| [`test-affected.sh`](test-affected.sh) | runs the Go, TypeScript, Python and parity suites a range of changes implicates | you test your branch before push; `make test-affected` |
| [`lint-readmes`](lint-readmes) | folder README coverage, Contents links and shape caps | `make lint-readmes` fails; rules in `docs/contributors/readme-guide.md` |
| [`sync-builtins.sh`](sync-builtins.sh) | copies `templates/` into the `internal/template/builtins/` embed mirror, rebasing Markdown links that leave a template tree | `make builtins-sync` or `make check-mirror-sync` reports drift |
| [`lint-internal-refs.sh`](lint-internal-refs.sh) | fails on real home-directory paths and names of documents that live outside the repo | `make lint-internal-refs` fails |
| [`install-hooks.sh`](install-hooks.sh) | points `core.hooksPath` at `.githooks/` for this clone | a fresh clone is not running the pre-push gate |
| [`promote-release.sh`](promote-release.sh) | records and commits a target-versioning step (channel promotion, opened line) per package or group through hop-top/.github's `promote.js` | you promote a line or open one; see `RELEASING.md` |
| [`rewrite-changelog.sh`](rewrite-changelog.sh) | rewrites a raw release-please changelog into the polished format, idempotently | the changelog-rewrite workflow needs a local run |
| [`test-release-e2e.sh`](test-release-e2e.sh) | end-to-end tests for the promotion wrapper and changelog-rewrite scripts | you change either script |
| [`setup-pypi-publisher.sh`](setup-pypi-publisher.sh) | creates a PyPI pending trusted publisher through a browser session | you add a Python package to the release |
| [`verify-tier1.sh`](verify-tier1.sh) | asserts the dogfood-grade workflow never requests Tier 2/3 grader output | you touch `dogfood-grade.yml` |
| [`bootstrap-scenarios-kit.sh`](bootstrap-scenarios-kit.sh) | generates the local template for the private grader repo | you bootstrap `scenarios-kit` |

## Conventions

- Shell only; Go generators live in [`internal/tools/`](../internal/tools/README.md).
- Every script is idempotent and documents itself in its header comment.
