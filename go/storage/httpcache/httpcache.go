package httpcache

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"

	"hop.top/kit/go/storage/kv"
)

// Transport is a caching http.RoundTripper backed by a kv.TTLStore.
// Construct it with New.
type Transport struct {
	store kv.TTLStore
	inner http.RoundTripper
	cfg   config
}

// Ensure Transport satisfies the RoundTripper contract.
var _ http.RoundTripper = (*Transport)(nil)

// New returns a caching RoundTripper that serves cacheable GETs from
// store and falls through to inner on a miss. inner is the base
// transport — typically breaker.WrapHTTP(b, http.DefaultTransport);
// pass http.DefaultTransport for no resilience layer. A nil inner
// defaults to http.DefaultTransport.
//
// store is borrowed, not owned: New never Closes it.
func New(store kv.TTLStore, inner http.RoundTripper, opts ...Option) *Transport {
	if inner == nil {
		inner = http.DefaultTransport
	}
	return &Transport{store: store, inner: inner, cfg: newConfig(opts...)}
}

// RoundTrip serves req from cache when possible, otherwise fetches via
// the inner transport and stores a cacheable response before returning
// it. It never returns a cache-layer error to the caller: a store read
// or decode failure degrades to a miss, and a store write failure is
// swallowed (the fetched response is still returned).
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !cacheableRequest(req) {
		return t.inner.RoundTrip(req)
	}

	key := t.key(req)
	if resp, ok := t.load(req, key); ok {
		return resp, nil
	}

	resp, err := t.inner.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if cacheableResponse(resp) {
		t.save(req, key, resp) // mutates resp.Body into a replayable reader
	}
	return resp, nil
}

// key derives the cache key as prefix + sha256(method + " " + url).
// Vary-aware keying is a v1 non-goal (see package doc); this is pinned
// by contracts/httpcache-v1/keying.json.
func (t *Transport) key(req *http.Request) string {
	sum := sha256.Sum256([]byte(req.Method + " " + req.URL.String()))
	return t.cfg.prefix + hex.EncodeToString(sum[:])
}

// load fetches and decodes a stored response. Any read/decode failure
// (or a genuine miss) reports !ok, so the caller refetches.
func (t *Transport) load(req *http.Request, key string) (*http.Response, bool) {
	raw, ok, err := t.store.Get(req.Context(), key)
	if err != nil || !ok {
		return nil, false
	}
	resp, err := decodeEntry(raw, req)
	if err != nil {
		return nil, false
	}
	return resp, true
}

// save serializes resp and writes it under key with the configured TTL.
// encodeEntry refills resp.Body so the response stays usable; write
// errors are intentionally ignored — caching is best-effort.
func (t *Transport) save(req *http.Request, key string, resp *http.Response) {
	raw, err := encodeEntry(resp)
	if err != nil {
		return
	}
	_ = t.store.PutWithTTL(req.Context(), key, raw, t.cfg.ttl)
}
