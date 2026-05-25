# Bring an old project up to date

When new defaults land in poly-kit's tool-versions manifest or
emitters (e.g. bumped Go version, new linter, new managed
service), pull them into an existing project:

## 1. Install (or upgrade) the kit binary

    go install hop.top/kit/cmd/kit@latest

Or rebuild from source if you're tracking a branch.

## 2. Preview drift

    kit init --check

Exits 0 if your project is in sync. Exits non-zero with a diff
if anything has changed.

## 3. Apply the refresh

    kit init --update

Refreshes `mise.toml`, `.devcontainer/devcontainer.json`,
`.devcontainer/docker-compose.yml`, `.env.example`. Only
content INSIDE `# >>> kit-managed >>> … # <<< kit-managed <<<`
markers is touched; user-owned content above markers is
preserved verbatim.

## 4. Sanity-check

    mise install
    mise run install
    docker compose -f .devcontainer/docker-compose.yml config -q

## Adding or removing services

    kit init --add-service redis
    # … or in compose YAML directly for full control

Catalog: `postgres`, `redis`, `minio`, `mailpit`, `redpanda`.
Each service bind-mounts its data into `.data/<service>/` at the
project root (gitignored).

## See also

- [`folder-structure.md` §4](folder-structure.md#4-what-scaffoldsh--kit-init-emits)
  — full layout of emitted files.
- [`shared/README.md`](shared/README.md) — emitter library API
  reference (`managed-block.sh`, `emit-*.sh`, `apply-services.sh`).
- [`CONFORM.md`](CONFORM.md) — `conform.sh` (thin wrapper around
  `kit init --update`) for the additive-merge checks outside the
  managed scope.
