# config/pkl

## What it answers

How a Rust tool reads a PKL config schema for validation and completion, and resolves computed
fields. Port of the pure half of Go `go/core/config/pkl`.

## Use it when

- validate one `key=value` before writing → `validate_value(&load_schema(path)?, key, value)`
- complete keys and values → `completion_keys(&schema)`, `completion_values(&schema, key)`
- fill computed fields from answers → `resolve(path, &answers)` (needs the `pkl` binary)

## Quick start

```rust
use hop_top_kit::config::pkl::{parse_source, validate_value};

let s = parse_source("port: Int(isBetween(1024, 65535))\n");
assert!(validate_value(&s, "port", "80").is_err());
```

## Contract

- Feature `config-pkl`. Parsing is Go's line-and-regex reading of the source, quirks included.
- Schema errors are `config::SchemaError` with Go's texts.
- Not ported: `RunWizard`, `WizardSteps`, `NewConfigCommand` (they need a console wizard and a
  CLI root, which kit-rs does not have yet).

## Neighbours

- `config` (../): `SchemaError`, `Value`

## See also

- [`go/core/config/pkl`](../../../../../../go/core/config/pkl/README.md), the Go reference
