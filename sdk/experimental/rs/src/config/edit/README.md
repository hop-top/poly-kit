# config/edit

## What it answers

How do `set`, `set_value` and `unset` change one key without losing the file's comments?
Crate-private: the Go tree edit as the oracle (`tree.rs`) and a text splicer (`splice.rs`).

## Use it when

- changing how config writes behave: start here, then run the setter tests and `config-v1`

## Quick start

```sh
cargo test --features config --lib config::edit
cargo test --features config --test config_setter
```

## Contract

- Only the bytes of the targeted entry change; comments, blank lines, anchors and layout
  elsewhere stay. A new key is appended after the mapping's last entry, at its indentation.
- Every splice is re-parsed and must hold exactly the values Go's tree edit produces; a shape
  the splicer does not handle falls back to emitting that tree whole (values right, comments lost).
- Bytes differ from Go, which re-emits the document; values, kept comments and key order match.

## Neighbours

- `yaml/` (../yaml/): parser spans and the emitter used for new values

## See also

- [config README](../README.md)
