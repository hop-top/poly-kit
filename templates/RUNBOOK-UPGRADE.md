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

## Copyright years

The scaffold writes copyright years at scaffold time and `kit init
--update` does NOT auto-bump them. This matches the legal convention
that the copyright year reflects when rights attached (creation), not
when the file was last touched — see the [SFLP guide][sflp] and the
[Copyright Office circular][circ].

  - `kit init` (first run) → writes `Copyright (c) <current-year> …`.
  - `kit init --update`    → leaves the copyright lines untouched.
  - `kit init --update --author "<years> <holder>[ <<URL>>]"` →
    overwrites the kit-managed copyright block with the supplied
    holders, e.g. extend `2026` to `2026-2027` by re-running with
    explicit `--author "2026-2027 Acme Inc <https://acme.example>"`.

Multiple holders: repeat `--author` and/or pass `;`-delimited values
within one invocation. Order is preserved.

User-added Copyright lines below the
`# <<< kit-managed: copyright <<<` close marker survive `--update`.

[sflp]: https://www.softwarefreedom.org/resources/2012/ManagingCopyrightInformation.html
[circ]: https://www.copyright.gov/circs/circ01.pdf

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
