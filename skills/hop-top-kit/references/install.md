# Install and update a port skill

Find the port's mirror and skill name in the ports table of `SKILL.md`. A
skill lives at `skills/<name>` in the mirror that publishes it. Pick one
channel; restart or reload the harness afterwards if it lists skills only at
startup. The update and install commands below were run against local
stand-ins for the mirrors and the marketplace; `<...>` marks what to fill in.

## Claude Code plugin

The shared marketplace is `hop-top/skillset` (marketplace name `hop-top`).

```sh
claude plugin marketplace add hop-top/skillset
claude plugin install <name>@hop-top
```

Update: `claude plugin marketplace update hop-top`, then
`claude plugin update <name>@hop-top`. The update reports the new version and
asks for a restart. `claude plugin list` shows the installed version.

## npx skills

Package `skills` (`npx skills`), run with the release of 2026-11-11; later releases are untested.

```sh
npx skills add <owner>/<mirror> -s <name> -a claude-code -a codex -g -y
```

- With `-g` it writes `.agents/skills/<name>` in the home directory, which
  Codex reads, and a symlink `.claude/skills/<name>` there for Claude Code. Without `-g` the same
  layout goes under `./.agents/skills` and `./.claude/skills` of the project.
- `--copy` copies into each agent directory instead of linking.
- The directory is copied whole, `.claude-plugin/` and `gym.json` included.
- `npx skills add <mirror-url-or-dir> --list` lists what a source holds.

Update, for a GitHub source (the install is recorded in
`.agents/.skill-lock.json` in the home directory, project scope
`skills-lock.json`):
`npx skills update -g -y`, or `npx skills update -p -y` for the project.
A source given as a local path or a non-GitHub git URL is not tracked: run the
same `npx skills add` again, which replaces the files. `npx skills remove <name>`
removes a skill.

## gym

gym needs a validator named `skills-ref` on `PATH`. The PyPI package
`skills-ref` installs the command `agentskills`, so `gym learn` fails with
`validation failed: exec: "skills-ref": executable file not found` until a
`skills-ref` link to `agentskills` exists.

```sh
gym coach hire <location of skills.json>
gym learn <name>
```

The catalog is `skills.json` at the root of the `hop-top/kit` repository.
Without `-g`, gym installs into `./.gym/skills`, which no harness scans; with
`-g` it writes to the global skills directory of each harness it detects
(`~/.claude/skills/<name>` for Claude Code). gym copies `.claude-plugin/` and
accepts `gym.json`; `gym guard test <dir>` reports a warning only for an
unreviewed skill, an unpinned digest, and a script text that mentions a
package installer.

Update: `gym update` refreshes the catalog and lists upgrades, then
`gym train` upgrades the installed skills.

## Manual copy

Copy or symlink the whole `skills/<name>` directory (it carries its own
references and scripts) into a directory your harness scans.

| Harness | Project directory | User directory |
|---------|-------------------|----------------|
| Claude Code | `.claude/skills/` | `.claude/skills/` in the home directory |
| Any client that follows the cross-client convention | `.agents/skills/` | `.agents/skills/` in the home directory |

A project directory shadows a user directory of the same skill name. For
another harness, use the directory its own documentation names; the
convention is described in
[adding skills support](https://agentskills.io/client-implementation/adding-skills-support).
Update by taking the directory again from the mirror.

A copied skill directory carries `.claude-plugin/plugin.json`, so Claude Code
loads it as a skills-directory plugin: `claude plugin list` shows
`<name>@skills-dir` with the version from that manifest.

## Registry packages

The npm package `@hop-top/kit` ships the TypeScript skill: the copy in the
`skills/hop-top-kit-ts` directory of the installed package (under
`node_modules`) matches the installed package version. Link or copy it as in
the manual channel.
