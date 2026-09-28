package cmdsurface

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"hop.top/kit/go/ai/cmdreflect"
	"hop.top/kit/go/storage/kv"
)

// AnnotationCacheTTL is the cobra annotation a read command sets to let
// the served result cache answer it: a Go duration greater than zero
// ("30s", "5m"), how long one result may be served without running the
// command again.
//
// It is honored only on a leaf that declares kit/side-effect: read. A
// write, destructive, interactive, unannotated or inferred tier is never
// cached, whatever it declares here.
const AnnotationCacheTTL = "kit/cache-ttl"

// ParseCacheTTL parses a kit/cache-ttl value. Anything that is not a
// Go duration greater than zero is an error.
func ParseCacheTTL(v string) (time.Duration, error) {
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil {
		return 0, fmt.Errorf("%s %q: want a Go duration such as 30s or 5m", AnnotationCacheTTL, v)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s %q: want a duration greater than zero", AnnotationCacheTTL, v)
	}
	return d, nil
}

// cacheTTLOf resolves a leaf's cache TTL at discovery: zero unless the
// leaf declares the read tier itself and a valid kit/cache-ttl.
func cacheTTLOf(d *cmdreflect.Descriptor) time.Duration {
	if d == nil || d.Safety.Tier != cmdreflect.TierRead || d.Safety.TierInferred {
		return 0
	}
	raw := annotationOf(d.Cmd, AnnotationCacheTTL)
	if raw == "" {
		return 0
	}
	ttl, err := ParseCacheTTL(raw)
	if err != nil {
		return 0
	}
	return ttl
}

// CacheTTL returns how long the result cache may serve one result of
// this leaf: its kit/cache-ttl on a read leaf, zero otherwise.
func (l *Leaf) CacheTTL() time.Duration { return l.cacheTTL }

// cacheSurfaces are the surfaces the result cache reaches: the REST
// projection, which is also where HTTP renders ETag, 304 and
// Cache-Control.
var cacheSurfaces = map[Surface]bool{SurfaceREST: true}

// Audit marks. A result the cache answers is audited like any other,
// with Meta.Extra["cache"] saying it did not run.
const (
	cacheExtraKey = "cache"
	// CacheHit marks a result answered from the store.
	CacheHit = "hit"
	// CacheCoalesced marks a result answered by an identical call
	// that was already running.
	CacheCoalesced = "coalesced"
)

// WithResultCache turns on the read-tier result cache with store
// holding the results. A leaf declaring kit/side-effect: read and a
// kit/cache-ttl is then answered from store while its result is
// fresh, on the surfaces the cache reaches (REST), and identical calls
// that arrive while one is running wait for it instead of running
// again. A nil store leaves the cache off, which is the default.
//
// Results are keyed by the command path, flags and args, and the
// caller's identity (principal, tenant and scopes), so one caller is
// never answered with another's result. Only a run that succeeds —
// no error, exit code zero — is stored.
//
// Only an identity the transport established counts: a claimed
// caller, tenant or scopes is keyed as anonymous. A command whose
// output depends on Meta.Caller must therefore require an established
// identity (kit/auth-required) or not declare kit/cache-ttl.
//
// The bridge does not close store.
func WithResultCache(store kv.TTLStore) Option {
	return func(c *bridgeConfig) { c.cache = store }
}

// resultCache is the bridge's store plus the calls in flight.
type resultCache struct {
	store kv.TTLStore
	now   func() time.Time

	mu       sync.Mutex
	inflight map[string]*cacheFlight
}

func newResultCache(store kv.TTLStore) *resultCache {
	if store == nil {
		return nil
	}
	return &resultCache{store: store, now: time.Now, inflight: map[string]*cacheFlight{}}
}

// cacheFlight is one run other identical calls wait on. entry is set
// before done is closed, and only when the run's result was stored.
type cacheFlight struct {
	done  chan struct{}
	entry *cacheEntry
	// waiters counts the calls waiting on the flight, under the
	// cache's lock.
	waiters int
}

// join returns the flight for key, and whether the caller leads it:
// the leader runs, everyone else waits.
func (rc *resultCache) join(key string) (*cacheFlight, bool) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if f, ok := rc.inflight[key]; ok {
		f.waiters++
		return f, false
	}
	f := &cacheFlight{done: make(chan struct{})}
	rc.inflight[key] = f
	return f, true
}

// land ends the leader's flight, handing entry to whoever waited.
func (rc *resultCache) land(key string, f *cacheFlight, e *cacheEntry) {
	rc.mu.Lock()
	delete(rc.inflight, key)
	rc.mu.Unlock()
	f.entry = e
	close(f.done)
}

// cacheEntry is what the store holds for one result.
type cacheEntry struct {
	Result  Result    `json:"result"`
	ETag    string    `json:"etag"`
	Expires time.Time `json:"expires"`
}

