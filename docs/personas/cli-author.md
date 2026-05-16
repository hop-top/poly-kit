---
id: cli-author
name: "CLI Author"
role: "Builds CLIs with kit in any language"
languages: [go, ts, python]
---

## Context

Polyglot developer building hop-top CLI tools.
Works across Go, TypeScript, Python -- expects uniform behaviour
regardless of language. Ships multiple CLIs per quarter.

## Needs

- Consistent CLI behaviour across languages (flags, env, exit codes)
- Themed output (colors, tables, spinners) via kit conventions
- Config loading (files, env, flags) with predictable precedence
- Upgrade checks baked into every tool automatically

## Pain points

- Duplicated boilerplate across tools; each CLI re-invents basics
- Inconsistent flag handling between languages
- Output formatting differs tool-to-tool; no shared theme
- Config precedence logic copied/tweaked per project

## Success criteria

- New CLI initialized in <5 min with all standard flags working
  (via `kit init`)
- Zero flag/config divergence between Go, TS, Python CLIs
- Themed output identical across languages for same data
- Upgrade check present without per-tool wiring
