# SDK support tiers

What "experimental" and "supported" mean for a kit SDK, which modules a
supported SDK must ship, and the checklist an experimental SDK clears to
graduate. Applies to the PHP and Rust SDKs today.

Status: the tier definitions and the checklist are **proposed**. Items
marked *(proposed)* are not yet decided by the maintainers; the
[open decisions](#open-decisions) section lists them. Nothing here changes
what ships.

## Who this is for

Contributors working on the PHP or Rust SDK, and reviewers deciding
whether a change moves one of them toward supported. Adopters choosing an
SDK should read
[`capability-parity`](../adopters/reference/capability-parity.md).

## Tiers

| Tier | Meaning | Today |
|------|---------|-------|
| Supported | Ships the required surface below. Every shared contract gate and cross-language harness runs it in CI with no skips. Breaking changes follow the release policy in [`releasing.md`](releasing.md) | Python (`sdk/py`), TypeScript (`sdk/ts`) |
| Experimental | Published on its own prerelease line, may lack required surface, may skip a gate. No promise that the API holds between prereleases | PHP (`sdk/experimental/php`), Rust (`sdk/rs`) *(proposed classification for Rust)* |

Rust lives under `sdk/rs/`, not `sdk/experimental/`, because it already
carries its own release component and a package shape of a supported SDK.
Its tier is decided by the checklist below, not by its directory. The
two directories only record where a port started.

A tier describes the SDK as a whole. A single missing module does not
demote a supported SDK, but a supported SDK that drops below the required
surface or starts skipping a gate returns to experimental until it is
back.

## Required surface

The modules a supported SDK ships, named by the Go package they port.
Each links to the row in the
[parity inventory](sdk-parity-inventory.md), which holds the per-port
status. *(proposed)*

| Area | Go packages | Why required |
|------|-------------|--------------|
| CLI factory and global flags | `console/cli` | Every adopter CLI starts here; the flags are the user-visible contract |
| Output and errors | `console/output` | `--format`, column order and the exit-code taxonomy |
| Config and paths | `core/config`, `core/xdg` | Layering and file locations must match across languages or tools disagree about where state lives |
| Serve lifecycle | `console/serve` | Pinned by the serve contract |
| MCP surface | `transport/cmdsurface` (MCP leg) | Pinned by the MCP wire corpus |
| Telemetry and consent | `runtime/telemetry`, `core/consent` | Opt-in egress must behave identically everywhere |
| Redaction | `core/redact` | Egress safety; the telemetry-only regex set is not enough |
| Network policy | `core/netpolicy` | `--offline` must hold |
| Delegation safety | `console/cli/policy`, `runtime/sideeffect` | Default-closed destructive commands |
| TypeID | `core/id` | Cross-language identifiers |

Out of scope for the supported tier: `ai/llm`, `console/wizard`,
`console/tui`, the storage backends, jobs, peers, and the Go-internal
packages listed in the inventory. An SDK may ship them; none blocks
graduation. *(proposed)*

Two modules are open decisions, see below: the 12-factor compliance
self-check (`core/compliance`) and the event bus plus `sqlstore`
(`runtime/bus`, `storage/sqlstore`).

## Where PHP and Rust stand

Status against the required surface, read from the inventory. "Partial"
links to the gap named in the inventory's Notes column.

| Area | PHP | Rust |
|------|-----|------|
| CLI factory and global flags | partial: delegation flags only, no global-flag layer | partial: root flags, help, delegation gate |
| Output and errors | ported | ported |
| Config and paths | absent | partial: no Pkl wizard; no cache or runtime dirs |
| Serve lifecycle | ported | ported |
| MCP surface | ported | ported |
| Telemetry and consent | ported; consent reads the persisted telemetry decision only | ported |
| Redaction | partial: telemetry-only set | ported |
| Network policy | ported? | ported |
| Delegation safety | partial | ported? |
| TypeID | ported | ported |
| Compliance self-check | absent | partial: static pass only |
| Bus, `sqlstore` | absent | partial bus; `sqlstore` ported? |

## Graduation checklist

An experimental SDK graduates when every item is met and the maintainers
sign off the required-surface list. Status was read from the repository
at the time of writing; re-check before relying on it. *(proposed)*

| # | Criterion | PHP | Rust |
|---|-----------|-----|------|
| 1 | The SDK implements the required surface above | not met: config and paths absent; CLI and delegation partial | not met: CLI, config and paths partial |
| 2 | Every shared contract gate and cross-language harness runs the SDK in CI with no skips | not met: the gates run only when `php` and `composer` are on PATH; the CLI-root corpus replays only the delegation and inference cases; the cross-language target runs the Rust leg only | met for the gates in `make test-parity`; the cross-language target runs a strict Rust leg |
| 3 | CI tests the floor and the latest toolchain; Rust also builds each feature alone | not met: PHP 8.4 only, no 8.5 leg | met: pinned toolchain, MSRV and stable legs; per-feature `check-rs-features` |
| 4 | The published package holds runtime files plus `LICENSE`, complete metadata, and docs that render | not met: no `LICENSE` in `sdk/experimental/php/`; `composer.json` carries no authors, homepage, support or keywords; no export allowlist, so whether tests ship depends on the mirror (unverified) | met: `include` allowlist, `LICENSE`, repository, documentation and keywords set, `doc-rs` builds every feature with warnings as errors, `rs-package` guards the file list |
| 5 | The component lives under `sdk/<lang>`, the root `exclude-paths` lists it, the template pin is release-managed, the release channel is documented | partly met: component and `exclude-paths` entry exist, channel is in [`RELEASING.md`](../../RELEASING.md); template pin not checked | partly met: same; template pin not checked |
| 6 | Docs list the SDK as supported; examples run | not met: no runnable examples directory | partly met: `examples/` holds three, [`rs-sdk.md`](../adopters/reference/rs-sdk.md) is the reference; docs still call Rust prerelease |
| 7 | Local `make`, pre-push and `test-affected` cover the SDK | partly met: `make lint-php`, `make test-php` and `test-affected` cover it; the pre-push hook does not detect `sdk/experimental/php/` | met: `make lint-rs`, `make test-rs`, `test-affected` and pre-push all detect `sdk/rs/` |

"Met" is a statement about the checklist as written, not a verdict that
Rust is ready. Rust still fails item 1.

## Toolchain floors

| SDK | Floor | Tested in CI | Bump policy |
|-----|-------|--------------|-------------|
| PHP | 8.4, set by `composer.json` (`^8.4`) | 8.4 | *(proposed)* Raise the floor only with a minor release; add the newest stable PHP as a second CI leg before graduation |
| Rust | MSRV 1.89, set by `rust-version` in `Cargo.toml` | MSRV, stable, and the pinned dev toolchain | Documented in [`rs-sdk.md`](../adopters/reference/rs-sdk.md#rust-versions): the MSRV is what the crate and its locked dependencies need, raising it is a breaking change shipped in a minor release |

Python's floor is 3.11 and TypeScript's is Node 22.12; both are declared in
their package manifests.

## Open decisions

These need a maintainer decision; this page proposes an answer for each.

| Question | Proposed answer |
|----------|-----------------|
| Is the 12-factor compliance self-check required? | Yes for a supported SDK: adopters run it in CI, and the compliance harness already covers Go, TypeScript and Python. PHP has no module; Rust has the static pass only |
| Are `bus` and `sqlstore` required? | `bus`: not required for PHP, whose adopters do not need an in-process bus. Required for Rust, where it already ships. `sqlstore`: not required |
| Are `ai/llm`, `console/wizard` and `console/tui` required? | No, out of scope for the required surface |
| PHP floor and test matrix | Floor 8.4; test the floor and the newest stable (8.4 and 8.5) |
| Rust MSRV bump policy | Keep the current policy: raise only when a dependency requires it, ship in a minor |
| Does an experimental SDK stay out of the shared release train? | No change: it stays a linked-family component on its own prerelease line |

## Keeping this page true

- Moving an SDK between tiers, or changing the required surface, is a
  change to this page in the same branch.
- Status cells in the checklist cite what the repository shows. When a
  gate, workflow or package file changes, update the cell.
