package oauth

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"sync"
	"time"
	"weak"

	"hop.top/kit/go/storage/secret"
)

// DefaultSkew is how long before expiry a cached token counts as stale.
const DefaultSkew = 5 * time.Minute

// Option configures TokenSource.
type Option func(*Source)

// WithHTTPClient sets the client used for token requests (default
// http.DefaultClient). Redirects are never followed regardless.
func WithHTTPClient(hc *http.Client) Option { return func(s *Source) { s.hc = hc } }

// WithSkew sets how much remaining lifetime makes a token stale (default
// DefaultSkew). A token whose expiry is at or inside now+skew is refreshed.
// For a token minted by this process the skew is capped at half its
// lifetime, so a token that lives no longer than the skew is still reused.
func WithSkew(d time.Duration) Option { return func(s *Source) { s.skew = d } }

// WithClock replaces time.Now for freshness decisions (tests).
func WithClock(now func() time.Time) Option { return func(s *Source) { s.now = now } }

// WithLockDir sets the directory of the per-reference advisory lock files
// (default: a "hop-kit-oauth" directory under os.UserCacheDir()).
func WithLockDir(dir string) Option { return func(s *Source) { s.lockDir = dir } }

// WithWriteBackRetry bounds the retries of a failed rotated-refresh-token
// write: attempts tries in total, sleeping base, 2*base, 4*base... between
// them. Defaults: 4 attempts, 100ms.
func WithWriteBackRetry(attempts int, base time.Duration) Option {
	return func(s *Source) { s.wbAttempts, s.wbBase = attempts, base }
}

// Source mints and caches access tokens for one grant. Safe for concurrent
// use. It satisfies cli.OAuthTokenSource.
type Source struct {
	store secret.Store
	ref   string
	g     Grant
	key   string
	pkey  pendingKeyT // key of the package-level pending rotation
	hc    *http.Client
	skew  time.Duration
	now   func() time.Time

	lockDir    string
	wbAttempts int
	wbBase     time.Duration
	persist    bool // store holds the cached token and the rotated refresh token

	sem chan struct{} // single-flight: one refresh at a time

	mu  sync.Mutex
	mem *CachedToken
	gen uint64 // bumped by Invalidate
	// rejected holds digests of access tokens dropped by Invalidate, so a
	// persisted copy that outlived a failed store Delete is never served.
	rejected [][32]byte
}

// maxRejected bounds the rejected-token memory.
const maxRejected = 8

// TokenSource reads and validates the grant stored at ref and returns a
// Source for it. A refresh_token grant on a store that cannot hold the
// rotated token fails here with ErrNotWritable.
//
// The cached token lives in memory and, when store is writable, at
// TokenKey(ref, grant) so other Sources and processes reuse it. Token calls
// are single-flight per Source; across processes an advisory file lock per
// ref serializes refresh and write-back.
func TokenSource(ctx context.Context, store secret.Store, ref string, opts ...Option) (*Source, error) {
	sec, err := store.Get(ctx, ref)
	switch {
	case errorsIsNotFound(err):
		return nil, invalid("", "grant not found")
	case err != nil:
		return nil, fmt.Errorf("oauth: read grant: %w", ErrTransient)
	}
	g, err := DecodeGrant(sec.Value)
	if err != nil {
		return nil, err
	}
	if g.Kind == RefreshToken {
		if err := checkRefreshWritable(store, "refresh token store"); err != nil {
			return nil, err
		}
	}
	s := &Source{
		store: store, ref: ref, g: g, key: TokenKey(ref, g), pkey: newPendingKey(store, ref, g),
		skew: DefaultSkew, now: time.Now, wbAttempts: 4, wbBase: 100 * time.Millisecond,
		sem: make(chan struct{}, 1),
	}
	for _, o := range opts {
		o(s)
	}
	if s.skew < 0 {
		s.skew = 0
	}
	if s.wbAttempts < 1 {
		s.wbAttempts = 1
	}
	_, mutable := store.(secret.MutableStore)
	s.persist = IsWritable(store) && mutable
	if s.persist && s.lockDir == "" {
		// A non-rotating grant only caches, so it runs without a lock.
		if s.lockDir, err = defaultLockDir(); err != nil && g.Kind == RefreshToken {
			return nil, err
		}
	}
	register(ref, s)
	return s, nil
}

