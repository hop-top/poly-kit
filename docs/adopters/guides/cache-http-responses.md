# Cache HTTP responses with httpcache

> Audience: Go tool authors whose CLI or service repeats idempotent GETs
> (API metadata, feeds, registry lookups) and wants a durable TTL cache
> without writing one. Package README:
> [`go/storage/httpcache`](../../../go/storage/httpcache/README.md).

`httpcache.New` returns an `http.RoundTripper` that serves cacheable GETs
from a `kv.TTLStore` and falls through to an inner transport on a miss.
Drop it into `http.Client.Transport`; call sites do not change.

## Minimal setup

```go
import (
    "net/http"
    "time"

    "hop.top/kit/go/storage/httpcache"
    "hop.top/kit/go/storage/kv/memory"
)

store := memory.New()
defer store.Close() // httpcache never closes the store

client := &http.Client{
    Transport: httpcache.New(store, nil, httpcache.WithTTL(time.Hour)),
}
resp, err := client.Get("https://example.com/data.json")
```

- `store` is any `kv.TTLStore` (needs `PutWithTTL`). `memory`, `sqlite`
  and `badger` backends implement it.
- `inner == nil` means `http.DefaultTransport`.
- You own the store lifecycle: open and close it yourself.

## Durable cache on SQLite

`kv.Open` returns `kv.Store`; assert to `kv.TTLStore`.

```go
import (
    "hop.top/kit/go/storage/kv"
    _ "hop.top/kit/go/storage/kv/sqlite" // registers "sqlite"
)

store, err := kv.Open(kv.Config{Backend: "sqlite", Path: dbPath})
if err != nil { return err }
defer store.Close()

ttl, ok := store.(kv.TTLStore)
if !ok { return errors.New("backend has no TTL support") }
rt := httpcache.New(ttl, http.DefaultTransport)
```

## Compose with the breaker

Put the cache outside the breaker: a hit costs no network call, so it
must not consume a breaker token or trip a rate limit. Only misses reach
the breaker and the wire.

```go
b := breaker.New("fetch")
rt := httpcache.New(ttl,
    breaker.WrapHTTP(b, http.DefaultTransport),
    httpcache.WithTTL(24*time.Hour),
)
```

Order, outermost first: `httpcache`, `breaker`, `http.DefaultTransport`.
Breaker details: [breaker reference](../reference/breaker.md).

## Options

| Option | Default | Effect |
|--------|---------|--------|
| `WithTTL(d)` | 24h | freshness window; `d <= 0` stores with no expiry |
| `WithPrefix(p)` | `httpcache:` | key namespace in a shared kv backend; empty restores default |

TTL is authoritative: `Cache-Control: max-age` is not read.

## What gets cached

| Case | Behavior |
|------|----------|
| non-GET request | passes through, never cached |
| request or response `Cache-Control: no-store` | not cached (token match, case-insensitive) |
| non-2xx response | returned, not stored |
| entry past TTL | miss; refetched and overwritten |
| store read, decode or write error | degrades to a miss; caller never sees a cache error |
| inner transport error | returned as-is |

Key: prefix + `sha256(method + " " + url)`. Cached responses are
rebuilt as `HTTP/1.1` with the stored status, headers (framing headers
`Connection`, `Content-Length`, `Transfer-Encoding` dropped) and body;
`ContentLength` comes from the stored bytes.

## Limits to design around

- No `Vary` keying: request headers other than the URL do not change the
  key. Do not cache per-user or `Authorization`-dependent responses
  through a shared transport and store; use a separate `WithPrefix`
  and store per identity, or send `Cache-Control: no-store` on those
  requests.
- No ETag or `If-None-Match` revalidation.
- Whole body is buffered in memory and stored in the kv value; avoid for
  large downloads (use [blob](store-files-with-blob.md)).
- Stale entries are only replaced on the next miss; `kv` TTL eviction is
  the backend's job.
- Bypass for one call: set `Cache-Control: no-store` on the request. This
  also skips storing the result.

## Cross-language contract

The stored envelope (`status`, `headers`, base64 `body`) and key
derivation are pinned by fixtures in `contracts/httpcache-v1/` so ports
in other languages share a store. Change the fixtures first if you change
keying or the envelope.

## See also

- [Storage abstractions](../concepts/storage-abstractions.md): the `kv`
  layer underneath.
- [breaker reference](../reference/breaker.md)
