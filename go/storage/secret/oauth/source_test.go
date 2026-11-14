package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hop.top/kit/go/storage/secret"
	"hop.top/kit/go/storage/secret/memory"
)

// wStore is a writable memory store whose first failSets Set calls to
// rtKey... fail (any key when failKey is empty); sets counts attempts.
type wStore struct {
	*memory.Store
	mu       sync.Mutex
	failSets int
	failKey  string
	sets     int
}

func (s *wStore) Writable() bool { return true }
func (s *wStore) Set(ctx context.Context, k string, v []byte) error {
	s.mu.Lock()
	if s.failKey == "" || s.failKey == k {
		s.sets++
		if s.failSets > 0 {
			s.failSets--
			s.mu.Unlock()
			return errors.New("disk full")
		}
	}
	s.mu.Unlock()
	return s.Store.Set(ctx, k, v)
}

func put(t *testing.T, st secret.MutableStore, k, v string) {
	t.Helper()
	if err := st.Set(context.Background(), k, []byte(v)); err != nil {
		t.Fatal(err)
	}
}

func secretEnv(v string) string { return `{"v":1,"kind":"secret","value":"` + v + `"}` }

func putGrant(t *testing.T, st secret.MutableStore, ref string, g Grant) {
	t.Helper()
	raw, err := EncodeGrant(g)
	if err != nil {
		t.Fatal(err)
	}
	put(t, st, ref, string(raw))
}

// srcServer counts client_credentials calls; each returns a distinct token.
func srcServer(t *testing.T, expiresIn int, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		time.Sleep(20 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":"at-%d","expires_in":%d}`, n, expiresIn)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func ccSetup(t *testing.T, st secret.MutableStore, url string) Grant {
	t.Helper()
	g := Grant{Kind: ClientCredentials, TokenURL: url, ClientID: "cid", SecretRef: "cs"}
	put(t, st, "cs", secretEnv("cs-fake"))
	putGrant(t, st, "g", g)
	return g
}

func TestSourceSingleFlight(t *testing.T) {
	var calls atomic.Int32
	srv := srcServer(t, 3600, &calls)
	st := memory.New()
	ccSetup(t, st, srv.URL)
	src, err := TokenSource(context.Background(), st, "g", WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if tok, err := src.Token(context.Background()); err != nil || tok.AccessToken != "at-1" {
				t.Errorf("token %+v err %v", tok, err)
			}
		}()
	}
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Errorf("token endpoint calls = %d, want 1", n)
	}
}

func TestSourceInvalidateRefreshesOnce(t *testing.T) {
	for _, writable := range []bool{false, true} {
		var calls atomic.Int32
		srv := srcServer(t, 3600, &calls)
		var st secret.MutableStore = memory.New()
		if writable {
			st = &wStore{Store: memory.New()}
		}
		ccSetup(t, st, srv.URL)
		src, err := TokenSource(context.Background(), st, "g", WithHTTPClient(srv.Client()), WithLockDir(t.TempDir()))
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		_, _ = src.Token(ctx)
		_, _ = src.Token(ctx)
		if calls.Load() != 1 {
			t.Fatalf("writable=%v: cache miss, calls %d", writable, calls.Load())
		}
		src.Invalidate()
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); _, _ = src.Token(ctx) }()
		}
		wg.Wait()
		if tok, _ := src.Token(ctx); calls.Load() != 2 || tok.AccessToken != "at-2" {
			t.Errorf("writable=%v: after Invalidate calls %d token %q, want 2 / at-2", writable, calls.Load(), tok.AccessToken)
		}
	}
}

func TestPackageInvalidate(t *testing.T) {
	var calls atomic.Int32
	srv := srcServer(t, 3600, &calls)
	st := memory.New()
	ccSetup(t, st, srv.URL)
	src, _ := TokenSource(context.Background(), st, "g", WithHTTPClient(srv.Client()))
	_, _ = src.Token(context.Background())
	Invalidate("g")
	Invalidate("unknown")
	_, _ = src.Token(context.Background())
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want 2", calls.Load())
	}
}