// Token returns a token with more than the skew left, minting one when the
// cached token is missing or stale.
func (s *Source) Token(ctx context.Context) (Token, error) {
	if t, ok := s.fresh(); ok {
		return t, nil
	}
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return Token{}, fmt.Errorf("oauth: waiting for refresh: %w: %w", ErrTransient, ctx.Err())
	}
	if t, ok := s.fresh(); ok {
		return t, nil
	}
	ct, err := s.refresh(ctx)
	if err != nil {
		return Token{}, err
	}
	return ct.Token, nil
}

// Invalidate drops the cached token, in memory and in the store, so the
// next Token call refreshes. A refresh already in flight is not cached or
// persisted. If the store cannot delete the persisted copy, the dropped token
// is remembered in memory and ignored when read back.
func (s *Source) Invalidate() {
	s.mu.Lock()
	if s.mem != nil {
		s.reject(s.mem.AccessToken)
	}
	s.mem = nil
	s.gen++
	s.mu.Unlock()
	if !s.persist {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ms := s.store.(secret.MutableStore)
	if err := ms.Delete(ctx, s.key); err == nil {
		return
	}
	// The persisted copy may survive: remember what it holds.
	if sec, err := s.store.Get(ctx, s.key); err == nil {
		if ct, err := DecodeCachedToken(sec.Value); err == nil {
			s.mu.Lock()
			s.reject(ct.AccessToken)
			s.mu.Unlock()
		}
	}
}

// reject records access as rejected; s.mu must be held.
func (s *Source) reject(access string) {
	d := sha256.Sum256([]byte(access))
	if len(s.rejected) >= maxRejected {
		s.rejected = s.rejected[1:]
	}
	s.rejected = append(s.rejected, d)
}

func (s *Source) isRejected(access string) bool {
	d := sha256.Sum256([]byte(access))
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rejected {
		if r == d {
			return true
		}
	}
	return false
}

func (s *Source) fresh() (Token, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mem != nil && !s.mem.Expired(s.now(), effectiveSkew(s.skew, s.mem.AccessToken)) {
		return s.mem.Token, true
	}
	return Token{}, false
}

func (s *Source) generation() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gen
}

func (s *Source) remember(ct CachedToken, gen uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gen == gen {
		s.mem = &ct
	}
}

// refresh runs with the in-process slot held.
func (s *Source) refresh(ctx context.Context) (CachedToken, error) {
	gen := s.generation()
	if ct, ok := s.readPersisted(ctx); ok {
		s.remember(ct, gen)
		return ct, nil
	}
	if s.persist {
		unlock, err := s.lock(ctx)
		if err != nil {
			return CachedToken{}, err
		}
		defer unlock()
		// Another process may have refreshed or rotated while we waited.
		s.flushPending(ctx)
		if ct, ok := s.readPersisted(ctx); ok {
			s.remember(ct, gen)
			return ct, nil
		}
	}
	ct, err := s.mint(ctx)
	if err != nil {
		return CachedToken{}, err
	}
	if ct.Expired(s.now(), 0) {
		return CachedToken{}, errTokenIncomplete
	}
	recordLifetime(ct.AccessToken, time.Until(ct.ExpiresAt), ct.ExpiresAt)
	if s.persist && s.generation() == gen {
		if raw, err := EncodeCachedToken(ct); err == nil {
			ms := s.store.(secret.MutableStore)
			_ = ms.Set(ctx, s.key, raw)
			if s.generation() != gen {
				// Invalidate ran during the write: undo it.
				s.mu.Lock()
				s.reject(ct.AccessToken)
				s.mu.Unlock()
				_ = ms.Delete(context.WithoutCancel(ctx), s.key)
			}
		}
	}
	s.remember(ct, gen)
	return ct, nil
}

