# templates

scaffolding and conformance tools for kit projects.

## Tools

- [scaffold.sh](scaffold.sh): create new project from templates.
- [conform.sh](conform.sh): bring existing repo to kit standards.

## Shared

- [lib.sh](lib.sh): shared utilities (sourced by scaffold + conform).
- [shared/](shared/README.md): common infrastructure (CI, docs, scripts).

## Blueprints

- [cli-go/](cli-go/README.md): Go CLI template.
- [cli-py/](cli-py/README.md): Python CLI template.
- [cli-ts/](cli-ts/README.md): TypeScript CLI template.

## Tests

- [test-lib.sh](test-lib.sh): unit tests for lib.sh.
- [test-conform-e2e.sh](test-conform-e2e.sh): e2e tests for conform.sh.
- [test-scaffold-e2e.sh](test-scaffold-e2e.sh): e2e tests for scaffold.sh.

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
