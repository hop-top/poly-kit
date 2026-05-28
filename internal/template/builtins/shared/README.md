# shared

common infrastructure blueprints.

This directory holds the SOT (`tool-versions.toml`), the idempotent
managed-block writer (`managed-block.sh`), the per-artifact emitters
(`emit-*.sh`), the opt-in services applier (`apply-services.sh`), and
the curated `--services` catalog under `services/`. `scaffold.sh`
sources these directly; `kit init` embeds a byte-identical mirror
under `cmd/kit/init/managed_assets/` (kept in sync by pre-commit
hook) so refreshes work with just the `kit` binary on `$PATH`.

## Contents

- [ci/](ci/README.md)
- [docs/](docs/README.md)
- [devcontainer/](devcontainer/README.md)
- [scripts/](scripts/README.md)
- [tool-versions.toml](tool-versions.toml)
- [services/](#services-catalog) — opt-in `--services` catalog
  (postgres, redis, minio, mailpit, redpanda)

## tool-versions.toml

Single source of truth (SOT) for tool versions across emitted
`mise.toml`, CI workflows, and devcontainer. Consumed by:

- `templates/scaffold.sh` — when emitting a new project.
- `kit init` — when creating or refreshing managed blocks in an
  existing project (see track
  `scaffold-emits-mise-toml-devcontainer-compose`, §3).

### Format

TOML, two tables:

| Table | Purpose | Members |
|-------|---------|---------|
| `[runtimes]` | Language toolchains, gated by project `--lang` | `go`, `node`, `pnpm`, `python`, `uv`, `rust` |
| `[workflow]` | Cross-cutting linters, formatters, release tooling | `golangci-lint`, `ruff`, `lychee`, `hadolint`, `actionlint`, `shellcheck`, `shfmt`, `npm:release-please` |

Keys match mise tool names. `npm:`-prefixed keys reference the
mise npm backend. Values are mise-resolvable version strings
(semver-ish, partial accepted).

### Update policy

- **Runtimes** — track upstream major-stable cadence; bump
  conservatively, avoid unreleased majors.
- **Workflow tools** — track upstream releases; bump freely when
  CI is green.
- Bumping any value here is a SOT change. Downstream projects
  pick it up via `kit init --update` (manual) or the next
  conform pass.
- Keep comments terse: one-line "what this tool is for".

No flag gates emission of this manifest itself; it is always
read.

## gitignore composition

The final project `.gitignore` is composed at scaffold time from
`shared/gitignore/common.gitignore` plus per-language snippets
under `shared/gitignore/<lang>.gitignore`. `templates/build.sh`'s
`compose_gitignore` concatenates these in order into
`<dest>/.gitignore`; tier filtering for the composed file is
governed by `shared/tiers.yaml`, not by per-language
`cli-<lang>/tiers.yaml`. As a convention, per-language
`tiers.yaml` files MUST NOT list `.gitignore` — there is no
`.gitignore` source file at `templates/cli-<lang>/` for the
engine to match, so any such entry is vestigial and will be
removed during audit.

The composed content is wrapped in a labeled `kit-managed`
block:

```
# >>> kit-managed: gitignore >>>
<common.gitignore content>
<per-lang snippet contents>
# <<< kit-managed: gitignore <<<
```

This lets users add custom entries (e.g. `.idea-local/`) above
or below the markers without losing them on a re-scaffold or
`kit init --update`. `build.sh` runs at distributable-build
time, so the markers are emitted as static shell text — the
managed-block library is not sourced here. There is no runtime
`emit-gitignore.sh`: `kit init` treats the composed file as a
tier-1 copy, and the marker semantics are honored by the same
`mb_*` helpers that govern the other emitted artifacts when an
existing project is refreshed.

## gitattributes composition

The final project `.gitattributes` is composed at scaffold time
from `shared/gitattributes/common.gitattributes` plus per-language
snippets under `shared/gitattributes/<lang>.gitattributes`.
`templates/build.sh`'s `compose_gitattributes` concatenates these
in order into `<dest>/.gitattributes`; tier filtering for the
composed file is governed by `shared/tiers.yaml`, not by
per-language `cli-<lang>/tiers.yaml`. As a convention,
per-language `tiers.yaml` files MUST NOT list `.gitattributes` —
there is no `.gitattributes` source file at `templates/cli-<lang>/`
for the engine to match, so any such entry is vestigial and will
be removed during audit.

Section order is deterministic: `common.gitattributes` first,
then per-lang snippets in the order passed to the composer.
Single-lang templates emit `common` + `<lang>`; the polyglot
template emits `common` + `go` + `ts` + `py` + `rs` + `php`.

The composed content is wrapped in a labeled `kit-managed`
block:

```
# >>> kit-managed: gitattributes >>>
<common.gitattributes content>
<per-lang snippet contents>
# <<< kit-managed: gitattributes <<<
```

This lets users add custom rules (e.g. `*.psd binary`) above or
below the markers without losing them on a re-scaffold or
`kit init --update`. `build.sh` runs at distributable-build time,
so the markers are emitted as static shell text — the
managed-block library is not sourced here. There is no runtime
`emit-gitattributes.sh`: `kit init` treats the composed file as a
tier-1 copy, and the marker semantics are honored by the same
`mb_*` helpers that govern the other emitted artifacts when an
existing project is refreshed.

## Managed-block library

`managed-block.sh` is a small bash library for **idempotent
edits to marker-delimited blocks** inside config files (TOML,
YAML, `.env`, shell, JSON-C). Scaffold emitters (`scaffold.sh`)
and the future `kit init` updater use it to create or refresh
specific sections of a project file without clobbering
user-owned content sitting outside the markers.

### Marker syntax

The comment character is chosen from the file path:

| Format                                  | Open marker                       | Close marker                      |
|-----------------------------------------|-----------------------------------|-----------------------------------|
| TOML, YAML, `.env`, shell, Dockerfile   | `# >>> kit-managed >>>`           | `# <<< kit-managed <<<`           |
| JSON-C (`devcontainer.json`, `*.jsonc`) | `// >>> kit-managed >>>`          | `// <<< kit-managed <<<`          |

Blocks may carry an optional **label** (per the
scaffold-emits-mise-toml-devcontainer-compose spec). Labeled
markers look like:

```
# >>> kit-managed: telemetry >>>
…
# <<< kit-managed: telemetry <<<
```

Multiple labeled blocks (plus one unlabeled) can coexist in
the same file.

### API

Source the library and call the helpers:

```bash
source "$SCRIPT_DIR/shared/managed-block.sh"

# read current block content
mb_read mise.toml                  # unlabeled
mb_read .env.example telemetry     # labeled

# write/replace (reads stdin); creates the file if absent;
# appends a block at EOF if no markers exist yet
printf 'go = "1.26"\n' | mb_write mise.toml
printf 'OTEL=...\n'    | mb_write .env.example telemetry

# delete an entire block (markers + content); no-op if absent
mb_remove docker-compose.yml "opted-in services"

# detect a block (exit 0 / 1)
mb_has mise.toml && echo "managed block present"

# inspect comment-char mapping for a file
mb_comment_char devcontainer.json   # -> "//"
```

### Idempotency guarantees

- Writing the **same** content twice produces a
  byte-identical file (verified by `cmp -s` before atomic
  `mv`).
- Writing **different** content updates only bytes inside the
  markers — every line above the open marker and below the
  close marker is preserved verbatim.
- `mb_remove` trims at most one blank-line separator
  immediately above the block to avoid orphaning a separator.

### Portability

Pure bash + POSIX `awk`, `grep`, `cmp`, `mktemp`, `mv`. Avoids
`sed -i` (BSD vs GNU incompatibility) by writing to a temp
file and atomically renaming. Verified on macOS BSD awk and
GNU awk. Tests live in `managed-block.bats` and run with
[bats-core](https://github.com/bats-core/bats-core).

## emit-mise.sh

`emit-mise.sh` writes a kit-managed `mise.toml` into a project
directory, populated from `tool-versions.toml` and scoped to
the project's selected languages. Built on top of
`managed-block.sh`, so the emitted file is idempotent and
preserves any user-owned content above the markers.

### API

```bash
source "$SCRIPT_DIR/shared/emit-mise.sh"

emit_mise <project-dir> <lang-csv>
```

- `<project-dir>` — path to the project root. Receives (or
  refreshes) `<project-dir>/mise.toml`.
- `<lang-csv>` — comma-separated subset of `go,ts,py,rs`.
  Order is ignored.

### What gets emitted

Always inside `# >>> kit-managed >>> … # <<< kit-managed <<<`:

- `[tools]` — runtimes from `[runtimes]` (gated by lang) +
  workflow tools from `[workflow]`. `golangci-lint` and
  `ruff` are lang-gated (go and py respectively); every
  other workflow tool is cross-cutting and always emitted.
- `[env]` — `_.file = ".env"`.
- `[tasks.install]` — `run = [...]` array with the ecosystem
  install command for each selected lang:
  `go mod download`, `pnpm install`, `uv sync`, `cargo fetch`.

### How scaffold.sh calls it

`templates/scaffold.sh` sources both `managed-block.sh` and
`emit-mise.sh` near the top, then in the post-clone setup
phase (after `init.sh` runs) calls:

```bash
emit_mise "$OUTPUT" "$LANG"
```

The same function is the contract used by the future
`kit init` updater so existing projects can refresh their
managed block without clobbering user-owned tools added
above the markers.

### Parsing

`tool-versions.toml` has a constrained shape (two flat
tables of `key = "value"` scalars), so the parser is a
small awk state machine — no TOML library dependency.
Portable on BSD + GNU awk. Tests live in `emit-mise.bats`.

## emit-devcontainer-json.sh

Emits `<project>/.devcontainer/devcontainer.json` in compose
mode — no custom Dockerfile, toolchain provided by the
`ghcr.io/jdx/mise/features/mise:1` devcontainer feature.

### API

```bash
source "$SCRIPT_DIR/shared/managed-block.sh"
source "$SCRIPT_DIR/shared/emit-devcontainer-json.sh"

emit_devcontainer_json <project-dir> <project-name> <lang-csv>
```

| Argument        | Notes                                              |
|-----------------|----------------------------------------------------|
| `<project-dir>` | Project root. The file is written under `.devcontainer/`. |
| `<project-name>`| Interpolated into the JSON `"name"` field.         |
| `<lang-csv>`    | Comma-separated subset of `go`, `ts`, `py`, `rs`.  |

### What gets emitted

- `dockerComposeFile: "docker-compose.yml"` (sibling file
  emitted by `emit-docker-compose.sh`).
- `service: "devcontainer"`, `workspaceFolder: "/workspace"`,
  `remoteUser: "dev"`.
- `features` — `common-utils:2` + `jdx/mise:1`.
- Managed `lifecycle` block — `postCreateCommand` runs
  `mise trust && mise install && mise run install`;
  `postStartCommand` runs `mise install --quiet`.
- Managed `extensions` block inside
  `customizations.vscode.extensions[]` — per-language VS Code
  extensions (see mapping below), plus always-included
  `jdx.mise-vscode` and `EditorConfig.EditorConfig`.
- `forwardPorts: [16686, 4318]` — Jaeger UI and OTLP HTTP.

### Per-language extension mapping

| Lang | Extensions                                                          |
|------|---------------------------------------------------------------------|
| `go` | `golang.go`                                                         |
| `ts` | `dbaeumer.vscode-eslint`, `esbenp.prettier-vscode`                  |
| `py` | `ms-python.python`, `ms-python.vscode-pylance`, `charliermarsh.ruff`|
| `rs` | `rust-lang.rust-analyzer`, `tamasfe.even-better-toml`               |

Per-language extensions are appended in the canonical order
`go → ts → py → rs` so two scaffolds with the same lang set
produce a byte-identical file (idempotency).

### JSON-C handling

`devcontainer.json` is JSON-C — JSON with `//` line comments
and trailing commas — both accepted by the devcontainer spec
and by VS Code's loader. The emitter uses `mb_write` with the
JSON-C comment marker (`//`) for the managed blocks. To parse
the file with stricter loaders (e.g. `jq`), strip `//` line
comments and trailing commas first; the bats tests demonstrate
this.

### Skipping emission

`templates/scaffold.sh --no-devcontainer` skips this emitter
(and the sibling `docker-compose.yml` emitter).
Tests live in `emit-devcontainer-json.bats`.

## emit-docker-compose.sh

Scaffold emitter that writes `.devcontainer/docker-compose.yml`
and the sibling `.devcontainer/otel-config.yaml` into a
freshly-scaffolded project. Sourced and invoked by
`templates/scaffold.sh` after `init.sh` finishes, unless
`--no-devcontainer` is passed.

### API

```bash
source "$SCRIPT_DIR/shared/managed-block.sh"
source "$SCRIPT_DIR/shared/emit-docker-compose.sh"

emit_docker_compose <project-dir> <project-name>
```

`<project-name>` is interpolated into the `OTEL_SERVICE_NAME`
environment variable inside the `devcontainer` service.

### File layout

`docker-compose.yml`:

| Section | Managed? | Notes |
|---------|----------|-------|
| `services:` header + `devcontainer:` | user-extensible | not inside markers; user may edit |
| `# kit-managed: telemetry` | managed | default `otel-collector` v0.112.0 + `jaeger` 1.62 |
| `# kit-managed: opted-in services` | managed | empty by default; populated via `--services` |

`otel-config.yaml` — entire file is one unlabeled
kit-managed block (no user-editable region).

### Idempotency

Re-emitting against an existing project is byte-identical when
the inputs match. Powered by `managed-block.sh`'s `cmp -s`
short-circuit. If the user hand-edits the `devcontainer:`
service block above the markers, the emitter leaves it alone
and only refreshes the managed blocks below.

### Tests

`emit-docker-compose.bats` covers content, indentation,
idempotency, and (if `docker compose` is available)
`docker compose -f … config` validation.

## emit-env-example.sh

`emit-env-example.sh` writes a project's `.env.example`
containing the five labeled kit-managed blocks described in the
scaffold-emits-mise-toml-devcontainer-compose spec §7:
`telemetry`, `storage`, `queue`, `log`, `config`. Defaults match
SQLite + local-XDG paths; redis and postgres URLs are commented
out. `OTEL_SERVICE_NAME` is interpolated from the project name.

### API

```bash
source "$SCRIPT_DIR/shared/managed-block.sh"
source "$SCRIPT_DIR/shared/emit-env-example.sh"

emit_env_example "$OUTPUT" "$NAME"   # writes <OUTPUT>/.env.example
```

`emit_env_example` is layered on top of `managed-block.sh`:
re-running it is idempotent (byte-identical output), and any
user-added env vars sitting **above** the managed markers
survive subsequent re-emits. `kit init --add-service redis`
(future) will flip `KIT_QUEUE_DRIVER` to `redis` and uncomment
`KIT_QUEUE_REDIS_URL` within the `queue` block.

Tests live in `emit-env-example.bats`.

## Services catalog

`templates/shared/services/` ships compose snippets + matching
`.env` snippets for the five `--services` catalog members. The
catalog is the opt-in counterpart to the always-on `telemetry`
block emitted by `emit-docker-compose.sh`.

### Layout

```
services/
├── postgres.yml      # compose stanza
├── redis.yml
├── minio.yml
├── mailpit.yml
├── redpanda.yml
└── env/
    ├── postgres.env  # env vars to merge into .env.example
    ├── redis.env
    ├── minio.env
    ├── mailpit.env
    └── redpanda.env
```

| Service    | Image (pinned)                                            | Purpose                                |
|------------|-----------------------------------------------------------|----------------------------------------|
| `postgres` | `postgres:17-alpine`                                      | Relational DB; queue backend candidate |
| `redis`    | `redis:8-alpine` (AOF on)                                 | Cache; queue backend candidate         |
| `minio`    | `quay.io/minio/minio:RELEASE.2025-04-22T22-12-26Z`        | S3-compatible object store             |
| `mailpit`  | `axllent/mailpit:v1.21`                                   | SMTP capture + web UI for dev          |
| `redpanda` | `docker.redpanda.com/redpandadata/redpanda:v24.2.7`       | Kafka API + schema registry, single node |

`{{PROJECT_NAME}}` inside each snippet is substituted with the
scaffolded project name (e.g. `POSTGRES_DB`, `S3_BUCKET`,
`SMTP_FROM`).

### API

```bash
source "$SCRIPT_DIR/shared/managed-block.sh"
source "$SCRIPT_DIR/shared/apply-services.sh"

apply_services    <project-dir> <project-name> <services-csv>
apply_no_services <project-dir>
```

`apply_services` performs three writes, all gated by
`managed-block.sh` for byte-level idempotency:

1. **Compose** — concatenates each selected snippet (in
   canonical catalog order `postgres → redis → minio →
   mailpit → redpanda`, regardless of input order) into the
   `# kit-managed: opted-in services` block of
   `<project-dir>/.devcontainer/docker-compose.yml`.
2. **Queue block** — rewrites the `# kit-managed: queue`
   block of `<project-dir>/.env.example` so `KIT_QUEUE_DRIVER`
   and the corresponding URL line reflect the selection.
3. **Services block** — creates (or refreshes) a single
   `# kit-managed: services` block at the bottom of
   `.env.example` containing the non-queue env vars
   (`S3_*`, `SMTP_*`, `KAFKA_*`). Per-adapter blocks
   (`queue`, `storage`, `log`, `config`, `telemetry`) stay
   focused on adapter-level config so catalog vars don't
   spread across them.

`apply_no_services` strips the `# kit-managed: telemetry`
block from the compose file (used by scaffold.sh's
`--no-services` flag). The user-extensible `devcontainer:`
service and the `# kit-managed: opted-in services` block are
left intact.

### `KIT_QUEUE_DRIVER` precedence

Highest wins:

| Selection contains | `KIT_QUEUE_DRIVER` |
|--------------------|--------------------|
| `redis`            | `redis`            |
| `postgres` (no redis) | `postgres`      |
| neither            | `sqlite` (default) |

Selecting both `postgres` and `redis` uncomments **both**
`KIT_QUEUE_REDIS_URL` and `KIT_QUEUE_POSTGRES_URL` but sets
`KIT_QUEUE_DRIVER=redis`. The user can flip the driver value
by hand inside the managed block; `kit init --update` will
preserve manual driver flips only if the surrounding shape
still matches.

### Idempotency + ordering

- Input order is normalized to catalog order, so
  `--services redpanda,postgres` and `--services postgres,redpanda`
  produce byte-identical compose + env files.
- Running `apply_services` twice with the same arguments is a
  no-op (verified by `cmp -s` in `mb_write`).

Tests live in `apply-services.bats`.
