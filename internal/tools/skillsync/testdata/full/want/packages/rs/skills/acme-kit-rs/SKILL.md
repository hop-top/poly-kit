---
name: acme-kit-rs
description: Skill acme-kit-rs. Use when testing.
license: MIT
compatibility: any
metadata:
  version: "1.0.0" # x-release-please-version
  min_version: "0.3.0-alpha.3"
---

# acme-kit-rs

Hand-written intro.

<!-- skills-sync:begin coverage -->
Tier: experimental.

- CLI and console. Lacks: Markdown rendering.
- Serving and transports. Lacks: Job queue.
<!-- skills-sync:end coverage -->

<!-- skills-sync:begin version -->
## Versions

Written for 1.0.0 # x-release-please-version
Expects 0.3.0-alpha.3 or newer.

Installed: `cargo tree -i acme-kit --depth 0`; `scripts/detect-port.sh` shows what the manifest
pins. Older than expected: tell the user, upgrade (`cargo update -p acme-kit`). Newer than written
for: this copy may be stale; update it, then re-read it. Update by install channel, then restart the
harness: Claude Code plugin `claude plugin marketplace update acme-market`, then
`claude plugin update acme-kit-rs@acme-market` (marketplace repository `acme/skillset`); gym,
`npx skills` or by hand: take `skills/acme-kit-rs` again from `acme/kit-rs` at `main`.
<!-- skills-sync:end version -->

Hand-written outro.
