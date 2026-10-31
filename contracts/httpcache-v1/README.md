# httpcache-v1

## What it answers

What `kit/storage/httpcache` ports must agree on so they can share one `kv` backend: which requests and responses are cacheable, how the cache key is derived, and the exact on-store envelope. Wrong place for the key/value binding underneath; that is `../kv-v1/`.

## Use it when

- you implement httpcache in a new SDK: replay all three files before writing a store
- you change keying, normalization or the envelope in Go: update the fixture in the same commit, since the other ports replay it

## Quick start

```sh
go test ./go/storage/httpcache/ -run '^TestContract_' -count=1
cd sdk/rs && cargo test --features httpcache --test httpcache_contract --locked
```

## Contract

| File | Pins |
|------|------|
| `cacheability.json` | `request_cacheable` (9) and `response_cacheable` (8) gate vectors, plus `vary_response_cacheable` (3) and `authorization_response_cacheable` (9) |
| `keying.json` | key = `default_prefix` + lowercase-hex sha256(`method + " " + url`); `derivation`, `normalization` rules, 22 `cases`, 2 `prefix_cases`, `vary` section (index, variant and field-list vectors) |
| `entry.json` | the three-field envelope (`status`, `headers`, `body`) with `schema`, `porting_notes`, `framing_headers`, 11 `encode_cases`, 9 `decode_cases` |

Method is used verbatim (no case folding); the URL is re-serialized by the language's URL type. The primary key depends on method and URL only; responses with `Vary` use the variant keys in `keying.json` (`vary` section), and `cacheability.json` carries the `Vary: *` and `Authorization` storage vectors. Ports MUST read and write byte-identical envelopes.

Consumers:

| Port | Loader |
|------|--------|
| Go | `go/storage/httpcache/contract_test.go` (`TestContract_Keying`, `TestContract_Entry`), `httpcache_test.go` (`TestContract_Cacheability`) |
| Rust | `sdk/rs/tests/httpcache_contract.rs` (feature `httpcache`): keying cases, the `vary` section (`contract_vary_keying`), the `vary_*` / `authorization_*` / `private_*` cacheability sections (`contract_vary_auth_private_storable`, `_end_to_end`), entry and cacheability vectors |

The fixtures are edited by hand and are the contract of record for every port, Go included. `make lint-config` checks they parse.

## Neighbours

- `../kv-v1/`: storage binding for the backend the envelope is written to
- `go/storage/httpcache/`: Go implementation, `policy.go` mirrors `cacheability.json`

## See also

- [`go/storage/httpcache/README.md`](../../go/storage/httpcache/README.md)
- [`sdk/rs/README.md`](../../sdk/rs/README.md)
