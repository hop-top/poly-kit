# output

## What it answers

How does a command render one payload as table, json or yaml under the standard `--format` flag family,
with the same column order in every port? Terminal styling belongs to `hop_top_kit::tui`; mounting flags
on a clap root is `hop_top_kit::cli`.

## Use it when

- a command has a `serde_json::Value` payload and a clap `ArgMatches` → `register_output_flags` once on the
  root, `dispatch` in the handler
- the payload is a list of rows and column order matters → pass a `ColumnSpec` slice on
  `DispatchOptions::columns`
- a command fails and the failure must carry a cross-tool exit code → build a `CliError` (`usage`,
  `not_found`, `transient`, ...) and `render_error`
- a custom format is needed → implement `Formatter`, `Registry::register`; see [builtins](builtins/README.md)

## Quick start

```rust
use hop_top_kit::output::{dispatch, register_output_flags, DispatchOptions, RegisterOutputFlagsOptions};
use serde_json::json;

let (cmd, _ctx) = register_output_flags(
    clap::Command::new("demo").no_binary_name(true),
    RegisterOutputFlagsOptions::default(),
);
let matches = cmd.try_get_matches_from(["--format", "json"]).unwrap();
let mut buf = Vec::new();
dispatch(&matches, &mut buf, &json!([{"a": 1}]), DispatchOptions::default()).unwrap();
assert_eq!(serde_json::from_slice::<serde_json::Value>(&buf).unwrap(), json!([{"a": 1}]));
```

## Contract

- Feature `output` pulls in `serde`, `serde_json` (`preserve_order`), `serde_yaml`, `thiserror`,
  `comfy-table`; `register_output_flags` and `dispatch` need feature `cli` (clap). Authority: the crate
  [feature table](../../README.md#features).
- Resolution order: `--output` extension infers the format, an explicit `--format` that conflicts with it
  is an error, default is `table`.
- `--template` and `--cols` are mutually exclusive; `--cols` is validated against the `ColumnSpec` slice.
- `ColumnSpec::new` panics when `header != key`; `priority` is stored but hide-on-overflow is Go only.
- Column order: `ColumnSpec` list order, then `--cols` order; payload key order only when no spec is given.
  json and yaml key order follows the same resolution.
- `CliError` codes and exit codes are the cross-tool constants (`CODE_*`, `EXIT_*`); transience is derived
  per code by `transience_for_code`.
- Parity: [ordering.json](../../../../tests/cross-lang/fixtures/ordering.json), replayed by
  `sdk/tests/cross-lang/runners/rs`; the fixtures compare re-parsed column order, never bytes.

## Neighbours

- `hop_top_kit::output::builtins`: the five shipped formatters and their `--format-opt` keys
- `hop_top_kit::cli` (src/cli.rs): the clap root the flag suite is registered on
- `hop_top_kit::tui`: status symbols and spinner, no payload rendering
- `hop_top_kit::serve`: maps `LifecycleOutcome` onto the `CliError` codes defined here

## See also

- [CLI parity guide](../../../../../docs/adopters/guides/cli-parity-guide.md)
- [Crate README, Output formatting](../../../../../docs/adopters/reference/rs-sdk.md#output-formatting)
- Go reference: [go/console/output](../../../../../go/console/output/README.md)
