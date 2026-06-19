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
//   - A stored entry past its TTL is a miss (the request is refetched).
//
// Entries are persisted as a language-neutral JSON envelope (status,
// headers, base64 body) rather than a Go-specific wire dump, so the
// TS and Python parity ports read and write the same store and the
// same cross-language test vectors. See the entry type for the format.
//
// httpcache borrows the TTLStore; it never Opens or Closes it. The
// caller owns the store's lifecycle, exactly as breaker.WrapHTTP's
// caller owns the wrapped RoundTripper.
//
// Deliberate v1 non-goals (additive seams for a later version):
// Vary-aware keying, ETag/If-None-Match revalidation, and deriving
// TTL from Cache-Control max-age. v1 keys on method+URL and honors
// only no-store; WithTTL is authoritative for freshness.
package httpcache
