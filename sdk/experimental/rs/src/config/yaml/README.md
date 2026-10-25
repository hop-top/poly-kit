# config/yaml

## What it answers

How does the Rust config port read and write YAML the way Go's yaml.v3 does? Crate-private:
a node tree with source spans, yaml.v3 scalar resolution, a style-faithful emitter and serde
bridges with yaml.v3 decode and encode rules.

## Use it when

- changing how config values type, merge, quote or decode: start here, then run the config tests

## Quick start

```sh
cargo test --features config --lib config::yaml
```

## Contract

- Parsing uses `saphyr-parser` events; tags, styles and resolution are this module's, mirroring
  yaml.v3 (`resolve.rs` ports `resolve.go`, `emit.rs` libyaml's style choice).
- Implicit empty keys (`: v`) are rejected, as libyaml does.
- Decode errors use yaml.v3's text; type names in them are Rust's (`int` for `i64`, `[]` for lists).
- The emitter matches yaml.v3 on how values read back, not byte for byte (no long-line folding,
  folded scalars become literal blocks, aliases are expanded).

## Neighbours

- `config` (../): every public entry point builds on this module

## See also

- [config README](../README.md)
