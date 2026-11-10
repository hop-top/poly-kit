# Quality gates

> Reference for the engineering gates every scaffold ships beyond
> `make check`: what each target runs per language, where each tool is
> pinned, and the CI workflow that runs them.

## Who this is for

Template authors changing what `make quality` or `make mutation` does in
a scaffolded project. The rule behind the tool choices lives in the
playbook (`quality.md`); this page is how the templates implement it.

## Targets

Every `templates/cli-<lang>/Makefile` carries the same target names, and
a polyglot root Makefile (`compose_polyglot_makefile` in
[`templates/lib.sh`](../../templates/lib.sh)) delegates each one to every
language directory.

| Target | Checks | In `quality` |
|---|---|---|
| `audit` | known vulnerabilities in dependencies (Rust: also licenses, bans, sources) | yes |
| `unused` | unused or undeclared dependencies, dead code | yes |
| `pkgcheck` | the artifact that gets published or released is valid | yes |
| `coverage` | tests, then a line-coverage floor (`COVERAGE_MIN`) | yes |
| `dupes` | copy-paste across the tree (jscpd, `.jscpd.json`) | yes |
| `mutation` | tests catch changed behaviour, not only run the code | no: weekly |

Complexity is not a target: each language's linter enforces it, so
`make lint` fails on it.

## Tools per language

| | Go | Rust | Python | TypeScript | PHP |
|---|---|---|---|---|---|
| `audit` | govulncheck | cargo-deny (`deny.toml`) | pip-audit | `pnpm audit` (shipped deps from moderate, dev from high) | `composer audit` |
| `unused` | `go mod tidy -diff`, deadcode | cargo-machete (dead code: clippy `-D warnings`) | deptry, vulture | knip | composer-dependency-analyser (dead code: shipmonk/dead-code-detector in phpstan) |
| `pkgcheck` | `goreleaser check` | `cargo package` | `uv build` + `twine check --strict` | `publint --strict` | `composer validate --strict` |
| `coverage` | `go test -coverprofile` + `go tool cover` | cargo-llvm-cov | pytest-cov | vitest + `@vitest/coverage-v8` | phpunit + pcov |
| complexity (`lint`) | golangci-lint `gocognit`, `gocyclo`, `dupl` | clippy `cognitive_complexity` (`clippy.toml`) | ruff `C90` | eslint `complexity` | tomasvotruba/cognitive-complexity |
| `mutation` | gremlins | cargo-mutants | mutmut | Stryker | Infection |
| `dupes` | jscpd | jscpd | jscpd | jscpd | jscpd |

## Where each tool is pinned

A tool lives in the lockfile of its own ecosystem whenever it has one,
so Dependabot moves it with the other dependencies:

- **Go**: `tool` lines in `go.mod.tmpl`, run as `go tool <name>`.
- **Python**: the `dev` dependency group in `pyproject.toml.tmpl`.
- **TypeScript**: `devDependencies` in `package.json.tmpl`, run through
  package scripts so knip sees them used. `stryker.config.json` names
  its vitest runner plugin: pnpm's strict `node_modules` hides it from
  Stryker's plugin discovery.
- **PHP**: `require-dev` in `composer.json.tmpl`; phpstan extensions load
  through `phpstan/extension-installer`.
- **Everything else** (goreleaser, cargo-deny, the `cargo:` tools and
  jscpd): [`tool-versions.toml`](../../templates/shared/tool-versions.toml),
  gated per language by `_mise_lang_gate` in
  [`emit-mise.sh`](../../templates/shared/emit-mise.sh). `cargo-binstall`
  is pinned beside the `cargo:` tools so mise installs prebuilt binaries
  instead of compiling them in every CI job.

New pins follow the dependency age gate: the newest release at least 14
days old.

## Coverage floors

`COVERAGE_MIN` defaults to what the scaffold's own starter tests reach,
so a fresh project passes its own gate: 35 for Go, whose root wiring
needs a running service to exercise, and 80 elsewhere. A project raises
it as tests land and never lowers it to get a change through.

## CI

[`templates/shared/ci/quality.yml`](../../templates/shared/ci/quality.yml)
is language-agnostic and lands as `.github/workflows/quality.yml` in
single-language and polyglot scaffolds alike (`copy_quality_workflow` in
`lib.sh`, the `ci/quality.yml` case in `cmd/kit/init/shared.go`):

- `quality` runs `make setup` then `make quality` on pull requests, pushes
  to `main`, and weekly, since an advisory can land without a code change.
- `mutation` runs `make mutation` weekly and on dispatch,
  `continue-on-error`: report-only until a project sets a mutation-score
  floor.

PHP comes from `setup-php` with `coverage: pcov`, and the Rust cache step
runs only when a `Cargo.toml` exists. The file carries no `${{ }}`
expressions, because `kit init` renders shared files through
`text/template`.

## Verifying a change

After editing a template, run `make builtins-sync sync-managed-assets`,
rebuild `kit`, scaffold the language (`kit init demo --from cli-<lang>
--runtime <lang> --yes --no-github --no-push --hop=false`), then run
`make setup quality mutation` inside it.