// lock takes the cross-process lock. A rotating grant (refresh_token) needs it
// to serialize exchange and write-back, so a failure is an error. A
// non-rotating grant only caches: without a lock it proceeds, still
// single-flight within this Source, and at worst two processes both mint.
// A canceled ctx is always an error.
func (s *Source) lock(ctx context.Context) (func(), error) {
	if s.lockDir == "" && s.g.Kind != RefreshToken {
		return func() {}, nil
	}
	unlock, err := lockRef(ctx, s.lockDir, s.ref)
	if err == nil {
		return unlock, nil
	}
	if s.g.Kind == RefreshToken || ctx.Err() != nil {
		return nil, err
	}
	return func() {}, nil
}

// readPersisted returns the stored cached token when present and fresh.
func (s *Source) readPersisted(ctx context.Context) (CachedToken, bool) {
	if !s.persist {
		return CachedToken{}, false
	}
	sec, err := s.store.Get(ctx, s.key)
	if err != nil {
		return CachedToken{}, false
	}
	ct, err := DecodeCachedToken(sec.Value)
	if err != nil || ct.Expired(s.now(), effectiveSkew(s.skew, ct.AccessToken)) || s.isRejected(ct.AccessToken) {
		return CachedToken{}, false
	}
	return ct, true
}

func (s *Source) mint(ctx context.Context) (CachedToken, error) {
	switch s.g.Kind {
	case ClientCredentials:
		return fetchClientCredentials(ctx, s.store, s.ref, s.g, s.hc)
	case ClientAssertion:
		return fetchClientAssertion(ctx, s.store, s.ref, s.g, s.hc)
	case JWTBearer:
		return fetchJWTBearer(ctx, s.store, s.ref, s.g, s.hc)
	}
	return s.mintRefresh(ctx)
}

// mintRefresh exchanges the refresh token. A rotated token is held in
// memory until the store accepts it; while one is pending it, never the
// stale stored one, is what the provider sees.
func (s *Source) mintRefresh(ctx context.Context) (CachedToken, error) {
	rt, err := s.pendingRT(ctx)
	if err != nil {
		return CachedToken{}, err
	}
	if rt == "" {
		if rt, err = readSecret(ctx, s.store, s.g.SecretRef, "secret_ref"); err != nil {
			return CachedToken{}, err
		}
	}
	ct, rotated, err := exchangeRefresh(ctx, s.store, s.g, s.hc, rt)
	if rotated != "" {
		s.setPending(rotated, rt)
		s.flushPending(ctx)
	}
	if err != nil {
		return CachedToken{}, err
	}
	return ct, nil
}

// flushPending writes the pending rotated refresh token with bounded
// backoff. The write ignores ctx cancellation: abandoning it would lose the
// only valid refresh token. On exhaustion the token stays pending and the
// next refresh retries it first.
func (s *Source) flushPending(ctx context.Context) {
	rt, err := s.pendingRT(ctx)
	if err != nil || rt == "" {
		return
	}
	ms := s.store.(secret.MutableStore)
	wait := s.wbBase
	for i := 0; i < s.wbAttempts; i++ {
		if i > 0 {
			time.Sleep(wait)
			wait *= 2
		}
		wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		err := storeRefreshToken(wctx, ms, s.g.SecretRef, rt)
		cancel()
		if err == nil {
			s.clearPending(rt)
			return
		}
	}
}

// registry lets the package-level Invalidate reach live Sources by ref.
var (
	regMu    sync.Mutex
	registry = map[string][]weak.Pointer[Source]{}
)

func register(ref string, s *Source) {
	regMu.Lock()
	defer regMu.Unlock()
	live := registry[ref][:0]
	for _, w := range registry[ref] {
		if w.Value() != nil {
			live = append(live, w)
		}
	}
	registry[ref] = append(live, weak.Make(s))
}

// Invalidate invalidates every live Source in this process created for ref
// (see Source.Invalidate). A host that does not hold the Source calls it
// after the provider answers 401.
func Invalidate(ref string) {
	regMu.Lock()
	var srcs []*Source
	for _, w := range registry[ref] {
		if s := w.Value(); s != nil {
			srcs = append(srcs, s)
		}
	}
	regMu.Unlock()
	for _, s := range srcs {
		s.Invalidate()
	}
}
