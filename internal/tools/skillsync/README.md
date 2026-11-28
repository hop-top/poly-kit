# skillsync

## What it answers

What do the agent skills and their install channels say, given the capability
parity page, the publish matrix and each skill's frontmatter? It derives that
text so no skill carries a second copy that can drift. Writing a skill is not
its job: the contract is
[`docs/contributors/agent-skills.md`](../../docs/contributors/agent-skills.md).

## Use it when

- you changed the parity page, the publish matrix, the tiers page or
  `ports.yaml` → `make skills-sync`
- CI or a pre-commit check must fail on stale output → `make check-skills-sync`
- a script needs the mirror and path of a component's skill →
  `go run ./internal/tools/skillsync expected --component kit-py`

## Quick start

```sh
make skills-sync          # write
make check-skills-sync    # fail on drift, write nothing
```

With no skill present it writes only the two root files and says
`0 of 6 skills present`.

## Contract

Commands: `sync [--check] [--require-all]`, `expected --component <name>`.
Input paths are flags (`--root --parity --publish --tiers --manifest --ports`)
that default to this repository's.

Output is written only into skill directories whose `SKILL.md` exists;
`--require-all` makes a missing expected skill an error.

| Output | Where |
|--------|-------|
| `references/capability-matrix.md`, `references/go-only.md`, managed block `ports` | router skill |
| managed block `coverage`, `references/coverage.md`, `references/other-ports.md` | each port skill |
| every `copies` entry | each destination skill |
| `.claude-plugin/plugin.json`, `gym.json` | each skill |
| `skills.json`, `claude-marketplace-entries.json` | repository root |

- A managed block sits between `<!-- skills-sync:begin <id> -->` and
  `<!-- skills-sync:end <id> -->`; a missing or misordered marker fails the run.
- `version` in `plugin.json` and `skills.json` is written once from the
  release manifest, then preserved. A release PR never fails `--check`.
- Skill, package and mirror paths come from the publish matrix. A component
  in neither `ports` nor `no_skill`, or the reverse, fails naming it.
- Skill URLs use `mirror_branch`; every docs URL uses the single
  `docs.branch`. Change either in `ports.yaml` and regenerate.
- Fails, in write and check mode, before writing anything: unknown status
  word, capability row without seven columns (file and line), a Summary row
  that disagrees with the rows, a port without a tier.
- `sync --check` prints each differing path, then `run: make skills-sync`.
- `expected` prints `mirror`, repository skill directory and mirror skill
  directory, tab separated, per skill; nothing for a `no_skill` component.
- Exit codes: 0 ok, 1 drift or inconsistent inputs, 2 usage or I/O failure.

`ports.yaml` keys: `docs`, `host`, `mirror_branch`, `author`, `category`,
`keywords`, `reference_column`, `router`, `ports` (component, skill, column,
language, install, package, tier, toolchain, keywords), `no_skill`, `copies`.

## Neighbours

- [`docs/contributors/agent-skills.md`](../../docs/contributors/agent-skills.md):
  the authoring contract
- [`capability-parity.md`](../../docs/adopters/reference/capability-parity.md):
  the source of every table it writes
- `make check-mirror-sync`: the same generate-then-fail-on-drift shape

## See also

- [`internal/tools/`](../README.md): the other build-time Go programs
