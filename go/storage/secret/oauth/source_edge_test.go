package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hop.top/kit/go/storage/secret"

	"hop.top/kit/go/storage/secret/memory"
)

// A reply that rotates the refresh token but lacks expires_in must not lose
// the rotated token: the old one is dead at the provider.
func TestRotationSurvivesIncompleteReply(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		seen = append(seen, r.PostForm.Get("refresh_token"))
		n := len(seen)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			fmt.Fprint(w, `{"access_token":"at-1","refresh_token":"rt-fake-1"}`)
			return
		}
		fmt.Fprint(w, `{"access_token":"at-2","expires_in":3600}`)
	}))
	defer srv.Close()
	st := &wStore{Store: memory.New()}
	rtSetup(t, st, srv.URL, "rt-fake-0")
	src, err := TokenSource(context.Background(), st, "g", WithHTTPClient(srv.Client()),
		WithLockDir(t.TempDir()), WithWriteBackRetry(1, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.Token(context.Background()); err == nil {
		t.Fatal("incomplete reply accepted")
	}
	if got := storedRT(t, st); got != secretEnv("rt-fake-1") {
		t.Fatalf("stored = %s, want rotated token persisted", got)
	}
	if _, err := src.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if seen[1] != "rt-fake-1" {
		t.Errorf("provider saw %v, want rotated token second", seen)
	}
}

// Same, when the store is down: the rotated token is held pending.
func TestRotationIncompleteReplyHeldPending(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		seen = append(seen, r.PostForm.Get("refresh_token"))
		n := len(seen)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			fmt.Fprint(w, `{"refresh_token":"rt-fake-1","expires_in":"soon"}`)
			return
		}
		fmt.Fprint(w, `{"access_token":"at-2","expires_in":3600}`)
	}))
	defer srv.Close()
	st := &wStore{Store: memory.New()}
	rtSetup(t, st, srv.URL, "rt-fake-0")
	st.failKey, st.failSets = "rt", 1000
	src, _ := TokenSource(context.Background(), st, "g", WithHTTPClient(srv.Client()),
		WithLockDir(t.TempDir()), WithWriteBackRetry(1, time.Millisecond))
	if _, err := src.Token(context.Background()); err == nil {
		t.Fatal("incomplete reply accepted")
	}
	st.mu.Lock()
	st.failSets = 0
	st.mu.Unlock()
	if _, err := src.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if seen[1] != "rt-fake-1" {
		t.Errorf("provider saw %v, want pending rotated token second", seen)
	}
}

// delFailStore is a writable store whose Delete always fails.
type delFailStore struct{ *wStore }

func (s delFailStore) Delete(context.Context, string) error { return errors.New("store down") }

// A failed store Delete in Invalidate must not leave the rejected token
// served from the persisted copy.
func TestInvalidateWithFailingDeleteDoesNotServeRejected(t *testing.T) {
	var calls atomic.Int32
	srv := srcServer(t, 3600, &calls)
	st := delFailStore{&wStore{Store: memory.New()}}
	ccSetup(t, st, srv.URL)
	ctx := context.Background()
	src, _ := TokenSource(ctx, st, "g", WithHTTPClient(srv.Client()), WithLockDir(t.TempDir()))
	first, err := src.Token(ctx)
	if err != nil {
		t.Fatal(err)
	}
	src.Invalidate()
	second, err := src.Token(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second.AccessToken == first.AccessToken || calls.Load() != 2 {
		t.Errorf("rejected token served again: %q calls %d", second.AccessToken, calls.Load())
	}
}

// A refresh in flight during Invalidate must not write its token back.
func TestInvalidateDuringRefreshSkipsPersist(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			close(started)
			<-release
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":"at-%d","expires_in":3600}`, n)
	}))
	defer srv.Close()
	st := &wStore{Store: memory.New()}
	g := ccSetup(t, st, srv.URL)
	ctx := context.Background()
	src, _ := TokenSource(ctx, st, "g", WithHTTPClient(srv.Client()), WithLockDir(t.TempDir()))
	done := make(chan struct{})
	go func() { defer close(done); _, _ = src.Token(ctx) }()
	<-started
	src.Invalidate()
	close(release)
	<-done
	if ok, _ := st.Exists(ctx, TokenKey("g", g)); ok {
		t.Error("token minted before Invalidate was persisted")
	}
	if tok, err := src.Token(ctx); err != nil || tok.AccessToken != "at-2" {
		t.Errorf("after Invalidate: %+v err %v, want a new mint", tok, err)
	}
}

