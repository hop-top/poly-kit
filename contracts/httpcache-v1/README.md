# httpcache-v1

## What it answers

What `kit/storage/httpcache` ports must agree on so they can share one `kv` backend: which requests and responses are cacheable, how the cache key is derived, and the exact on-store envelope. Wrong place for the key/value binding underneath; that is `../kv-v1/`.

## Use it when

- you implement httpcache in a new SDK: replay all three files before writing a store
- you change keying, normalization or the envelope in Go: update the fixture in the same commit, since the other ports replay it

## Quick start

```sh
go test ./go/storage/httpcache/ -run '^TestContract_' -count=1
cd sdk/experimental/rs && cargo test --features httpcache --test httpcache_contract --locked
```

## Contract

| File | Pins |
|------|------|
| `cacheability.json` | `request_cacheable` (9) and `response_cacheable` (8) gate vectors |
| `keying.json` | key = `default_prefix` + lowercase-hex sha256(`method + " " + url`); `derivation`, `normalization` rules, 22 `cases`, 2 `prefix_cases` |
| `entry.json` | the three-field envelope (`status`, `headers`, `body`) with `schema`, `porting_notes`, `framing_headers`, 11 `encode_cases`, 9 `decode_cases` |

Method is used verbatim (no case folding); the URL is re-serialized by the language's URL type. Vary-aware keying is a v1 non-goal: the key depends on method and URL only. Ports MUST read and write byte-identical envelopes.

Consumers:

| Port | Loader |
|------|--------|
| Go | `go/storage/httpcache/contract_test.go` (`TestContract_Keying`, `TestContract_Entry`), `httpcache_test.go` (`TestContract_Cacheability`) |
| Rust | `sdk/experimental/rs/tests/httpcache_contract.rs` (feature `httpcache`) |

The fixtures are edited by hand and are the contract of record for every port, Go included. `make lint-config` checks they parse.

## Neighbours

- `../kv-v1/`: storage binding for the backend the envelope is written to
- `go/storage/httpcache/`: Go implementation, `policy.go` mirrors `cacheability.json`

## See also

- [`go/storage/httpcache/README.md`](../../go/storage/httpcache/README.md)
- [`sdk/experimental/rs/README.md`](../../sdk/experimental/rs/README.md)
