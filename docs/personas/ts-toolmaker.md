---
id: ts-toolmaker
name: "TS Toolmaker"
role: "Builds TypeScript CLI tools with kit packages"
extends: cli-author
languages: [ts]
---

## Context

TypeScript developer building tools like idx, eva-pkg.
Expects kit to provide same foundation Go toolmakers get.
Uses Commander for CLI parsing; needs parity with Go behaviour.

## Needs

- Commander setup via shared factory (`createCLI()`)
- Table/JSON/YAML output matching Go conventions
- Consistent flags (--format, --verbose, --config, --no-color)
- Config loading with same precedence as Go counterpart

## Pain points

- No shared TS CLI foundation; each tool wires Commander differently
- Output formatting hand-rolled per project
- Flag names/defaults drift from Go tools over time
- No upgrade check mechanism in TS tools

## Success criteria

- `createCLI()` gives full-featured CLI matching Go behaviour
- Same --format flag produces identical output across languages
- Config file discovered at same XDG paths as Go tools
- Upgrade check works identically to Go implementation