func TestSourcePersistedCacheAvoidsCall(t *testing.T) {
	var calls atomic.Int32
	srv := srcServer(t, 3600, &calls)
	st := &wStore{Store: memory.New()}
	ccSetup(t, st, srv.URL)
	dir := t.TempDir()
	a, _ := TokenSource(context.Background(), st, "g", WithHTTPClient(srv.Client()), WithLockDir(dir))
	first, err := a.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := TokenSource(context.Background(), st, "g", WithHTTPClient(srv.Client()), WithLockDir(dir))
	second, err := b.Token(context.Background())
	if err != nil || calls.Load() != 1 || second.AccessToken != first.AccessToken {
		t.Errorf("fresh Source: calls %d token %+v err %v", calls.Load(), second, err)
	}
}

func TestSourceMemoryOnlyStoreNeverWritesToken(t *testing.T) {
	var calls atomic.Int32
	srv := srcServer(t, 3600, &calls)
	st := memory.New() // not writable
	g := ccSetup(t, st, srv.URL)
	src, _ := TokenSource(context.Background(), st, "g", WithHTTPClient(srv.Client()))
	if _, err := src.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ok, _ := st.Exists(context.Background(), TokenKey("g", g)); ok {
		t.Error("token persisted to a non-writable store")
	}
}

func TestSourceSkewBoundaries(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	skew := 5 * time.Minute
	cases := []struct {
		name    string
		expires time.Time
		refresh bool
	}{
		{"past", now.Add(-time.Hour), true},
		{"at now", now, true},
		{"inside skew", now.Add(skew - time.Second), true},
		{"exactly skew", now.Add(skew), true},
		{"just outside skew", now.Add(skew + time.Nanosecond), false},
		{"far future", now.Add(time.Hour), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := srcServer(t, 3600, &calls)
			st := &wStore{Store: memory.New()}
			g := ccSetup(t, st, srv.URL)
			raw, _ := EncodeCachedToken(CachedToken{Token: Token{AccessToken: "cached", ExpiresAt: c.expires}})
			put(t, st, TokenKey("g", g), string(raw))
			src, _ := TokenSource(context.Background(), st, "g",
				WithHTTPClient(srv.Client()), WithClock(func() time.Time { return now }), WithSkew(skew), WithLockDir(t.TempDir()))
			// The fetched token's expiry comes from the real clock, so
			// move the fake clock only for the persisted decision.
			tok, err := src.Token(context.Background())
			if c.refresh {
				// fresh token expires an hour after the real now; the fake
				// clock is far in the past, so it is accepted.
				if err != nil || calls.Load() != 1 || tok.AccessToken == "cached" {
					t.Errorf("want refresh, got calls %d token %q err %v", calls.Load(), tok.AccessToken, err)
				}
			} else if err != nil || calls.Load() != 0 || tok.AccessToken != "cached" {
				t.Errorf("want cached, got calls %d token %q err %v", calls.Load(), tok.AccessToken, err)
			}
		})
	}
}

func TestSourceNegativeSkewIsZero(t *testing.T) {
	st := memory.New()
	ccSetup(t, st, "http://127.0.0.1:1")
	src, _ := TokenSource(context.Background(), st, "g", WithSkew(-time.Hour))
	if src.skew != 0 {
		t.Errorf("skew = %v", src.skew)
	}
}

func TestSourceRejectsNonPositiveExpiresIn(t *testing.T) {
	for _, n := range []int{0, -5} {
		var calls atomic.Int32
		srv := srcServer(t, n, &calls)
		st := &wStore{Store: memory.New()}
		g := ccSetup(t, st, srv.URL)
		src, _ := TokenSource(context.Background(), st, "g", WithHTTPClient(srv.Client()), WithLockDir(t.TempDir()))
		if tok, err := src.Token(context.Background()); err == nil {
			t.Errorf("expires_in %d: accepted %+v", n, tok)
		}
		if ok, _ := st.Exists(context.Background(), TokenKey("g", g)); ok {
			t.Errorf("expires_in %d: token cached", n)
		}
	}
}

