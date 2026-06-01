# Bring an old project up to date

When new defaults land in poly-kit's tool-versions manifest or
emitters (e.g. bumped Go version, new linter, new managed
service), pull them into an existing project:

## 1. Install (or upgrade) the kit binary

    go install hop.top/kit/cmd/kit@latest

While kit is in pre-release, `@latest` may not resolve. Pin to a
published tag instead, e.g. `go install hop.top/kit/cmd/kit@v0.4.0-alpha.4`
— see <https://github.com/hop-top/poly-kit/releases> for the
current tag.

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

## Migrating `.golangci.yml` to v2

`tool-versions.toml` pins `golangci-lint = "2.12"`. v1.62.x was built
with Go 1.23 and refuses to lint Go 1.26+ targets:

    can't load config: the Go language version (go1.24) used to build
    golangci-lint is lower than the targeted Go version (1.26.1)

v2 has a different config schema (`version: "2"` required,
`linters-settings:` moves under `linters: settings:`). The Go scaffold
ships a v2-shape baseline at `.golangci.yml`. Projects with a
hand-rolled v1 config must migrate:

    # before (v1)
    linters:
      enable: [govet, errcheck]
    linters-settings:
      gosec:
        severity: medium

    # after (v2)
    version: "2"
    linters:
      enable: [govet, errcheck]
      settings:
        gosec:
          severity: medium

Full guide: <https://golangci-lint.run/docs/product/migration-guide/>.

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
