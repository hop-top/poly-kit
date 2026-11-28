---
name: acme-kit-ts
description: Skill acme-kit-ts. Use when testing.
license: MIT
compatibility: any
metadata:
  version: "1.0.0" # x-release-please-version
  min_version: "0.3.0-alpha.4"
---

# acme-kit-ts

Hand-written intro.

<!-- skills-sync:begin coverage -->
Tier: supported.

- CLI and console. Lacks: Markdown rendering.
- Serving and transports. Has only: Serve lifecycle.
<!-- skills-sync:end coverage -->

<!-- skills-sync:begin version -->
## Versions

Written for 1.0.0 # x-release-please-version
Expects 0.3.0-alpha.4 or newer.

Installed: `npm ls @acme/kit` or `pnpm ls @acme/kit`; `scripts/detect-port.sh` shows what the
manifest pins. Older than expected: tell the user, upgrade (`pnpm add @acme/kit@latest`). Newer than
written for: this copy may be stale; update it, then re-read it. Update by install channel, then
restart the harness: Claude Code plugin `claude plugin marketplace update acme-market`, then
`claude plugin update acme-kit-ts@acme-market` (marketplace repository `acme/skillset`); gym,
`npx skills` or by hand: take `skills/acme-kit-ts` again from `acme/kit-ts` at `main`.
<!-- skills-sync:end version -->

Hand-written outro.
