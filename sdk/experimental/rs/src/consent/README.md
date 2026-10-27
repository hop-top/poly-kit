# consent

## What it answers

May this subject use this capability now, and what did the user decide? Per-capability consent
grants with audit, revoke and confirmed delivery to the bus; telemetry consent is one capability.
Port of Go `go/core/consent`. No OS permission prompt and no capture here: the caller owns those.

## Use it when

- a tool or shell reads the screen, camera, microphone, clipboard or files for the user →
  `consent::open(Some("tool"))?.build()`, then `request` before asking, `use_grant` before every tap
- the caller has its own consent UI → pass a `Prompter`; without one, a request is refused
- consent events must reach other components → `deliver_to(bus)` and run `grants.relay()`
- tests or a host with no filesystem → `Grants::builder(Arc::new(MemoryGrantStore::new()))`

## Quick start

```rust
use hop_top_kit::consent::{open, Answer, Duration, Effect, Prompt};

let g = open(Some("dek"))?.build();
let ask = |_: &Prompt| -> Result<Answer, String> {
    Ok(Answer { decision: Effect::Allow, duration: Duration::Session, expires_at: None })
};
g.request("io.screen.capture", "app:dek", Some(&ask))?;
g.use_grant("io.screen.capture", "app:dek")?; // audited before the tap
```

## Contract

- Feature `consent`: model, decisions, prompter seam, audit records, memory store; wasm32-safe,
  no clock read (set `GrantsBuilder::clock` there). Feature `consent-file`: `FileStore`,
  `FileAuditor`, `Relay`, `open`; native only.
- Grant: capability (`telemetry`, or 2+ `^[a-z][a-z0-9_]*$` segments, <= 128 bytes) × subject
  (empty = any) × allow/deny × once/session/until/always. Lookup: subject, then any-subject;
  every record of one key deny-wins, so a session grant never overrides a stored deny.
- Fail closed: no prompter, a prompter error, an invalid answer, an unreadable store or an audit
  failure on use all mean no. `denied` is `UNAUTHORIZED`/5/permanent; `unset`, `expired`,
  `declined` are `CONSENT_REFUSED`/7/transient (`RefusedError::to_cli_error` with `output`).
- Files: `<XDG_STATE_HOME>/<tool>/consent/{grants.yaml,audit.jsonl,audit.cursor}`, 0700 dir,
  0600 files, grants and cursor written temp + rename. Decoding is strict: one bad record makes
  the file `malformed_store`.
- Telemetry: grants file, `kit.telemetry.consent` (`kit/config.yaml`) and `kit/telemetry.yaml`
  combined deny-wins; writes mirrored into the legacy records (what `telemetry::load_consent`
  reads), revoke clears all three; `DO_NOT_TRACK` and `KIT_TELEMETRY_MODE=off` are a standing
  deny. Edits keep every other key and the comments, as yaml.v3 does.
- Events `kit.core.consent.{granted,denied,revoked,used,refused}`, source `core.consent`, id
  `<log>:<seq>`; the cursor moves only after every sync subscriber returned `Ok`; redelivery
  after an error or restart. `refused` is recorded once per (capability, subject, reason) per
  session.
- Parity: `contracts/consent-v1` (`make test-parity-consent`).

## Neighbours

- `hop_top_kit::bus`: the `Publisher` the relay and `BusAuditor` publish through
- `hop_top_kit::telemetry`: the read-only consent loader the legacy mirror keeps current
- `hop_top_kit::xdg` (src/xdg/): state and config homes

## See also

- [Consent grants reference](../../../../../docs/adopters/reference/consent-grants.md)
- [`go/core/consent`](../../../../../go/core/consent/README.md), the Go reference
