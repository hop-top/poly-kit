# spaced-noncompliant

> **Fixture binary.** Deliberately non-compliant. Do not copy this as a
> starting point. The canonical adopter example lives at
> [`../spaced/`](../spaced/).

This is a minimal kit-powered binary used by the `kit compliance` test
suite to validate F13 (`ConsentingTelemetry`) flag-accuracy. Its
`spaced-noncompliant.toolspec.yaml` is intentionally malformed in
**exactly one way**: `telemetry.kill_switch_envs` is missing
`DO_NOT_TRACK`.

Every other F13 sub-condition is satisfied:

- (a) telemetry block well-formed — pass
- (b) consent subcommands declared + mapped — pass
- (c) `DO_NOT_TRACK=1` declared — **FAIL** (intentional)
- (d) `<APP>_TELEMETRY_MODE=off` declared — pass
- (e) first-run prompt fires-or-skips per precedence — pass
- (f) decision persisted with `prompt_version` field — pass
- (g) `inspect` returns post-redact payload + audit topic — pass

## How the test suite uses this fixture

A compliance test against this fixture must assert:

- F1–F12 pass (the existing twelve factors still work).
- F13 fails with `Details` text mentioning `DO_NOT_TRACK`.
- F13 does NOT fail for any other sub-condition. The Suggestion is
  surgical — it names the missing env, not "kill switch is wrong".

The matching test lives in `hops/main/go/core/compliance/` and is
authored under a sibling task in the
[`kit-telemetry-compliance`](../../../.tlc/tracks/kit-telemetry-compliance/plan.md)
track (T-0704 / T-0706 / follow-up). This fixture pre-lands as part of
T-0705 so those tests have a stable target to assert against.

## Layout

| Path | Purpose |
|------|---------|
| `main.go` | Minimal cobra wiring; reuses spaced's command tree so the binary actually runs |
| `spaced-noncompliant.toolspec.yaml` | Deliberately-broken toolspec; documents the violation inline |
| `README.md` | This file |

## Building

From the kit repo root:

```
cd hops/main
go build -buildvcs=false ./examples/spaced-noncompliant/...
```

The binary produced is non-functional beyond `--help` and the consent
subcommand surface — it does NOT emit telemetry. Its only job is to be
walked by `kit compliance check` paired with the broken toolspec.

## Running compliance against the fixture

```
kit compliance check ./spaced-noncompliant \
  --toolspec ./examples/spaced-noncompliant/spaced-noncompliant.toolspec.yaml
```

Expected output (shape):

```
✓ Twelve Factors  12/12
✗ Consenting Telemetry
  Suggestion: kill_switch_envs is missing DO_NOT_TRACK.
              Add DO_NOT_TRACK to kill_switch_envs in
              spaced-noncompliant.toolspec.yaml.
              See: docs/adopters/reference/telemetry-compliance.md §2(c).

Report: 12/13
```

## See also

- [`docs/adopters/reference/telemetry-compliance.md`](../../docs/adopters/reference/telemetry-compliance.md) —
  the adopter checklist this fixture validates against.
- [`docs/adr/0037-consenting-telemetry-factor.md`](../../docs/adr/0037-consenting-telemetry-factor.md) —
  why F13 exists and how the seven sub-conditions are scoped.
- [`../spaced/`](../spaced/) — the canonical (compliant) adopter example.