// lookup reads a fresh entry for key; any failure reads as a miss.
func (rc *resultCache) lookup(ctx context.Context, key string) *cacheEntry {
	raw, ok, err := rc.store.Get(ctx, key)
	if err != nil || !ok {
		return nil
	}
	var e cacheEntry
	if err := decodeCanonical(raw, &e); err != nil || !rc.now().Before(e.Expires) {
		return nil
	}
	return &e
}

// cacheCall is the cache's part of one admitted invocation.
type cacheCall struct {
	rc      *resultCache
	key     string
	ttl     time.Duration
	private bool
	// hit is the fresh entry the lookup at admission found.
	hit *cacheEntry
	// info is how the caller may cache the outcome, set when the
	// result came from the cache or was stored in it.
	info   CacheInfo
	served bool
}

// CacheInfo tells a transport how the result it is about to send may be
// cached by its caller. The api projection renders it as ETag and
// Cache-Control, and answers a matching If-None-Match with 304.
type CacheInfo struct {
	// ETag is an opaque validator for this result, unquoted. It is
	// derived from the result and the cache key, so two callers never
	// share one.
	ETag string
	// MaxAge is how long the result stays fresh from now.
	MaxAge time.Duration
	// Private reports that the result belongs to one caller: the
	// transport established an identity naming a principal, tenant or
	// scopes.
	Private bool
	// Hit reports that the result was answered without running the
	// command: from the store, or by an identical call in flight.
	Hit bool
}

// Cache reports how the admitted invocation's result may be cached, once
// Run has returned it. ok is false when the leaf is not cacheable here,
// or when the run's result was not stored (an error, a non-zero exit).
func (a *Admission) Cache() (CacheInfo, bool) {
	if a == nil || a.cache == nil || !a.cache.served {
		return CacheInfo{}, false
	}
	return a.cache.info, true
}

// cacheLookup is invocation-plane slot 8's read-tier cache step. It
// runs after any idempotency replay and returns nil when the cache has
// nothing to do with inv: no store, a leaf without a TTL, a surface the
// cache does not reach, or an invocation that cannot be keyed.
// Otherwise the returned call carries the key and, on a hit, the entry
// Run answers with instead of running.
func (b *Bridge) cacheLookup(ctx context.Context, inv Invocation, leaf *Leaf) *cacheCall {
	if b.rcache == nil || leaf == nil || leaf.cacheTTL <= 0 || !cacheSurfaces[inv.Meta.Surface] {
		return nil
	}
	key, err := cacheKey(b.root, inv)
	if err != nil {
		return nil
	}
	return &cacheCall{
		rc:      b.rcache,
		key:     key,
		ttl:     leaf.cacheTTL,
		private: callerScoped(inv.Meta),
		hit:     b.rcache.lookup(ctx, key),
	}
}

// runCached is Run for an invocation the cache handles: answer a hit,
// wait on an identical call in flight, or lead the run and store its
// result.
func (a *Admission) runCached(ctx context.Context) (Result, error) {
	ctx = a.b.auditContext(ctx, a.leaf)
	c := a.cache
	if c.hit != nil {
		return a.answerFromCache(ctx, c.hit, CacheHit), nil
	}
	f, leader := c.rc.join(c.key)
	if !leader {
		select {
		case <-f.done:
			if f.entry != nil {
				return a.answerFromCache(ctx, f.entry, CacheCoalesced), nil
			}
			// The run this call waited on stored nothing: an error
			// or a failure is never shared, so run for itself.
			return a.runUncached(ctx)
		case <-ctx.Done():
			err := ctx.Err()
			a.auditOutcome(ctx, a.inv, Result{}, err)
			return Result{}, err
		}
	}
	// A result stored between admission and now answers this call
	// too; the flight still ends, so its waiters take it.
	if e := c.rc.lookup(ctx, c.key); e != nil {
		c.rc.land(c.key, f, e)
		return a.answerFromCache(ctx, e, CacheHit), nil
	}
	var entry *cacheEntry
	defer func() { c.rc.land(c.key, f, entry) }()
	res, err := a.runBounded(ctx)
	if err == nil && res.ExitCode == 0 {
		if e := c.store(ctx, res); e != nil {
			entry = e
			res = e.Result
			c.info = CacheInfo{ETag: e.ETag, MaxAge: c.ttl, Private: c.private}
			c.served = true
		}
	}
	a.auditOutcome(ctx, a.inv, res, err)
	return res, err
}

// runUncached runs the invocation as Run does without a cache.
func (a *Admission) runUncached(ctx context.Context) (Result, error) {
	res, err := a.runBounded(ctx)
	a.auditOutcome(ctx, a.inv, res, err)
	return res, err
}

// streamHit answers a stream from a cached result: one "done" event
// carrying it, then close. Nothing runs.
func (a *Admission) streamHit(ctx context.Context, out chan<- Event) error {
	ctx = a.b.auditContext(ctx, a.leaf)
	res := a.answerFromCache(ctx, a.cache.hit, CacheHit)
	out <- Event{Kind: "done", Data: &res, At: a.cache.rc.now()}
	close(out)
	return nil
}

