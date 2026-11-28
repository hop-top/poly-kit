# Agent skills: authoring contract

The rules every agent skill in this repository follows, so all of them are
written against one text. A skill is a directory an agent harness loads to
learn how to use kit; this page says what goes in it, what stays out, and
which parts are generated.

Rules marked *enforced* are checked by tooling that lands separately from
this page (the generator and the skill lint). Until it lands, reviewers
apply them by hand. Nothing here claims a check already runs.

## Who this is for

Contributors writing or changing a skill, and reviewers of that change.
Adopters installing a skill read the skill itself, not this page.

## Skills and where they live

One format: an [agentskills.io](https://agentskills.io) skill directory
(`SKILL.md` with `name`, `description`, `license`, `compatibility`,
`metadata` frontmatter). No per-harness variants. A skill lives beside the
component it describes, so the mirror split that publishes the component
carries it with no workflow change.

| Skill | Directory in this repository | Path in the mirror |
|-------|------------------------------|--------------------|
| `hop-top-kit` (router) | `skills/hop-top-kit/` | `hop-top/kit`: `skills/hop-top-kit/` |
| `hop-top-kit-go` | `skills/hop-top-kit-go/` | `hop-top/kit`: `skills/hop-top-kit-go/` |
| `hop-top-kit-ts` | `sdk/ts/skills/hop-top-kit-ts/` | `hop-top/kit-ts`: `skills/hop-top-kit-ts/` |
| `hop-top-kit-py` | `sdk/py/skills/hop-top-kit-py/` | `hop-top/kit-py`: `skills/hop-top-kit-py/` |
| `hop-top-kit-rs` | `sdk/rs/skills/hop-top-kit-rs/` | `hop-top/kit-rs`: `skills/hop-top-kit-rs/` |
| `hop-top-kit-php` | `sdk/experimental/php/skills/hop-top-kit-php/` | `hop-top/kit-php`: `skills/hop-top-kit-php/` |

The router picks a port; each port skill covers one language. A language
with no port is served by running the `kit` binary as a sidecar; the router
says so. The PHP skill is deliberately thin and marked experimental.

Install channels, all reading the same files from the mirrors: the gym
skill manager, `npx skills add hop-top/<mirror>`, a manual copy or symlink
of `skills/<name>` into the harness directory, and a Claude Code plugin
from a shared marketplace (see [Plugin packaging](#plugin-packaging)).

## Layout

```text
<skill>/
  SKILL.md
  references/<topic>.md
  scripts/<name>.sh
  assets/snippets/<name>/<files>
  .claude-plugin/plugin.json     generated
  gym.json                       generated
```

- Nothing else. No other top-level file or directory.
- Nothing deeper than `assets/snippets/<name>/`.
- `references/` and `scripts/` are flat.
- `.claude-plugin/plugin.json` and `gym.json` are the two generated channel
  files; see [Generated content](#generated-content).

## Plugin packaging

Every skill directory is also one Claude Code plugin whose root is the skill
directory itself. `SKILL.md` sits at the plugin root, which Claude Code
loads as a single skill. Plugin name equals skill name equals directory
name.

Consequences, each *enforced*:

- no nested `skills/` directory inside a skill
- no `skills`, `commands`, `hooks` or `mcpServers` key in the plugin
  manifest
- no `bin/` directory and no `CLAUDE.md` in a skill
- no `.claude-plugin/marketplace.json` anywhere in this repository: the
  marketplace is shared by every hop-top project, each repository
  contributing its own plugins, and lives elsewhere. Neither this repository
  nor a mirror is a marketplace.

## One source for channel metadata

| Datum | Source |
|-------|--------|
| `name`, `description`, `license` | `SKILL.md` frontmatter |
| language label, install command, toolchain floor, mirror, keywords, copy list | `internal/tools/skillsync/ports.yaml` |
| capability tables, statuses, tiers | [`capability-parity.md`](../adopters/reference/capability-parity.md) and [`sdk-support-tiers.md`](sdk-support-tiers.md) |
| component directory and mirror per port | the `ecosystems` map in `.github/workflows/publish.yml` |

The plugin manifest, `gym.json`, `skills.json` and the marketplace entries
are generated from those and never edited by hand. A new channel adds a
generator output, not a hand-kept file.

## Caps and frontmatter

| Rule | Limit |
|------|-------|
| `SKILL.md` | at most 120 lines |
| a file under `references/` | at most 200 lines |
| `name` | equals the directory name; lowercase letters, digits, hyphens; at most 64 characters |
| `description` | at most 1024 characters; contains `Use when` |
| frontmatter keys | `name`, `description`, `license`, `compatibility`, `metadata`, `allowed-tools` only; block-style YAML only |

All *enforced*. A reference that outgrows its cap is split by topic, not
trimmed to fit.

## Progressive discovery

`SKILL.md` is read first and fully; everything below it is read on demand.
It holds:

- when to use this skill, and when to use another
- the default path: the few commands or steps most tasks need
- one `Open when` table, a cue per deeper file

Every reference, script and snippet is named in that table with the cue
that makes an agent open it (*enforced*). A file nobody is told about is
dead weight; a cue that says "more details" is not a cue.

What `--help` or a reference page already states is linked, never copied.
A copy drifts; a link does not.

## Generated content

Files and managed blocks owned by `make skills-sync` are never edited by
hand. A managed block sits between marker lines:

```text
<!-- skills-sync:begin <id> -->
...generated...
<!-- skills-sync:end <id> -->
```

Edit the source (parity page, `ports.yaml`, `SKILL.md` frontmatter) and
regenerate. A change that leaves generated output out of date fails the
drift check (*enforced*): the same generate-then-fail-on-drift shape as the
other sync targets in the Makefile.

## Links

- Inside a skill directory: relative.
- Everything else: absolute. An installer copies one skill alone, so a
  relative link to a sibling skill or to a docs page is dead after install.
- Never a relative link that leaves the skill directory (*enforced*).

Absolute forms; no other branch, no other host:

| Target | Form |
|--------|------|
| another skill | `https://github.com/hop-top/<mirror>/blob/main/skills/<name>/SKILL.md` |
| a docs page | `https://github.com/hop-top/poly-kit/blob/main/docs/<path>` |

The mirror is the one that publishes that skill (table above). Installers
copy bytes and gym pins a content digest, so links are never rewritten at
install time.

## Snippets

- Code longer than 15 lines is a file under `assets/snippets/<name>/`, not
  inline in a markdown file.
- A snippet is executed or compiled by the port's own test suite, so it
  cannot rot. A snippet no test touches is not allowed.
- Expected output lives in `<name>.expected`. A `*.out` file is ignored by
  the repository `.gitignore` and never lands.
- Source files under `skills/` owe no directory `README.md`: the lint
  prunes directories named `skills` (see
  [`readme-guide.md`](readme-guide.md)), because a skill is entered through
  its `SKILL.md`.

## Scripts

- `#!/bin/sh`, POSIX only: no bashisms.
- Read-only: reports, never changes the caller's files or system.
- No network.
- Exit codes documented in the header comment.
- Named in the `Open when` table with its cue, like any other file.

## Versions

`metadata.version` carries the annotation `# x-release-please-version` on
its line; the release tooling bumps it with the component. A version number
appears only on a line with that annotation (*enforced*): everywhere else a
skill says "the current release" and links the source. The plugin manifest
`version` is written once at creation and left to the release tooling after.

## Never in a skill

A skill is public and installed far from this repository. It never carries:

- tracker ids or track names
- paths outside this repository
- machine or account names
- names of projects that use kit
- decision-record numbers
- migration or breaking-change notes while the component is pre-stable
- attribution: authors of the text, tools or assistants used to write it

The same list applies to generated output, so its sources carry none either.
The repository-wide lint for references to documents and paths outside the
repository covers skills like any other tracked file.

## Keeping skills true

A skill that states a capability the port lacks, a command that no longer
exists or a link that 404s is worse than no skill. Gates that hold the
contract in place are listed here as they land, each with the `make` target
that runs it. No gate is listed yet.

| Gate | Target | Checks |
|------|--------|--------|
| (none yet) | | |