func TestSourceExpiredOnArrivalRejected(t *testing.T) {
	var calls atomic.Int32
	srv := srcServer(t, 3600, &calls)
	st := memory.New()
	ccSetup(t, st, srv.URL)
	future := time.Now().Add(48 * time.Hour) // clock ahead of the provider
	src, _ := TokenSource(context.Background(), st, "g", WithHTTPClient(srv.Client()), WithClock(func() time.Time { return future }))
	if tok, err := src.Token(context.Background()); err == nil {
		t.Errorf("accepted already-expired token %+v", tok)
	}
}

func TestTokenSourceConfiguration(t *testing.T) {
	ctx := context.Background()
	st := memory.New() // not writable
	put(t, st, "rt", secretEnv("x"))
	putGrant(t, st, "r", Grant{Kind: RefreshToken, TokenURL: "https://idp.example/t", ClientID: "c", SecretRef: "rt"})
	if _, err := TokenSource(ctx, st, "r"); !errors.Is(err, ErrNotWritable) {
		t.Errorf("refresh grant on non-writable store: %v", err)
	}
	if _, err := TokenSource(ctx, st, "missing"); err == nil {
		t.Error("missing grant accepted")
	}
	put(t, st, "bad", `{"v":1}`)
	if _, err := TokenSource(ctx, st, "bad"); err == nil {
		t.Error("invalid grant accepted")
	}
	// non-rotating grant on a non-writable store is fine
	putGrant(t, st, "c", Grant{Kind: ClientCredentials, TokenURL: "https://idp.example/t", ClientID: "c", SecretRef: "rt"})
	if _, err := TokenSource(ctx, st, "c"); err != nil {
		t.Errorf("client_credentials on memory: %v", err)
	}
}

// rotServer rotates refresh tokens: it accepts only the current one.
type rotServer struct {
	*httptest.Server
	mu    sync.Mutex
	cur   string
	seen  []string
	calls int
	bad   int
}

func newRotServer(t *testing.T, first string, delay time.Duration) *rotServer {
	t.Helper()
	rs := &rotServer{cur: first}
	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got := r.PostForm.Get("refresh_token")
		rs.mu.Lock()
		rs.seen = append(rs.seen, got)
		rs.calls++
		ok := got == rs.cur
		if !ok {
			rs.bad++
		}
		time.Sleep(delay)
		next := fmt.Sprintf("rt-fake-%d", rs.calls)
		if ok {
			rs.cur = next
		}
		rs.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if !ok {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		fmt.Fprintf(w, `{"access_token":"at-%d","expires_in":3600,"refresh_token":%q}`, rs.calls, next)
	}))
	t.Cleanup(rs.Close)
	return rs
}

func rtSetup(t *testing.T, st secret.MutableStore, url, first string) {
	t.Helper()
	put(t, st, "rt", secretEnv(first))
	putGrant(t, st, "g", Grant{Kind: RefreshToken, TokenURL: url, ClientID: "cid", SecretRef: "rt"})
}

func storedRT(t *testing.T, st secret.Store) string {
	t.Helper()
	s, err := st.Get(context.Background(), "rt")
	if err != nil {
		t.Fatal(err)
	}
	return string(s.Value)
}

