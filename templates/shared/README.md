# shared

common infrastructure blueprints.

## Contents

- [ci/](ci/README.md)
- [docs/](docs/README.md)
- [devcontainer/](devcontainer/README.md)
- [scripts/](scripts/README.md)

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
