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
- Every splice is re-parsed and must hold exactly the values Go's tree edit produces, and every
  comment, anchor and alias outside the bytes it dropped must still be there. Otherwise the edit
  is refused (`ErrorKind::EditUnsupported`) and the file is not written; nothing is re-emitted
  whole. Refused today: explicit `? key` entries, a new block value after an old block scalar
  whose header carries a comment, and an `unset` that strands an alias.
- `\r\n` files keep `\r\n`; a `|+` value takes its trailing blank lines with it on unset.
- Bytes differ from Go, which re-emits the document; values, kept comments and key order match.

## Neighbours

- `yaml/` (../yaml/): parser spans and the emitter used for new values

## See also

- [config README](../README.md)
