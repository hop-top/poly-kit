# parity

## What it answers

Which status symbols, spinner frames, animation timings, help sections, verbosity levels and stream rules does every kit CLI share across languages? Typed loader for `contracts/parity/parity.json`. Wrong place for rendering them: that is `hop_top_kit::cli` and `hop_top_kit::output`.

## Use it when

- a CLI needs the cross-language TUI constants → `parity::values()`, a `&'static Parity` parsed once
- a test must see the raw contract → `parity::RAW`
- a new block lands in the contract → add it to `parity::BLOCKS` and a field on `Parity`

## Contract

- `src/parity/parity.json` is a byte-identical copy of `contracts/parity/parity.json`: a crate cannot `include_str!` outside its own tree. `tests/parity.rs` fails on drift and on a contract block this loader does not declare.
- A parse failure of the embedded data is a build defect and panics, as Go's `init` does.
- Port of `contracts/parity/parity.go`; parity row and corpus: [contracts/parity](../../../../contracts/parity/README.md).

## Neighbours

- `hop_top_kit::cli`: the kit root that consumes the help and verbosity blocks
- `hop_top_kit::output`: formatters and `CliError`