// answerFromCache returns e's result for this call, audited with mark.
func (a *Admission) answerFromCache(ctx context.Context, e *cacheEntry, mark string) Result {
	c := a.cache
	c.info = CacheInfo{
		ETag:    e.ETag,
		MaxAge:  max(e.Expires.Sub(c.rc.now()), 0),
		Private: c.private,
		Hit:     true,
	}
	c.served = true
	inv := a.inv
	extra := make(map[string]string, len(inv.Meta.Extra)+1)
	for k, v := range inv.Meta.Extra {
		extra[k] = v
	}
	extra[cacheExtraKey] = mark
	inv.Meta.Extra = extra
	a.auditOutcome(ctx, inv, e.Result, nil)
	return e.Result
}

// auditOutcome audits one outcome on a remote surface, as Run does.
func (a *Admission) auditOutcome(ctx context.Context, inv Invocation, res Result, err error) {
	if inv.Meta.Surface.remote() {
		a.b.Audit(ctx, inv, res, err)
	}
}

// store writes res under the call's key and returns the entry, or nil
// when res cannot be encoded. The returned entry's Result is the
// canonical one — decoded back from the stored bytes — so a caller gets
// the same result, byte for byte once encoded, whether it ran the
// command or hit the cache. A store that fails to write only loses the
// entry; the result is still answered.
func (c *cacheCall) store(ctx context.Context, res Result) *cacheEntry {
	body, err := json.Marshal(res)
	if err != nil {
		return nil
	}
	var canon Result
	if err := decodeCanonical(body, &canon); err != nil {
		return nil
	}
	sum := sha256.Sum256([]byte(c.key + "\n" + string(body)))
	e := &cacheEntry{
		Result:  canon,
		ETag:    hex.EncodeToString(sum[:16]),
		Expires: c.rc.now().Add(c.ttl),
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return nil
	}
	// A caller that went away after the run still leaves its result.
	_ = c.rc.store.PutWithTTL(context.WithoutCancel(ctx), c.key, raw, c.ttl)
	return e
}

// decodeCanonical decodes JSON keeping numbers exact: an int64 id
// survives the round trip instead of becoming the nearest float64.
func decodeCanonical(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	return dec.Decode(v)
}

// cacheKeyDoc is what a cache key is the hash of. The surface is not in
// it: the same call answers the same way whichever surface carries it.
type cacheKeyDoc struct {
	Tool  string         `json:"tool"`
	Path  []string       `json:"path"`
	Args  []string       `json:"args"`
	Flags map[string]any `json:"flags"`
	// Caller is the caller's [IdempotencyScope] when the transport
	// established it, else empty: the anonymous entry.
	Caller string   `json:"caller"`
	Scopes []string `json:"scopes"`
}

// cacheKeyPrefix namespaces result-cache keys in a shared store.
const cacheKeyPrefix = "kit/cache/v1/"

// cacheKey is the store key for inv: a hash of the canonical invocation
// — path, flags, args — and the caller's identity. Flags are encoded
// with sorted keys, so their order in a request never matters.
//
// The identity counts only when the transport established it
// ([Meta.Authenticated]), and is scoped as idempotency scopes it
// ([IdempotencyScope]): a verified principal by its tenant and
// principal, plus its credential's scopes; a caller the transport
// vouches for by that transport, whatever name it claims. A claimed
// identity is keyed as anonymous, so no claim — whichever transport
// carries it — reaches the entry of the identity it names.
func cacheKey(root interface{ Name() string }, inv Invocation) (string, error) {
	doc := cacheKeyDoc{
		Path:  inv.Path,
		Args:  inv.Args,
		Flags: inv.Flags,
	}
	if inv.Meta.Authenticated() {
		doc.Caller = IdempotencyScope(inv.Meta)
	}
	if inv.Meta.Established == EstablishedVerified {
		doc.Scopes = callerScopes(inv.Meta)
	}
	if root != nil {
		doc.Tool = root.Name()
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return "", errors.New("cmdsurface: invocation cannot be keyed for the result cache")
	}
	sum := sha256.Sum256(raw)
	return cacheKeyPrefix + hex.EncodeToString(sum[:]), nil
}

// callerScopes returns the caller's scopes, sorted, from the
// comma-joined Meta.Extra entry the transports fill.
func callerScopes(m Meta) []string {
	scopes := splitCSV(m.Extra["scopes"])
	sort.Strings(scopes)
	return scopes
}

// callerScoped reports whether a result belongs to one caller: the
// transport vouches for the caller (the owner), or a verifier
// established an identity naming a principal, a tenant or scopes. A
// claimed identity is keyed as anonymous, and so is not.
func callerScoped(m Meta) bool {
	switch m.Established {
	case EstablishedTransport:
		return true
	case EstablishedVerified:
		return m.Caller != "" || m.Tenant != "" || len(callerScopes(m)) > 0
	default:
		return false
	}
}
