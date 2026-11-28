---
name: acme-kit
description: Skill acme-kit. Use when testing.
license: MIT
compatibility: any
metadata:
  version: "1.0.0" # x-release-please-version
  min_version: "1.4.0-beta.2"
---

# acme-kit

Hand-written intro.

<!-- skills-sync:begin ports -->
| Port | Skill | Package | Install | Tier | Status counts |
|---|---|---|---|---|---|
| Go | [`acme-kit-go`](https://example.test/acme/kit/blob/main/skills/acme-kit-go/SKILL.md) | `acme.test/kit` | `go get acme.test/kit` | reference | 8 done |
| TypeScript | [`acme-kit-ts`](https://example.test/acme/kit-ts/blob/main/skills/acme-kit-ts/SKILL.md) | `@acme/kit` | `pnpm add @acme/kit` | supported | 2 done, 1 unverified, 4 none, 1 n/a |
| Python | [`acme-kit-py`](https://example.test/acme/kit-py/blob/main/skills/acme-kit-py/SKILL.md) | `acme-kit` | `pip install acme-kit` | supported | 2 done, 1 unverified, 4 none, 1 n/a |
| Rust | [`acme-kit-rs`](https://example.test/acme/kit-rs/blob/main/skills/acme-kit-rs/SKILL.md) | `acme-kit` | `cargo add acme-kit` | experimental | 2 done, 1 partial, 2 unverified, 2 none, 1 n/a |
| PHP | [`acme-kit-php`](https://example.test/acme/kit-php/blob/main/skills/acme-kit-php/SKILL.md) | `acme/kit` | `composer require acme/kit` | experimental | 1 done, 1 partial, 5 none, 1 n/a |
<!-- skills-sync:end ports -->

<!-- skills-sync:begin version -->
## Versions

Written for 1.0.0 # x-release-please-version
Expects 1.4.0-beta.2 or newer.

The `kit` binary: `kit --version` prints `kit v<version>` (a source build prints `kit vdev`: nothing
to compare); older than expected: upgrade it (`brew upgrade acme/tap/kit`; [other
routes](https://example.test/acme/mono/blob/next/cmd/kit/README.md)); newer than written for: update
this skill. Port skills carry their own Versions block; update them the same way. Update by install
channel, then restart the harness: Claude Code plugin
`claude plugin marketplace update acme-market`, then `claude plugin update acme-kit@acme-market`
(marketplace repository `acme/skillset`); gym, `npx skills` or by hand: take `skills/acme-kit` again
from `acme/kit` at `main`.
<!-- skills-sync:end version -->

Hand-written outro.
