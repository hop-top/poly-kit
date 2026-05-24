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
