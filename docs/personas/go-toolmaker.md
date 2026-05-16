---
id: go-toolmaker
name: "Go Toolmaker"
role: "Builds Go CLI tools with kit packages"
extends: cli-author
languages: [go]
---

## Context

Go developer building tools like tlc, rsx, ben, mdl.
Primary kit consumer -- deepest integration surface.
Relies on cobra, viper, fang, bubbletea, lipgloss stack.

## Needs

- Cobra + Viper + Fang wiring via kit conventions package
- SQLite store (sqlstore) for local persistence
- XDG-compliant paths for config, data, cache, state
- TUI components (charm v2) for interactive flows
- Markdown rendering with hop.top theme

## Pain points

- Each tool reinvents config/storage/XDG path setup
- Charm v2 migration pain -- API surface changed significantly
- Cobra boilerplate duplicated across every new command
- No shared SQLite schema migration pattern

## Success criteria

- Import kit packages, get production CLI with zero custom wiring
- `conventions.New()` yields configured app with all subsystems
- TUI components work out of box with hop.top theme
- SQLite store initialized at correct XDG path automatically
