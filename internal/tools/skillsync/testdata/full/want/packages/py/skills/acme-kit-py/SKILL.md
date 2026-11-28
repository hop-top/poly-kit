---
name: acme-kit-py
description: Skill acme-kit-py. Use when testing.
license: MIT
compatibility: any
metadata:
  version: "1.0.0" # x-release-please-version
  min_version: "0.3.0-alpha.2"
---

# acme-kit-py

Hand-written intro.

<!-- skills-sync:begin coverage -->
Tier: supported.

- CLI and console. Lacks: Markdown rendering.
- Serving and transports. Has only: Serve lifecycle.
<!-- skills-sync:end coverage -->

<!-- skills-sync:begin version -->
## Versions

Written for 1.0.0. # x-release-please-version
Expects 0.3.0-alpha.2 or newer.

Installed: `pip show acme-kit` or `uv pip show acme-kit`; Python may print the PEP 440 spelling (a,
b, rc for -alpha., -beta., -rc.). `scripts/detect-port.sh` shows what the manifest pins. Older than
expected: tell the user, upgrade (`pip install -U --pre acme-kit`). Newer than written for: this
copy may be stale; update it, then re-read it. Update by install channel, then restart the harness:
Claude Code plugin `claude plugin marketplace update acme-market`, then
`claude plugin update acme-kit-py@acme-market` (marketplace repository `acme/skillset`); gym,
`npx skills` or by hand: take `skills/acme-kit-py` again from `acme/kit-py` at `main`.
<!-- skills-sync:end version -->

Hand-written outro.
