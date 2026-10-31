// Package httpcache provides a caching http.RoundTripper backed by a
// kit/storage/kv TTLStore.
//
// Where kit/breaker bounds how much an HTTP path may do, httpcache
// bounds how often it must reach the wire: a cacheable GET that hit
// the network once is served from the kv store until its TTL lapses.
// It is a transport decorator in the same family as breaker.WrapHTTP —
// a RoundTripper wrapping a RoundTripper — and composes with it:
//
//	store, _ := kv.Open(kv.Config{Backend: "sqlite", Path: dbPath})
//	defer store.Close()
//	b := breaker.New("fetch")
//	rt := httpcache.New(store.(kv.TTLStore),
//		breaker.WrapHTTP(b, http.DefaultTransport),
//		httpcache.WithTTL(24*time.Hour),
//	)
//	client := &http.Client{Transport: rt}
//
// Composition order (outermost → innermost at the client):
//
//	httpcache → breaker → http.DefaultTransport
//
// httpcache sits OUTSIDE the breaker on purpose: a cache hit costs no
// network call, so it must never consume a breaker token or trip a
// rate limit. Only misses fall through to the breaker and the wire.
//
// Caching is conservative by default and HTTP-correct where it is
// cheap to be:
//
//   - Only GET requests are cached; everything else passes through.
//   - Only 2xx responses are stored.
//   - Cache-Control: no-store on request or response opts the
//     exchange out, on both the request and the stored response.
//   - A response with Vary is stored per variant (keyed on the request's
//     values for the varied headers); Vary: * is never stored.
//   - A response to a request with Authorization is stored only when it
//     carries Cache-Control public, s-maxage or must-revalidate (RFC 9111
//     section 3.5), so one identity's response is not replayed to another.
//   - A stored entry past its TTL is a miss (the request is refetched).
//
// Entries are persisted as a language-neutral JSON envelope (status,
// headers, base64 body) rather than a Go-specific wire dump, so parity
// ports in other languages can read and write the same store. The
// format is pinned by the fixtures in contracts/httpcache-v1 — those
// are the contract of record, not this implementation. See the entry
// type for the shape.
//
// httpcache borrows the TTLStore; it never Opens or Closes it. The
// caller owns the store's lifecycle, exactly as breaker.WrapHTTP's
// caller owns the wrapped RoundTripper.
//
// Deliberate v1 non-goals (additive seams for a later version):
// ETag/If-None-Match revalidation and deriving TTL from Cache-Control
// max-age. WithTTL is authoritative for freshness.
package httpcache
