# scripts

Repo maintenance scripts invoked by the Makefile, CI and the git hooks;
none of them ship.

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`preflight.sh`](preflight.sh) | verifies the host toolchain against the repo's declared minimums | `make preflight` fails or a tool version drifts |
| [`lint-readmes`](lint-readmes) | folder README coverage, Contents links and shape caps | `make lint-readmes` fails; rules in `docs/contributors/readme-guide.md` |
| [`install-hooks.sh`](install-hooks.sh) | points `core.hooksPath` at `.githooks/` for this clone | a fresh clone is not running the pre-push gate |
| [`promote-release.sh`](promote-release.sh) | moves the release-please prerelease channel one step (alpha → beta → rc → release) | you cut the next channel; see `RELEASING.md` |
| [`rewrite-changelog.sh`](rewrite-changelog.sh) | rewrites a raw release-please changelog into the polished format, idempotently | the changelog-rewrite workflow needs a local run |
| [`test-release-e2e.sh`](test-release-e2e.sh) | end-to-end tests for the promotion and changelog-rewrite scripts | you change either script |
| [`setup-pypi-publisher.sh`](setup-pypi-publisher.sh) | creates a PyPI pending trusted publisher through a browser session | you add a Python package to the release |
| [`verify-tier1.sh`](verify-tier1.sh) | asserts the dogfood-grade workflow never requests Tier 2/3 grader output | you touch `dogfood-grade.yml` |
| [`bootstrap-scenarios-kit.sh`](bootstrap-scenarios-kit.sh) | generates the local template for the private grader repo | you bootstrap `scenarios-kit` |

## Conventions

- Shell only; Go generators live in [`internal/tools/`](../internal/tools/README.md).
- Every script is idempotent and documents itself in its header comment.
