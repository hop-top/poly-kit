# ADR-0037 — ConsentingTelemetry as Factor #13 of kit compliance

- **Status**: Proposed
- **Date**: 2026-05-19
- **Track**: `kit-telemetry-compliance`
- **Related**: [ADR-0035](0035-runtime-telemetry.md) (kit-telemetry —
  canonical interfaces / paths / schema), ADR-0036 (kit-consent —
  consent precedence, persisted decision shape)

## Context

`hops/main/go/core/compliance/` ships a 12-factor adopter contract
that any kit-powered binary is expected to satisfy. Telemetry is a
new capability landing across three sibling tracks (`kit-telemetry`
core emitter, `kit-consent` first-run prompt + subcommands,
`cmdsurf-telemetry` cobra bridge). Adopter binaries that ship
telemetry need an enforceable contract: declare a toolspec block,
honor kill-switch envs, expose consent subcommands, persist a
decision with the right field shape, redact-on-write.

Two options surfaced:

1. **Fold telemetry checks into `ObservableOps` (Factor #9) as
   sub-checks.**
2. **Add a new Factor #13 `ConsentingTelemetry`.**

## Decision

Add a new Factor #13 `ConsentingTelemetry`. Not a sub-check under an
existing factor.

## Rationale

- **`ObservableOps` is operator-facing.** It checks that the binary
  emits structured logs/events that an operator can scrape.
  `ConsentingTelemetry` is a user-facing trust contract: opt-in,
  redact-on-write, env kill-switches, user-controlled persisted
  decision. Different actor, different trust model. Folding them
  conflates "loggable" with "consenting" and lets a tool pass
  `ObservableOps` while silently failing the consent contract.
- **Sub-check signal is too soft.** A warn line nested under another
  factor invites adopters to ignore it. Half-broken consent
  (e.g. `DO_NOT_TRACK` honored but `inspect` returns raw PII) would
  pass the parent factor with a single yellow line. A 13th factor
  forces a yes/no on the row, with seven distinct sub-conditions
  each carrying its own `Suggestion`.
- **Skip semantics keep non-adopters whole.** Binaries that don't
  ship telemetry get `result=skip` on F13. `Report.Total` derives
  from `len(results)` post-merge (not hardcoded `12`); non-opt-in
  binaries score `12/12` because the skip is excluded from both
  numerator and denominator (or equivalently, counted as pass on
  both — math is identical: 12/12).

### `Report.Total` derivation

`Report.Total` is derived from `len(results)`, so non-opt-in
binaries score `len(non-skip)/len(non-skip)` = **12/12** and the
existing test suite continues to pass without modification.

Opt-in binaries with full compliance score **13/13**. Opt-in
binaries with one sub-condition broken score **12/13** with the
failure line surfacing the actionable suggestion.

## Toolspec `telemetry:` block schema (canonical reference)

```yaml
telemetry:
  enabled: true
  categories: [invocation, error]
  sinks: [bus, jsonl]
  consent_command: "<bin> telemetry"
  consent_subcommands: [status, enable, disable, reset, inspect]
  kill_switch_envs: [DO_NOT_TRACK, "<APP>_TELEMETRY_MODE"]
  prompt_version: "v1"
  redact_rules: kit-default
```

Field-level notes:

- `enabled: false` → F13 returns `skip("binary does not opt into
  telemetry")`. Lets a binary ship the block partially filled
  without failing compliance.
- `kill_switch_envs` must include `DO_NOT_TRACK` AND at least one
  mode env. Accepted shapes: app-prefixed (e.g.
  `SPACED_TELEMETRY_MODE`) OR the kit-built canonical
  `KIT_TELEMETRY_MODE`.
- `prompt_version` is the load-bearing field name. The persisted
  config (owned by `kit-consent`, locked by ADR-0035 / ADR-0036)
  uses the same name. Specs that use `consent_version` or any other
  alias are rejected by the static check.
- `redact_rules: kit-default` references the canonical ruleset
  shipped by `kit-telemetry`. Custom paths are allowed; runtime
  check verifies the ruleset fired.

## Sub-conditions checked

Seven sub-conditions, split across the static and runtime arms:

| # | Sub-condition | Static | Runtime |
|---|---------------|--------|---------|
| a | `telemetry:` block present with non-empty `categories` | yes | — |
| b | `consent_subcommands` lists `{status, enable, disable, reset, inspect}` AND each maps to a command in the tree | yes | invoke each: exit 0 + structured output |
| c | `DO_NOT_TRACK=1` suppresses emission | declared in `kill_switch_envs` | set env, emit, assert zero events within 500 ms |
| d | `<APP>_TELEMETRY_MODE=off` (or `KIT_TELEMETRY_MODE=off`) suppresses emission | declared in `kill_switch_envs` (shape match) | same shape as (c); runtime toggles whichever the spec declares |
| e | First-run prompt fires-or-skips per precedence | — | three scenarios in fresh `rtEnv` |
| f | Decision persisted with `prompt_version` field | `prompt_version` set in spec (field-name locked) | inspect post-decision config under temp `XDG_CONFIG_HOME` |
| g | `inspect` returns POST-REDACT payload | `redact_rules` set in spec | inject PII-laden event, assert raw PII absent + redact placeholders present + subscribe to `kit.telemetry.redact.matched` audit topic and assert matches fired (load-bearing) |

The audit-topic subscription in (g) is the load-bearing assertion
that the redactor actually ran; a no-op redactor would pass
"raw PII absent" vacuously.

`install_id` (referenced by some runtime checks): read from
`<XDG_STATE_HOME>/kit/telemetry/installation_id` per ADR-0035 —
32 raw bytes on disk, SHA-256 hex on the wire.

## Consequences

### Positive

- Adopters get a single, named row that tells them whether their
  telemetry implementation is consent-correct.
- Each sub-condition carries its own actionable `Suggestion`.
- Non-opt-in binaries are unaffected (`Report.Total` derivation
  means existing tests stay green).
- Compliance becomes the enforcement layer for the consent contract
  defined by `kit-consent` (ADR-0036) and the wire contract defined
  by `kit-telemetry` (ADR-0035).

### Negative

- One more factor for adopters to read. Mitigated by the
  intent-driven adopter doc (`hops/main/docs/telemetry-compliance.md`)
  that walks the 90% path inline.
- Runtime check cost: each opt-in binary takes ~5–8 s for the full
  F13 sweep (3 kill-switch scenarios + 5 subcommand invocations + 3
  prompt scenarios, each in a fresh `rtEnv`). Acceptable for
  `kit compliance`; the existing `RunStatic` vs `Run` split lets
  adopters skip runtime checks in fast paths.

### Neutral

- `format.go` column width bumps from `%-20s` to `%-22s` to fit
  "Consenting Telemetry" (20 chars vs prior longest "State
  Transparency" at 18 chars).

## Alternatives considered

- **Sub-check under `ObservableOps`.** Rejected: silently lowers the
  bar; conflates operator scraping with user consent.
- **Keep `Report.Total=12` and hide F13 entirely when not opted in.**
  Rejected: identical to deriving Total from `len(results)` for the
  non-opt-in case, but loses the explicit `skip` row that signals
  "we know about this factor, you just don't ship telemetry."

## Cross-references

- **ADR-0035** — kit-telemetry: canonical emitter interfaces, env
  names, install_id path, bus topics including
  `kit.telemetry.redact.matched`, jsonl sink contract.
- **ADR-0036** — kit-consent: precedence rules, persisted decision
  shape (`telemetry.consent.{state, decided_at, prompt_version,
  decision_source}`), `prompt_version` ownership.
- **Plan**: `.tlc/tracks/kit-telemetry-compliance/plan.md` — full
  task breakdown (T-0696..T-0706, T-0739).
- **Adopter doc**: `hops/main/docs/telemetry-compliance.md`
  (authored by T-0705).
