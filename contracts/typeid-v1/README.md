# typeid-v1

## What it answers

Which `typeid` string every SDK must produce for a given `(prefix, uuid)`, and which `(prefix, uuid)` it must recover from a `typeid`. A divergence is a wire-format break. Wrong place for UUID generation itself; that is each port's `id` package.

## Use it when

- you port the TypeID primitive to a new SDK: load these vectors in your contract test before anything else
- you change encoding or prefix validation in any port: extend `vectors` here, then run all five loaders

## Quick start

```sh
make test-parity-typeid
```

## Contract

`fixtures.json` pins 6 `vectors` against Jetify TypeID spec v0.3 (`spec` field): `prefix`, `uuid`, expected `typeid`, and an optional `skip_in` list naming ports that opt out of one vector (`empty-prefix-bare-suffix` skips `rs`). Every port MUST encode and round-trip each vector it does not skip.

Loaders, one per port, all run by `make test-parity-typeid`:

| Port | Loader |
|------|--------|
| Go | `go/core/id/contract_test.go` (`TestContract*`) |
| Rust | `sdk/experimental/rs/tests/contract.rs` (feature `id`) |
| TypeScript | `sdk/ts/src/id/contract.test.ts` |
| Python | `sdk/py/tests/test_id_contract.py` |
| PHP | `sdk/experimental/php/tests/Id/ContractTest.php`, run only when `php` and `composer` are on PATH |

The fixture is edited by hand; there is no generator. `make lint-config` checks it parses.

## Neighbours

- `go/core/id/`: Go implementation and the reference behaviour
- `../parity/`: cross-language constants loaded at runtime, not test vectors

## See also

- [`docs/announcements/2026-05-typeid-primitive.md`](../../docs/announcements/2026-05-typeid-primitive.md)
- [`go/core/id/contract_test.go`](../../go/core/id/contract_test.go): reference loader
