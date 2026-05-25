# conform.sh

Thin wrapper over `kit init --update`. `kit init` refreshes the
kit-managed blocks (mise.toml, `.devcontainer/*`, `.env.example`
adapter blocks); `conform.sh` adds the additive-merge checks
outside that scope (license headers, missing files, Makefile /
`.gitignore` merges). New adopters: reach for `kit init` first —
see [RUNBOOK-UPGRADE.md](RUNBOOK-UPGRADE.md).

`conform.sh` is a thin wrapper over `kit init --update`: the
kit-managed blocks (mise.toml, `.devcontainer/*`, `.env.example`
kit-adapter blocks) are refreshed by `kit init`; the additive-merge
checks below cover everything *outside* the managed scope.

## Usage

```
conform.sh [flags]
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--path DIR` | `.` | Project directory |
| `--dry-run` | — | Report only, no file changes; runs `kit init --check` to preview managed-block drift |
| `--report FILE` | `conform-report.md` | Output report path |
| `--track-id ID` | `kit-conform` | tlc track ID |
| `--no-tlc` | — | Skip tlc track creation |
| `--no-managed-refresh` | — | Skip the `kit init --update` step; run only the additive-merge checks below |
| `-h, --help` | — | Show help |

## Managed blocks

The managed-block refresh is delegated to `kit init --update`
(track: `scaffold-emits-mise-toml-devcontainer-compose`, §4).
These files contain `kit-managed:` marker pairs; only the content
between markers is rewritten — user-owned content above/below is
preserved verbatim.

Files in the **managed scope** (owned by `kit init`):

- `mise.toml` — managed `[tools]` block
- `.devcontainer/devcontainer.json` — managed
  `postCreateCommand`, `postStartCommand`, `extensions`,
  `features` keys
- `.devcontainer/docker-compose.yml` — `kit-managed: telemetry`
  and `kit-managed: opted-in services` blocks
- `.devcontainer/otel-config.yaml` — entire file managed
- `.env.example` — one managed block per adapter domain

Files in the **additive-merge scope** (owned by `conform.sh`):

- License headers, missing-file copies (see "Safe: Copy from
  Template" below)
- `Makefile` — additive merge of kit targets
- `.gitignore` — additive merge of kit entries
- Review items (AGENTS.md, go.mod replaces, etc.)

### kit binary detection

`conform.sh` resolves the kit binary in this order:

1. `command -v kit` — installed kit on `$PATH`
2. `$KIT_BIN` — explicit override (must be executable)
3. In-repo `go build ./cmd/kit` — only when running against
   poly-kit itself (detected by `templates/scaffold.sh`)
4. Skip with a warning — additive-merge checks still run

Pass `--no-managed-refresh` to skip step 1-3 entirely (useful
for dry-run / CI checks that only want the additive-merge
checks).

## What It Checks

### Safe: Copy from Template

Missing files copied verbatim from blueprint/shared templates.
Skipped if target already exists.

- `.golangci.yml` (Go)
- `.trivyignore` (Go)
- `.goreleaser.yaml` (Go)
- `.github/workflows/ci.yml` (Go)
- `.github/workflows/ci-py.yml` (Python)
- `CONTRIBUTING.md`
- `SECURITY.md`
- `docs/RELEASING.md`
- `CHANGELOG.md` (Go)
- `scripts/promote-release.sh`
- `internal/version/version.go` (Go)

### Safe: Generated

Config files generated from detected languages. Skipped if
already present; missing ecosystems appended to existing files.

- `.github/dependabot.yml` — ecosystems from detected languages
- `.release-please-manifest.json` + `release-please-config.json`

### Safe: Additive Merge

Existing files extended with missing entries. Marker comments
prevent duplicate additions on re-runs.

- `Makefile` — kit targets: `build`, `clean`, `release`,
  `promote`, `promote-*`, `links`
- `.gitignore` — kit entries: `bin/`, `dist/`, `*.db`,
  `coverage.out`, `coverage_e2e.out`

### Review: LLM-Prompted

Items requiring human judgment. Flagged in report with context
and suggested action for LLM-assisted resolution.

- **AGENTS.md** — missing agent instructions; prompt includes
  detected entry points, packages, test dirs
- **go.mod replace directives** — local path replaces flagged
  for removal; remote replaces flagged for confirmation
- **Makefile target conflicts** — pre-existing targets that
  overlap kit standards; compare and decide
- **requirements.txt migration** — Python project without
  `pyproject.toml`; prompt includes first 30 lines of deps

## Report Format

Output is a markdown file with YAML frontmatter compatible
with `tlc track update --add-plan`:

```yaml
---
title: "kit conformance: <app>"
tracks:
  - <track-id>
tasks:
  - title: "<file or check>"
    description: |
      <detail>
    effort: XS|S|M
    priority: P1|P2|P3
    tags: [status:applied|skipped|review]
---
```

### tlc Integration

When `tlc` is available and `--no-tlc` not set:

1. Creates track with `--type refactor`
2. Copies report to `.tlc/tracks/<id>/plan.md`
3. Ingests plan via `tlc track update --add-plan`

Disabled automatically in `--dry-run` mode.

## Exit Codes

| Code | Meaning |
|------|---------|
| `0` | All checks passed or applied; no review items |
| `1` | Runtime error (bad path, missing deps) |
| `2` | Review items remain; see report |
| `3` | `--dry-run` only — managed-block drift detected by `kit init --check` |

## Examples

```bash
# dry-run against current dir
conform.sh --dry-run

# conform a specific project
conform.sh --path ~/src/myapp

# custom report path, no tlc
conform.sh --report /tmp/audit.md --no-tlc

# custom track ID
conform.sh --track-id myapp-conform
```

## Idempotency

Safe to run multiple times. Each action checks current state:

- copy actions skip if target file exists
- generated configs skip if manifest present; append only
  missing ecosystems
- merge actions use marker comments to detect prior runs
- review checks re-evaluate each run (findings may change
  as issues are resolved)
