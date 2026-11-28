# Install a port skill

Manual channel: copy or link one skill directory. The other channels
(skill managers, a plugin marketplace) are not covered here yet.

1. Find the port's mirror and skill name in the ports table of `SKILL.md`.
   Each skill lives at `skills/<name>` in the mirror that publishes it.
2. Clone the mirror, or download only that directory.
3. Copy or symlink `skills/<name>` into a directory your harness scans.
   A skill is a directory holding `SKILL.md`; copy the whole directory,
   because it carries its own references and scripts.

| Harness | Project directory | User directory |
|---------|-------------------|----------------|
| Claude Code | `.claude/skills/` | `.claude/skills/` in the home directory |
| Any client that follows the cross-client convention | `.agents/skills/` | `.agents/skills/` in the home directory |

A project directory shadows a user directory of the same skill name. For
another harness, use the directory its own documentation names; the
convention is described in
[adding skills support](https://agentskills.io/client-implementation/adding-skills-support).
Restart or reload the harness if it lists skills only at startup.
