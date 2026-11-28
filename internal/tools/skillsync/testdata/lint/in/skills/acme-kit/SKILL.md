---
name: acme-kit
description: >-
  Picks an Acme kit port for a language. Use when a task needs the kit and no language is chosen yet.
license: MIT
compatibility: any
metadata:
  version: "1.4.0-beta.2" # x-release-please-version
---

# acme-kit

Pick a port, then open its skill.

Choose by language, then open [acme-kit-go](https://example.test/acme/kit/blob/main/skills/acme-kit-go/SKILL.md).

<!-- skills-sync:begin ports -->
<!-- skills-sync:end ports -->

<!-- skills-sync:begin version -->
<!-- skills-sync:end version -->

## Open when

| File | Open when |
|---|---|
| [references/capability-matrix.md](references/capability-matrix.md) | comparing what each port covers |
| [references/go-only.md](references/go-only.md) | the task needs a capability only Go has |
| [scripts/detect.sh](scripts/detect.sh) | the language of the project is not known |

Source of truth: [capability parity](https://example.test/acme/mono/blob/next/parity.md).
