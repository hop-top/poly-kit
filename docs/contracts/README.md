# contracts

Wire-level and behavioural contracts a kit-based tool must honour, one page per contract, each pointing at the Go source that is authoritative.

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`event-topics.md`](event-topics.md) | 4-segment past-tense bus topic convention, configurable per emitter | you publish or subscribe on the bus and need the topic shape |
| [`ext-discover-protocol.md`](ext-discover-protocol.md) | PATH-based external plugin contract for kit-based tools | you ship a sidecar binary a kit tool should discover |
| [`kit-init-pr-wiring.md`](kit-init-pr-wiring.md) | `.github` wiring and PR hooks that `kit init` generates | you change what `kit init` writes under `.github` or the before/after-PR hooks |
| [`serve-lifecycle.md`](serve-lifecycle.md) | serve hierarchy and service lifecycle for `<tool> serve` | you add a service to `serve` or port the supervisor to another SDK |

## Conventions

- A page opens with a blockquote naming the authoritative implementation; the page is the cross-reference, not the source of truth.
- Machine-readable fixtures live in [`../../contracts/`](../../contracts/README.md), not here.
