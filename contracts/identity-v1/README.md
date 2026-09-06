# identity-v1

## What it answers

The exact bytes `DeriveKey` must produce from an Ed25519 seed and a domain-separation string, so an encrypted store written by one language stays readable by another. Wrong place for encryption vectors: encryption uses a random nonce and is covered by round-trip tests, not fixed vectors.

## Use it when

- you port `kit/core/identity` or an encrypted store to a new SDK: assert these vectors before encrypting anything
- you change the KDF parameters: this file changes in the same commit, or every other port's stores become unreadable

## Quick start

```sh
go test ./go/core/identity/ -run '^TestDeriveKeyContract$' -count=1
cd sdk/experimental/rs && cargo test --features sqlstore --test sqlstore --locked
```

## Contract

`derive-key.json` pins:

- `algorithm`: HKDF-SHA256, `ikm` = the 32-byte Ed25519 seed, empty `salt`, `info` = the domain string, `output_len` 32
- `derive_key`: 5 vectors with `seed`, `public_key`, `info` and the expected output; the public key lets a port confirm keypair construction before checking derivation

Consumers:

| Port | Loader |
|------|--------|
| Go | `go/core/identity/derivekey_contract_test.go` (`TestDeriveKeyContract`) |
| Rust | `sdk/experimental/rs/tests/sqlstore.rs` (feature `sqlstore`), implementation in `src/sqlstore/crypto.rs` |

The fixture is edited by hand; `make lint-config` checks it parses.

## Neighbours

- `go/core/identity/`: Go implementation
- `../kv-v1/`, `../httpcache-v1/`: what the derived key ends up protecting

## See also

- [`go/core/identity/README.md`](../../go/core/identity/README.md)
- [`sdk/experimental/rs/README.md`](../../sdk/experimental/rs/README.md)