// A token whose lifetime is shorter than the skew must be cached for part of
// its life, not re-minted on every call.
func TestShortLivedTokenCachedDespiteSkew(t *testing.T) {
	for _, writable := range []bool{false, true} {
		var calls atomic.Int32
		srv := srcServer(t, 60, &calls)
		var st secret.MutableStore = memory.New()
		if writable {
			st = &wStore{Store: memory.New()}
		}
		ccSetup(t, st, srv.URL)
		ctx := context.Background()
		var mu sync.Mutex
		off := time.Duration(0)
		clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return time.Now().Add(off) }
		opts := []Option{WithHTTPClient(srv.Client()), WithLockDir(t.TempDir()), WithClock(clock), WithSkew(5 * time.Minute)}
		a, _ := TokenSource(ctx, st, "g", opts...)
		for i := 0; i < 10; i++ {
			if _, err := a.Token(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if writable {
			b, _ := TokenSource(ctx, st, "g", opts...) // sibling reads the persisted token
			if _, err := b.Token(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if n := calls.Load(); n != 1 {
			t.Errorf("writable=%v: mints = %d, want 1", writable, n)
		}
		mu.Lock()
		off = 20 * time.Second // lifetime/2 = 30s not reached
		mu.Unlock()
		_, _ = a.Token(ctx)
		if n := calls.Load(); n != 1 {
			t.Errorf("writable=%v: re-minted at 20s of 60s, mints = %d", writable, n)
		}
		mu.Lock()
		off = 40 * time.Second // inside the halved skew
		mu.Unlock()
		_, _ = a.Token(ctx)
		if n := calls.Load(); n != 2 {
			t.Errorf("writable=%v: not re-minted at 40s of 60s, mints = %d", writable, n)
		}
	}
}

// A rotated refresh token that could not be written back must be used by
// every Source of the same ref in the process, not only the one that got it.
func TestPendingRotationSharedBetweenSources(t *testing.T) {
	rs := newRotServer(t, "rt-fake-0", 0)
	st := &wStore{Store: memory.New()}
	rtSetup(t, st, rs.URL, "rt-fake-0")
	st.failKey, st.failSets = "rt", 1000
	ctx := context.Background()
	opts := []Option{WithHTTPClient(rs.Client()), WithLockDir(t.TempDir()), WithWriteBackRetry(1, time.Millisecond)}
	a, _ := TokenSource(ctx, st, "g", opts...)
	b, _ := TokenSource(ctx, st, "g", opts...)
	if _, err := a.Token(ctx); err != nil {
		t.Fatal(err)
	}
	b.Invalidate() // drop the token A persisted so B must refresh
	if _, err := b.Token(ctx); err != nil {
		t.Fatalf("sibling Source: %v", err)
	}
	if rs.bad != 0 || len(rs.seen) != 2 || rs.seen[1] != "rt-fake-1" {
		t.Errorf("provider saw %v, want the pending rotated token from the sibling", rs.seen)
	}
}

// After a fresh consent replaces the stored refresh token, a stale pending
// one must not be used instead.
func TestPendingRotationDroppedAfterReconsent(t *testing.T) {
	rs := newRotServer(t, "rt-fake-0", 0)
	st := &wStore{Store: memory.New()}
	rtSetup(t, st, rs.URL, "rt-fake-0")
	st.failKey, st.failSets = "rt", 1000
	ctx := context.Background()
	opts := []Option{WithHTTPClient(rs.Client()), WithLockDir(t.TempDir()), WithWriteBackRetry(1, time.Millisecond)}
	a, _ := TokenSource(ctx, st, "g", opts...)
	if _, err := a.Token(ctx); err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	st.failSets = 0
	st.mu.Unlock()
	rs.mu.Lock()
	rs.cur = "rt-fake-new"
	rs.mu.Unlock()
	put(t, st, "rt", secretEnv("rt-fake-new")) // new consent
	b, _ := TokenSource(ctx, st, "g", opts...)
	b.Invalidate()
	if _, err := b.Token(ctx); err != nil {
		t.Fatal(err)
	}
	if rs.seen[len(rs.seen)-1] != "rt-fake-new" {
		t.Errorf("provider saw %v, want the re-consented token last", rs.seen)
	}
}

// Two stores in one process (two tenants) with the same ref and grant must
// never share a pending rotated token.
func TestPendingRotationNotSharedAcrossStores(t *testing.T) {
	rs := newRotServer(t, "rt-fake-0", 0)
	stA := &wStore{Store: memory.New()}
	stB := &wStore{Store: memory.New()}
	rtSetup(t, stA, rs.URL, "rt-fake-0")
	rtSetup(t, stB, rs.URL, "rt-fake-0")
	stA.failKey, stA.failSets = "rt", 1000
	ctx := context.Background()
	opts := []Option{WithHTTPClient(rs.Client()), WithLockDir(t.TempDir()), WithWriteBackRetry(1, time.Millisecond)}
	a, _ := TokenSource(ctx, stA, "g", opts...)
	b, _ := TokenSource(ctx, stB, "g", opts...)
	if _, err := a.Token(ctx); err != nil {
		t.Fatal(err)
	}
	_, _ = b.Token(ctx)
	for _, got := range rs.seen[1:] {
		if got == "rt-fake-1" {
			t.Fatalf("store B sent store A's pending token: %v", rs.seen)
		}
	}
}

// getFailStore fails reads of key while fail is set.
type getFailStore struct {
	*wStore
	key  string
	fail atomic.Bool
}

func (s *getFailStore) Get(ctx context.Context, k string) (*secret.Secret, error) {
	if k == s.key && s.fail.Load() {
		return nil, errors.New("backend down token=hunter2")
	}
	return s.wStore.Get(ctx, k)
}

// If the stored refresh token cannot be read, a Source that did not receive
// the pending token fails closed; the receiving Source may still use it.
func TestPendingRotationFailsClosed(t *testing.T) {
	rs := newRotServer(t, "rt-fake-0", 0)
	st := &getFailStore{wStore: &wStore{Store: memory.New()}, key: "rt"}
	rtSetup(t, st, rs.URL, "rt-fake-0")
	st.failKey, st.failSets = "rt", 1000
	ctx := context.Background()
	opts := []Option{WithHTTPClient(rs.Client()), WithLockDir(t.TempDir()), WithWriteBackRetry(1, time.Millisecond)}
	a, _ := TokenSource(ctx, st, "g", opts...)
	b, _ := TokenSource(ctx, st, "g", opts...)
	if _, err := a.Token(ctx); err != nil {
		t.Fatal(err)
	}
	st.fail.Store(true)
	b.Invalidate()
	_, err := b.Token(ctx)
	if !errors.Is(err, ErrTransient) || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("sibling err = %v, want ErrTransient without backend text", err)
	}
	if rs.calls != 1 {
		t.Fatalf("pending token sent by a non-owner: provider saw %v", rs.seen)
	}
	a.Invalidate()
	if _, err := a.Token(ctx); err != nil {
		t.Fatalf("owner recovery: %v", err)
	}
	if rs.seen[1] != "rt-fake-1" {
		t.Errorf("owner sent %v, want its pending token", rs.seen)
	}
}
