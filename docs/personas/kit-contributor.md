---
id: kit-contributor
name: "Kit Contributor"
role: "Contributes to kit packages specifically"
extends: oss-contributor
languages: [go, ts, python]
---

## Context

Contributor adding or modifying kit packages specifically.
Must understand cross-language contracts -- a change in Go config
loading must match TS and Python behaviour. Works across the
monorepo boundary between conventions, sdk/ts/, sdk/py/ packages.

## Needs

- Cross-language test suite validating behavioural parity
- Lint gate covering all three languages in one pass
- Persona and story awareness -- know who changes affect
- Clear package boundaries and dependency rules

## Pain points

- Changes in one language break contracts in another
- No unified gate -- must run Go, TS, Python checks separately
- Hard to know which personas a change impacts
- Cross-language behavioural drift discovered late (in consumers)

## Success criteria

- `task check` validates all languages before PR
- Contract tests catch cross-language drift at PR time
- Persona impact visible in PR template or CI output
- Single command runs full lint + test + build gate

## Referenced in

- [features/FT-0002.md](../features/FT-0002.md) — reproducible dev
  environment feature targets this persona.
- [oss-contributor.md](oss-contributor.md) — the base persona this
  one extends.