func TestRotationWriteBackRetried(t *testing.T) {
	rs := newRotServer(t, "rt-fake-0", 0)
	st := &wStore{Store: memory.New()}
	rtSetup(t, st, rs.URL, "rt-fake-0")
	st.failKey, st.failSets = "rt", 1
	src, err := TokenSource(context.Background(), st, "g", WithHTTPClient(rs.Client()),
		WithLockDir(t.TempDir()), WithWriteBackRetry(3, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rs.calls != 1 {
		t.Errorf("provider calls = %d, want 1", rs.calls)
	}
	if got := storedRT(t, st); got != secretEnv("rt-fake-1") {
		t.Errorf("stored = %s, want rotated token persisted", got)
	}
	src.Invalidate()
	if _, err := src.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rs.bad != 0 || rs.seen[1] != "rt-fake-1" {
		t.Errorf("old refresh token re-sent: %v", rs.seen)
	}
}

func TestRotationPendingNeverResendsOld(t *testing.T) {
	rs := newRotServer(t, "rt-fake-0", 0)
	st := &wStore{Store: memory.New()}
	rtSetup(t, st, rs.URL, "rt-fake-0")
	st.failKey, st.failSets = "rt", 1000
	src, _ := TokenSource(context.Background(), st, "g", WithHTTPClient(rs.Client()),
		WithLockDir(t.TempDir()), WithWriteBackRetry(2, time.Millisecond))
	if _, err := src.Token(context.Background()); err != nil {
		t.Fatalf("valid access token must be returned despite write-back failure: %v", err)
	}
	if got := storedRT(t, st); got != secretEnv("rt-fake-0") {
		t.Fatalf("setup: stored %s", got)
	}
	// Store recovers; next refresh must use the pending token and persist it.
	st.mu.Lock()
	st.failSets = 0
	st.mu.Unlock()
	src.Invalidate()
	if _, err := src.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rs.bad != 0 || rs.seen[1] != "rt-fake-1" {
		t.Errorf("provider saw %v, want pending token second", rs.seen)
	}
	if got := storedRT(t, st); got != secretEnv("rt-fake-2") {
		t.Errorf("stored = %s, want latest rotated token", got)
	}
}

func TestRotationPendingFlushedWithoutRefresh(t *testing.T) {
	rs := newRotServer(t, "rt-fake-0", 0)
	st := &wStore{Store: memory.New()}
	rtSetup(t, st, rs.URL, "rt-fake-0")
	st.failKey, st.failSets = "rt", 1000
	src, _ := TokenSource(context.Background(), st, "g", WithHTTPClient(rs.Client()),
		WithLockDir(t.TempDir()), WithWriteBackRetry(1, time.Millisecond), WithSkew(2*time.Hour),
		WithClock(func() time.Time { return time.Now().Add(45 * time.Minute) }))
	_, _ = src.Token(context.Background())
	st.mu.Lock()
	st.failSets = 0
	st.mu.Unlock()
	// skew makes every call stale; a second Token flushes then refreshes
	// with the pending token (never the stored old one).
	if _, err := src.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rs.bad != 0 || rs.calls != 2 {
		t.Errorf("provider saw %v", rs.seen)
	}
}

func TestRotationProviderRejectsNeedsReconsent(t *testing.T) {
	rs := newRotServer(t, "rt-fake-other", 0) // stored token is not the live one
	st := &wStore{Store: memory.New()}
	rtSetup(t, st, rs.URL, "rt-fake-0")
	src, _ := TokenSource(context.Background(), st, "g", WithHTTPClient(rs.Client()), WithLockDir(t.TempDir()))
	if _, err := src.Token(context.Background()); !errors.Is(err, ErrNeedsReconsent) {
		t.Errorf("err = %v, want ErrNeedsReconsent", err)
	}
}

func TestSourceMemoryTokenRefreshedInsideSkew(t *testing.T) {
	var calls atomic.Int32
	srv := srcServer(t, 3600, &calls)
	st := memory.New()
	ccSetup(t, st, srv.URL)
	now := time.Now()
	var mu sync.Mutex
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	src, _ := TokenSource(context.Background(), st, "g", WithHTTPClient(srv.Client()), WithClock(clock), WithSkew(5*time.Minute))
	_, _ = src.Token(context.Background())
	mu.Lock()
	now = now.Add(54 * time.Minute) // 6 min left: fresh
	mu.Unlock()
	_, _ = src.Token(context.Background())
	if calls.Load() != 1 {
		t.Fatalf("refreshed with 6m left, calls %d", calls.Load())
	}
	mu.Lock()
	now = now.Add(2 * time.Minute) // 4 min left: stale
	mu.Unlock()
	_, _ = src.Token(context.Background())
	if calls.Load() != 2 {
		t.Errorf("not refreshed inside skew, calls %d", calls.Load())
	}
}
