---
name: acme-kit-php
description: >-
  Skill acme-kit-php. Use when testing.
license: MIT
compatibility: any
metadata:
  version: "1.0.0" # x-release-please-version
  min_version: "0.3.0-alpha.1"
---

# acme-kit-php

Hand-written intro.

<!-- skills-sync:begin coverage -->
Tier: experimental.

- CLI and console. Has only: CLI factory.
- Serving and transports. Has only: Serve lifecycle.
<!-- skills-sync:end coverage -->

<!-- skills-sync:begin version -->
## Versions

Written for 1.0.0. # x-release-please-version
Expects 0.3.0-alpha.1 or newer.

Installed: `composer show acme/kit`; `scripts/detect-port.sh` shows what the manifest pins. Older
than expected: tell the user, upgrade (`composer update acme/kit`). Newer than written for: this
copy may be stale; update it, then re-read it. Update by install channel, then restart the harness:
Claude Code plugin `claude plugin marketplace update acme-market`, then
`claude plugin update acme-kit-php@acme-market` (marketplace repository `acme/skillset`); gym,
`npx skills` or by hand: take `skills/acme-kit-php` again from `acme/kit-php` at `main`.
<!-- skills-sync:end version -->

Hand-written outro.
