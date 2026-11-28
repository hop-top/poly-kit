# skillsync

## What it answers

What do the agent skills and their install channels say, given the capability
parity page, the publish matrix and each skill's frontmatter, and does every
skill still obey the authoring contract? `sync` derives the text so no skill
carries a second copy that can drift; `lint` checks the rest. Writing a skill
is not its job: the contract is
[`docs/contributors/agent-skills.md`](../../docs/contributors/agent-skills.md).

## Use it when

- you changed the parity page, the publish matrix, the tiers page or
  `ports.yaml` → `make skills-sync`
- CI or a pre-commit check must fail on stale output or a broken skill →
  `make check-skills` (`check-skills-sync`, then `lint-skills`)
- a mirror-tree test checks one skill alone →
  `go run ./internal/tools/skillsync lint --standalone <skill dir>`
- a script needs the mirror and path of a component's skill →
  `go run ./internal/tools/skillsync expected --component kit-py`

## Contract

Commands: `sync [--check] [--require-all]`, `expected --component <name>`,
`lint [--standalone] [dir...]`. Input paths are flags (`--root --parity
--publish --tiers --manifest --ports`) that default to this repository's.
Exit codes: 0 ok, 1 drift, findings or inconsistent inputs, 2 usage or I/O.

`sync` writes only into skill directories whose `SKILL.md` exists
(`--require-all` makes a missing one an error): per skill `plugin.json`,
`gym.json`, managed blocks and generated references; at the root `skills.json`
and `claude-marketplace-entries.json`. A managed block sits between
`<!-- skills-sync:begin <id> -->` and `<!-- skills-sync:end <id> -->`; a
missing or misordered marker fails the run. `sync --check` prints each
differing path and writes nothing. It also owns: name equal to the
`ports.yaml` skill name, a description and a license, and the markers.
`version` is written once from the release manifest, then preserved.
Skill, package and mirror paths come from the publish matrix; skill URLs use
`mirror_branch`, every docs URL the single `docs.branch`. `expected` prints
mirror, repository skill directory and mirror skill directory, tab
separated.

`lint` checks the skill directories `ports.yaml` names (default: those that
exist; none is a pass) and prints `path:line: rule: message`, sorted. A
whole-file finding is on line 1. `--standalone` checks any directory with
the rules marked `s` and nothing outside it.

| Rule | Checks | s |
|------|--------|---|
| S1 | frontmatter keys, name pattern, length, directory; description length, `Use when`; block-style YAML | s |
| S2 | `SKILL.md` 120 lines, reference 200, allowed layout | s |
| S3 | every file under `references/`, `scripts/`, `assets/snippets/` named in `SKILL.md`; relative links exist | s |
| S4 | no relative link leaves the skill; links into the org are a skill URL, a docs URL whose path exists, the one branch | s (relative) |
| S5 | `references/<area>.md` only if the port has a done, partial or unverified capability there | |
| S6 | `metadata.version` equals the release manifest and is annotated; no other unannotated `x.y.z` | |
| S7 | named `@hop-top/kit/<sub>`, `hop_top_kit.<mod>`, Cargo features, `hop.top/kit/go/<pkg>`, `HopTop\Kit\<Ns>` exist | |
| S8 | no tracker id, no `tlc://`, no fence over 15 lines outside `assets/snippets/` | s |
| S9 | scripts: `#!/bin/sh` first line, executable | s |
| S10 | plugin manifest name, version, keys; no `skills/`, `bin/`, `CLAUDE.md`, marketplace file | s (not the repo-wide marketplace scan) |

Bounds: S4 checks only URLs under the organisations of `docs.repo` and the
mirrors; other hosts are free. S7 checks the first segment of a Python
module and PSR-4 paths for PHP. Not enforced by tooling: script safety
(read-only, no network), snippet execution, section order of `SKILL.md`.
Tracker-id shape and the three-part version shape are heuristics.

`ports.yaml` keys: `docs`, `host`, `mirror_branch`, `author`, `category`,
`keywords`, `reference_column`, `router`, `ports`, `no_skill`, `copies`.

## Neighbours

- [`agent-skills.md`](../../docs/contributors/agent-skills.md): the contract
- [`capability-parity.md`](../../docs/adopters/reference/capability-parity.md):
  the source of every table `sync` writes
- `make lint-internal-refs`, `make lint-adr-refs`: scan skill files too; not
  repeated here
- [`internal/tools/`](../README.md): the other build-time Go programs
