---
id: py-toolmaker
name: "Python Toolmaker"
role: "Builds Python CLI tools with kit packages"
extends: cli-author
languages: [python]
---

## Context

Python developer building tools like eva, eva-ee.
Uses Typer/Click for CLI parsing; needs parity with Go behaviour.
Expects kit to eliminate per-project wiring entirely.

## Needs

- Typer app factory (`create_app()`) with standard config
- Table/JSON/YAML output matching Go conventions
- Consistent flags (--format, --verbose, --config, --no-color)
- Config loading with same precedence as Go counterpart

## Pain points

- No shared Python CLI foundation; Typer/Click setup varies per tool
- Output formatting hand-rolled; no shared table/json renderer
- Flag names/defaults drift from Go tools over time
- No upgrade check mechanism in Python tools

## Success criteria

- `create_app()` gives full-featured CLI matching Go behaviour
- Same --format flag produces identical output across languages
- Config file discovered at same XDG paths as Go tools
- Upgrade check works identically to Go implementation
