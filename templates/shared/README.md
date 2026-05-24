# shared

common infrastructure blueprints.

## Contents

- [ci/](ci/README.md)
- [docs/](docs/README.md)
- [devcontainer/](devcontainer/README.md)
- [scripts/](scripts/README.md)
- [tool-versions.toml](tool-versions.toml)

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

`docker-compose.yml` (matches spec §6 of track
`scaffold-emits-mise-toml-devcontainer-compose`):

| Section | Managed? | Notes |
|---------|----------|-------|
| `services:` header + `devcontainer:` | user-extensible | not inside markers; user may edit |
| `# kit-managed: telemetry` | managed | default `otel-collector` v0.112.0 + `jaeger` 1.62 |
| `# kit-managed: opted-in services` | managed | empty by default; populated by T-0808 `--services` |

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
