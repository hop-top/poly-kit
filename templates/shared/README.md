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
