# Author a Template

Use `examples/spaced/` as the canonical reference when bootstrapping
a new kit-based CLI. This guide covers structure, CI, and conventions.

> **Tip**: `kit init <name>` generates output equivalent to this guide
> automatically (CI workflows, Makefile, lint configs, README skeleton,
> toolspec). Use `kit init` to create new projects and `kit init
> --tier <N>` to augment existing repos. See
> [kit-init.md](kit-init.md).

---

## README Structure

Every kit CLI README should include, in order:

1. **Header** -- project name, one-line description
2. **Badges** -- CI status, version, license
3. **Quick Start** -- per-language install + first command
4. **Commands table** -- all commands with descriptions
5. **Shell Completion** -- bash/zsh/fish setup snippets
6. **Aliases** -- create, list, remove; storage location
7. **Contributing** -- how to add commands across languages
8. **Disclaimer** -- if applicable

See `examples/spaced/README.md` for a working example.

---

## Doc Structure

A kit CLI should document:

| Doc | Purpose |
| --- | ------- |
| `README.md` | Entry point; quick start + command table |
| `docs/cli-api-reference.md` | Flag/arg spec per command |
| `docs/completion-user-guide.md` | Shell completion setup |
| `*.toolspec.yaml` | Machine-readable command contract |
| `CHANGELOG.md` | Release history |

Optional but recommended:

- `docs/getting-started-cli.md` -- extended walkthrough
- `docs/help-rendering.md` -- help output conventions
- `docs/alias-api.md` -- alias system reference

---

## CI Setup

Three lint jobs run in parallel via GitHub Actions.

### Markdownlint

Config: `.markdownlint.yaml`

```yaml
default: true
MD013: false          # line length handled by AGENTS.md rule
MD033: false          # allow inline HTML (badges, details)
MD041: false          # first line heading not required
```

Action: `DavidAnson/markdownlint-cli2-action@v19`

### Lychee (link checking)

Config: `lychee.toml` + `.lycheeignore`

`lychee.toml` sets timeouts, retries, accepted status codes, and
excluded paths. `.lycheeignore` lists domains to skip (localhost,
rate-limited endpoints).

Action: `lycheeverse/lychee-action@v2`

### ShellCheck

Scans `tapes/` for shell script issues.

Action: `ludeeus/action-shellcheck@2.0.0`

### Workflow file

Copy `.github/workflows/lint.yml` to any kit CLI repo. Adjust
`scandir` and lychee `args` paths as needed.

---

## Makefile Structure

Targets grouped by section:

| Section | Targets |
| ------- | ------- |
| Build | `build`, `build-go`, `build-ts`, `build-py`, `build-web` |
| Run | `run-go`, `run-ts`, `run-py`, `serve-web` |
| Test | `test`, `test-web` |
| Lint | `lint`, `lint-md`, `lint-links`, `lint-shell` |
| Media | `screenshots`, `record`, `media` |
| Clean | `clean` |

Every target has a `## comment` for `make help` autodiscovery.

---

## Config Files

| File | Purpose |
| ---- | ------- |
| `.markdownlint.yaml` | Markdownlint rules |
| `lychee.toml` | Link checker config |
| `.lycheeignore` | Domains/patterns to skip |
| `.github/workflows/lint.yml` | CI workflow |
| `*.toolspec.yaml` | Command contract spec |

---

## New Kit CLI Checklist

- [ ] Copy `examples/spaced/` as starting point
- [ ] Update `README.md` header, badges, command table
- [ ] Update `Makefile` paths (`REPO_ROOT`, entry points)
- [ ] Copy `.markdownlint.yaml`, `lychee.toml`, `.lycheeignore`
- [ ] Copy `.github/workflows/lint.yml`; adjust paths
- [ ] Create `*.toolspec.yaml` for command contract
- [ ] Add shell completion section to README
- [ ] Add aliases section to README
- [ ] Wire `Root.LoadAliases()` in main (Go)
- [ ] Wire `Expander` in CLI entry point (TS)
- [ ] Run `make lint` locally before first push
- [ ] Verify CI passes on first PR
