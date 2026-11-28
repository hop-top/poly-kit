---
name: acme-kit-go
description: Skill acme-kit-go. Use when testing.
license: MIT
compatibility: any
metadata:
  version: "1.0.0" # x-release-please-version
  min_version: "1.4.0-beta.2"
---

# acme-kit-go

Hand-written intro.

<!-- skills-sync:begin coverage -->
Tier: reference.

- CLI and console. Lacks: nothing.
- Serving and transports. Lacks: nothing.
<!-- skills-sync:end coverage -->

<!-- skills-sync:begin version -->
## Versions

Written for 1.0.0 # x-release-please-version
Expects 1.4.0-beta.2 or newer.

Installed: `go list -m acme.test/kit`; `scripts/detect-port.sh` shows what the manifest pins. Older
than expected: tell the user, upgrade (`go get acme.test/kit@latest`). Newer than written for: this
copy may be stale; update it, then re-read it. The `kit` binary: `kit --version` prints
`kit v<version>` (a source build prints `kit vdev`: nothing to compare); older than expected:
upgrade it (`brew upgrade acme/tap/kit`; [other
routes](https://example.test/acme/mono/blob/next/cmd/kit/README.md)); newer than written for: update
this skill. Update by install channel, then restart the harness: Claude Code plugin
`claude plugin marketplace update acme-market`, then `claude plugin update acme-kit-go@acme-market`
(marketplace repository `acme/skillset`); gym, `npx skills` or by hand: take `skills/acme-kit-go`
again from `acme/kit` at `main`.
<!-- skills-sync:end version -->

Hand-written outro.
