# conform.sh

Idempotent script that brings an existing repo to kit standards.
Safe actions applied automatically; edge cases flagged in a report
with LLM-ready prompts for review.

## Usage

```
conform.sh [flags]
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--path DIR` | `.` | Project directory |
| `--dry-run` | — | Report only, no file changes |
| `--report FILE` | `conform-report.md` | Output report path |
| `--track-id ID` | `kit-conform` | tlc track ID |
| `--no-tlc` | — | Skip tlc track creation |
| `-h, --help` | — | Show help |

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
