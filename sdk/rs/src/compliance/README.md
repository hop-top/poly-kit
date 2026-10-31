# compliance

## What it answers

Does a tool's toolspec meet the 12-factor AI CLI spec (plus Factor 13, Consenting Telemetry, for a tool that
opts into telemetry)? The static pass of Go `go/core/compliance`: load the toolspec YAML, grade each factor,
score and render the report.

## Use it when

- a toolspec file must be graded in CI or by a `doctor` command → `run_static(path)`, then `format_report(&r, "text" | "json")`
- the spec is already in memory, or built by code → `grade(&spec, label)`
- another tool needs the toolspec's types → `load_spec` / `spec::ToolSpec`

## Quick start

```rust
use hop_top_kit::compliance::{format_report, run_static, Factor, Status};

let report = run_static(std::path::Path::new("toolspec.yaml")).unwrap();
let evolution = report.results.iter().find(|r| r.factor == Factor::Evolution).unwrap();
if evolution.status == Status::Fail {
    eprintln!("{}", format_report(&report, "text"));
}
```

## Contract

- Feature `compliance` rides on `output` (serde, serde_json, serde_norway, thiserror). Authority: the crate [feature table](../../README.md#features).
- The report has 13 rows in factor order. `score` counts passes; `total` is 12, or 13 when `telemetry.enabled: true`. A spec that does not opt in still gets the factor 13 row, as a skip outside the total.
- Factors 3, 9 and 10 are runtime-only and always skip here; factor 12 skips without `state_introspection.auth_commands`. The runtime pass, which executes the binary, is not ported (Go, TypeScript and Python only).
- Factor 13 aggregates every broken sub-condition into one row joined by `; `: categories, the five canonical consent subcommands, `DO_NOT_TRACK`, an `<APP>_TELEMETRY_MODE` env, `prompt_version` (no aliases), `redact_rules`, and every consent subcommand present in the command tree under the consent command's tail (`telemetry` when it has one token).
- `schema_version` reads any YAML scalar as the string it spells (`2` and `"2"` agree). A float such as `1.0` reads as `1`.
- JSON matches Go's shape: `factor` is the number, empty `details`/`suggestion` are left off.
- Parity: the verdict (score, total, per-factor status) is pinned by `sdk/tests/cross-lang/run-compliance.sh rs` against `expected/compliance.json`.

## Neighbours

- `hop_top_kit::output`: renders the structured error a failing command returns
- `sdk/tests/cross-lang/runners/rs/src/compliance.rs`: the harness runner

## See also

- [`go/core/compliance`](../../../../go/core/compliance/README.md), the Go reference
- [Cross-language harnesses](../../../tests/cross-lang/README.md#compliance-conformance-harness)
