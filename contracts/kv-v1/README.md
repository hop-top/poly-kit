# kv-v1

## What it answers

How a key/value pair written by one SDK's `kv` SQLite backend must bind so that any other SDK reads it back byte-for-byte from the same file. Wrong place for httpcache envelopes (`../httpcache-v1/`) or key derivation (`../identity-v1/`).

## Use it when

- you add or change a `kv` SQLite backend in any SDK: run the corpus in both directions
- you touch key binding, ordering or prefix scans: add a case here first, then make every port pass it

## Quick start

```sh
make test-parity-kv
```

Runs `TestCrossLang*` in `go/storage/kv/sqlite` with `KV_CROSSLANG=1`; the Go test drives the Rust half (`sdk/experimental/rs/tests/kv_crosslang.rs`, feature `kv`) as a `cargo test` subprocess, so both toolchains are required.

## Contract

`keys.json` pins:

- `cases`: 16 key/value pairs in hex that every port writes and every other port reads back with the exact value bytes
- `list_order`: the expected `ORDER BY key` sequence for the corpus
- `prefix_scans`: 6 prefix queries with the keys they must return

The `kv.key` column is TEXT in every implementation. SQLite compares storage class before value, so a BLOB-bound key silently misses a TEXT-bound one; a suite that round-trips within one language cannot catch this, which is why each SDK writes and another reads.

The fixture is edited by hand. `make lint-config` checks it parses; `make test-parity-kv` checks every port still agrees.

## Neighbours

- `go/storage/kv/sqlite/crosslang_test.go`: Go loader and the cross-process driver
- `sdk/experimental/rs/tests/kv_crosslang.rs`: Rust loader
- `../httpcache-v1/`: on-store envelope layered on top of `kv`

## See also

- [`go/storage/kv/README.md`](../../go/storage/kv/README.md)
- [`sdk/experimental/rs/README.md`](../../sdk/experimental/rs/README.md)
