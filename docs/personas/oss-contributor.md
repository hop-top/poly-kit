---
id: oss-contributor
name: "OSS Contributor"
role: "Contributes to any hop-top repo"
languages: [go, ts, python]
---

## Context

External or internal contributor submitting PRs.
May be first-time contributor or occasional drive-by fixer.
Needs to orient fast, pass CI, get merged without friction.

## Needs

- Clear contribution guide (setup, test, lint, PR template)
- Passing CI on first submission -- no hidden local-only gates
- Reviewable PRs with obvious scope and intent
- Consistent tooling across all hop-top repos

## Pain points

- Unclear conventions; different repos use different patterns
- Inconsistent tooling -- some repos use make, others task, others just
- CI failures from undocumented lint/format requirements
- PR feedback cycles caused by convention mismatches

## Success criteria

- First PR submitted and merged without convention confusion
- CI green on first push after following contribution guide
- Reviewer focuses on logic, not style/convention fixes
- Contributor returns for second PR

## Referenced in

- [plans/2026-04-04-conventions-design.md](../plans/2026-04-04-conventions-design.md)
  — base persona in the kit conventions hierarchy; `kit-contributor`
  extends this one.
