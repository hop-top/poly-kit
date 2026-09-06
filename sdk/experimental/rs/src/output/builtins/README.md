# builtins

## What it answers

Which formatters ship by default, what `--format-opt` keys each accepts, and how a custom formatter
registers beside them? Flag parsing and dispatch live in the parent `hop_top_kit::output`.

## Use it when

- a fresh `Registry` needs the standard five → `register_all(&registry)`; `default_registry()` already has them
- a format needs tuning from the command line → `--format-opt key=value`, keys per the table below
- the shipped `table` is too plain (no borders, no colour, comfy-table `NOTHING` preset) → register your own
  `Formatter` under the `table` key with `Registry::override_with`

| Key | Extensions | Options (default) |
|-----|------------|-------------------|
| `table` | none | `header` bool (true) |
| `json` | `.json` | `indent` int (2, 0 = compact) |
| `yaml` | `.yaml`, `.yml` | none |
| `csv` | `.csv` | `delimiter` string (`,`), `no-header` bool, `quote-all` bool, `crlf` bool |
| `text` | `.txt` | `style` enum kv, lines, paragraph (kv), `separator` string (`=`, kv only) |

## Quick start

```rust
use std::sync::Arc;
use hop_top_kit::output::builtins::{register_all, JsonFormatter};
use hop_top_kit::output::{Options, Registry};
use serde_json::json;

let registry = Registry::new();
register_all(&registry);
assert_eq!(registry.keys(), ["csv", "json", "table", "text", "yaml"]);
assert!(registry.register(Arc::new(JsonFormatter)).is_err());

let mut buf = Vec::new();
let json = registry.lookup("json").unwrap();
json.render(&mut buf, &json!({"name": "alpha"}), &Options::default(), &[]).unwrap();
assert_eq!(String::from_utf8(buf).unwrap(), "{\n  \"name\": \"alpha\"\n}\n");
```

## Contract

- Same feature as the parent, `output`; `comfy-table` is here for `table` only. Authority: the crate
  [feature table](../../../README.md#features).
- `Registry::register` refuses a duplicate key; replacing a built-in is `override_with`, on purpose.
- `table`, `csv` and `text` share one row helper: a single object is a one-row payload, columns come from
  the resolved list or, without one, the first row's key order; zero rows emit nothing.
- `csv` quoting is byte-for-byte Go's `encoding/csv` on output; `crlf` changes only the record terminator.
- Option `default` fields are `None` in the static specs and merged at render time.
- Parity: [ordering.json](../../../../../tests/cross-lang/fixtures/ordering.json), replayed by
  `sdk/tests/cross-lang/runners/rs`.

## Neighbours

- `hop_top_kit::output` (../README.md): `Formatter`, `OptionSpec`, `Registry`, flags and `dispatch`
- `hop_top_kit::tui`: styled terminal output that is not a payload format

## See also

- [Crate README, Output formatting](../../../../../../docs/adopters/reference/rs-sdk.md#output-formatting)
- Go reference: [go/console/output](../../../../../../go/console/output/README.md)
