package httpcache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

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
	if storableFor(req, resp) {
		t.save(req, key, resp) // mutates resp.Body into a replayable reader
	}
	return resp, nil
}

// key derives the primary cache key as prefix + sha256(method + " " +
// url). It never depends on request headers: responses without a Vary
// header live here, exactly as pinned by contracts/httpcache-v1/keying.json.
// Responses that carry Vary are stored under varyKey instead.
func (t *Transport) key(req *http.Request) string {
	return t.cfg.prefix + digest(req.Method+" "+req.URL.String())
}

func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// varyIndexKey is where the Vary field list of a URL is recorded:
// prefix + "varyidx:" + sha256(method + " " + url).
func (t *Transport) varyIndexKey(req *http.Request) string {
	return t.cfg.prefix + "varyidx:" + digest(req.Method+" "+req.URL.String())
}

// varyKey derives the secondary key for the variant of req selected by
// the Vary field names (see varyFields): prefix + "vary:" + sha256 over
// method + " " + url, then for each field, in order, "\n" + field + ":" +
// the request's values for it joined by ", " (each trimmed). A request
// without the field contributes an empty value, so "absent" is its own
// variant. Pinned by contracts/httpcache-v1/keying.json (vary section).
func (t *Transport) varyKey(req *http.Request, fields []string) string {
	var b strings.Builder
	b.WriteString(req.Method + " " + req.URL.String())
	for _, f := range fields {
		vals := append([]string(nil), req.Header.Values(f)...) // copy: never mutate the request
		for i, v := range vals {
			vals[i] = strings.TrimSpace(v)
		}
		b.WriteString("\n" + f + ":" + strings.Join(vals, ", "))
	}
	return t.cfg.prefix + "vary:" + digest(b.String())
}

// load fetches and decodes a stored response. Any read/decode failure
// (or a genuine miss) reports !ok, so the caller refetches. When the URL
// has a recorded Vary field list the matching variant is loaded;
// otherwise the primary entry is, and an entry that itself names a Vary
// (written by a Vary-unaware writer sharing the store) is a miss.
func (t *Transport) load(req *http.Request, key string) (*http.Response, bool) {
	ctx := req.Context()
	if idx, ok, err := t.store.Get(ctx, t.varyIndexKey(req)); err == nil && ok {
		var fields []string
		if json.Unmarshal(idx, &fields) != nil || len(fields) == 0 {
			return nil, false
		}
		return t.loadKey(req, t.varyKey(req, fields), false)
	}
	return t.loadKey(req, key, true)
}

func (t *Transport) loadKey(req *http.Request, key string, primary bool) (*http.Response, bool) {
	raw, ok, err := t.store.Get(req.Context(), key)
	if err != nil || !ok {
		return nil, false
	}
	resp, err := decodeEntry(raw, req)
	if err != nil {
		return nil, false
	}
	if primary && len(varyFields(resp.Header)) > 0 {
		return nil, false
	}
	return resp, true
}

// save serializes resp and writes it. Without Vary it lands on the
// primary key (and clears any stale Vary index for the URL); with Vary it
// lands on the variant key and the field list is recorded in the index.
// A positive TTL uses PutWithTTL; a non-positive TTL uses Put so the
// entry has no expiry (PutWithTTL would otherwise stamp an already-past
// expiry and the entry would never be served). encodeEntry refills
// resp.Body so the response stays usable; write errors are intentionally
// ignored — caching is best-effort.
func (t *Transport) save(req *http.Request, key string, resp *http.Response) {
	raw, err := encodeEntry(resp)
	if err != nil {
		return
	}
	ctx := req.Context()
	fields := varyFields(resp.Header)
	if len(fields) == 0 {
		_ = t.store.Delete(ctx, t.varyIndexKey(req))
		t.put(req, key, raw)
		return
	}
	idx, err := json.Marshal(fields)
	if err != nil {
		return
	}
	t.put(req, t.varyIndexKey(req), idx)
	t.put(req, t.varyKey(req, fields), raw)
}

func (t *Transport) put(req *http.Request, key string, raw []byte) {
	if t.cfg.ttl > 0 {
		_ = t.store.PutWithTTL(req.Context(), key, raw, t.cfg.ttl)
		return
	}
	_ = t.store.Put(req.Context(), key, raw)
}
