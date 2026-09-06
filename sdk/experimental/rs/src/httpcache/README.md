# httpcache

## What it answers

How are HTTP responses cached across runs so every port computes the same key
and stores the same envelope? The TTL store underneath is `hop_top_kit::kv`;
deciding whether the network may be used at all is `hop_top_kit::netpolicy`.

## Use it when

- repeated `GET`s against the same URL should hit disk after the first run → `Cache::new(&store).fetch(&req, fetch)`
- several callers share one kv backend → `Config::default().with_prefix(..)`
- a response should be served for longer or shorter than a day → `Config::default().with_ttl(..)`
- you need the key a request maps to, for inspection or eviction → `Cache::key(&req)`

## Quick start

```rust
use hop_top_kit::httpcache::{Cache, Request, Response};
use hop_top_kit::kv::Config;

let dir = tempfile::tempdir().unwrap();
let store = Config::sqlite(dir.path().join("cache.db").to_str().unwrap())
    .open()
    .unwrap();
let cache = Cache::new(&store);

let req = Request::get("https://example.com/a").unwrap();
let mut fetches = 0;
let mut fetch = |_: &Request| {
    fetches += 1;
    Ok::<_, std::convert::Infallible>(Response::new(200, "hello"))
};

assert_eq!(cache.fetch(&req, &mut fetch).unwrap().body_string(), "hello");
assert_eq!(cache.fetch(&req, &mut fetch).unwrap().body_string(), "hello");
assert_eq!(fetches, 1, "second call served from cache");
```

## Contract

- Feature `httpcache` pulls in `kv` plus `serde`, `serde_json`, `sha2`, `base64`. Authority: the crate
  [feature table](../../README.md#features).
- The fetch is a closure over plain `Request` / `Response` data; the module brings no HTTP client
  and no async runtime. Go decorates an `http.RoundTripper` instead.
- Only `GET` requests and 2xx responses are stored; `Cache-Control: no-store` on either side opts out;
  an entry past its TTL is a miss. Defaults: prefix `httpcache:`, TTL 24h.
- Cache failures never reach the caller: a bad read decodes to a miss, a failed write is dropped.
- The method is used verbatim in the key, never case-folded.
- Parity: [`contracts/httpcache-v1/`](../../../../../contracts/httpcache-v1/) (`keying.json`,
  `entry.json`, `cacheability.json`), replayed by `tests/httpcache_contract.rs`.

## Neighbours

- `hop_top_kit::kv` (src/kv.rs): the `TtlStore` this cache writes through; key binding rules live there
- `hop_top_kit::netpolicy` (src/netpolicy.rs): the `--offline` gate that decides whether `fetch` may run
- `hop_top_kit::api` (src/api.rs): the guarded reqwest client an adopter wires into the fetch closure

## See also

- Crate README, [httpcache wire contract](../../../../../docs/adopters/reference/rs-sdk.md#httpcache-wire-contract)
- [`contracts/kv-v1/keys.json`](../../../../../contracts/kv-v1/keys.json), the key-binding contract underneath
- [`go/storage/httpcache`](../../../../../go/storage/httpcache/README.md), the Go reference
