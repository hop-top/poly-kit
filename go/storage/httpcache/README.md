# httpcache

Caching `http.RoundTripper` backed by a `kit/storage/kv` TTLStore.
Composes with `breaker.WrapHTTP` (cache outside, breaker inside).

```go
store, _ := kv.Open(kv.Config{Backend: "sqlite", Path: dbPath})
defer store.Close()
b := breaker.New("fetch")
client := &http.Client{Transport: httpcache.New(
	store.(kv.TTLStore),
	breaker.WrapHTTP(b, http.DefaultTransport),
	httpcache.WithTTL(24*time.Hour),
)}
```

v1 caches GET + 2xx only, honors `Cache-Control: no-store`, keys on
`sha256(method+" "+url)`. Stores a language-neutral JSON envelope
(status, headers, base64 body) per the cross-language contract in
`contracts/httpcache-v1/`. Non-goals (v2 seams): Vary keying, ETag
revalidation, max-age-derived TTL.
