# templates

scaffolding and conformance tools for kit projects.

## Tools

- [scaffold.sh](scaffold.sh): create new project from templates.
- [conform.sh](conform.sh): bring existing repo to kit standards.

## Shared

- [lib.sh](lib.sh): shared utilities (sourced by scaffold + conform).
- [shared/](shared/README.md): common infrastructure (CI, docs, scripts).

## Blueprints

Single-lang sources (one per language):

- [cli-go/](cli-go/): Go CLI template.
- [cli-py/](cli-py/): Python CLI template.
- [cli-ts/](cli-ts/): TypeScript CLI template.
- [cli-rs/](cli-rs/): Rust CLI template.
- [cli-php/](cli-php/): PHP CLI template.

Built dists (produced by [`build.sh`](build.sh)):

- `cli-template-<lang>` (one per language) — full single-lang dist
  consumed by `scaffold.sh app --lang <lang>`. Unchanged.
- `cli-template-base` — lang-agnostic skeleton: `shared/`, scripts,
  `LICENSE`, `.devcontainer/`, common-only `.gitignore` /
  `.gitattributes`, no per-lang content, no `dependabot.yml`.
  Consumed by `scaffold.sh app --langs <csv>` (any 2+ langs).

### Scaffold-time composition (polyglot)

`scaffold.sh app --langs <csv>` (2+ langs) reads `cli-template-base`
as the skeleton, then overlays per-lang dirs from the matching
single-lang sources and composes `.gitignore`, `.gitattributes`,
`dependabot.yml`, `Makefile`, and `README.md` from per-lang
fragments. Section / dir order follows the order of `--langs`
(`LANG_ARRAY` pass order), not a fixed canonical order. Polyglot
means "any 2+ langs you chose", not "all 5".

## Tests

- [test-scaffold-e2e.sh](test-scaffold-e2e.sh): e2e tests for scaffold.sh.
- [test-kit-init-idempotency.sh](test-kit-init-idempotency.sh): idempotency tests for `kit init`.
- [test-e2e.sh](test-e2e.sh): top-level e2e harness.

## What scaffold emits

Every new project (and every existing one refreshed with
`kit init --update`) gets:

- `mise.toml` — SOT for tool versions; pinned from
  [`shared/tool-versions.toml`](shared/README.md#tool-versionstoml).
  Contributor entry point is `mise run install`.
- `.devcontainer/devcontainer.json` + `docker-compose.yml` +
  `otel-config.yaml` — compose-mode, no Dockerfile. `mise` feature
  installs the toolchain. otel-collector + jaeger always on; Jaeger
  UI at <http://localhost:16686>.
- `.env.example` — five kit-managed blocks (`telemetry`, `storage`,
  `queue`, `log`, `config`). Defaults to SQLite + local XDG paths.
- `.github/workflows/*.yml` — no version literals; CI uses
  `jdx/mise-action@v2` and reads the project's own `mise.toml`.

Opt-in catalog (`scaffold.sh --services <list>` or
`kit init --add-service <name>`): `postgres`, `redis`, `minio`,
`mailpit`, `redpanda`. Service data persists in
`./.data/<service>/` bind mounts.

See [folder-structure.md §4](folder-structure.md#4-what-scaffoldsh--kit-init-emits)
for the full layout and
[shared/README.md](shared/README.md) for the emitter library API.

## Bring an old project up to date

See [RUNBOOK-UPGRADE.md](RUNBOOK-UPGRADE.md) for the
`kit init --check` / `kit init --update` procedure.
