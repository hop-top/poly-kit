package oauth

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"hop.top/kit/go/storage/secret/memory"
)

// A minted token's expiry is stamped from the injected clock, so a clock far
// from the wall clock still sees a fresh token with the full lifetime.
func TestMintedExpiryUsesInjectedClock(t *testing.T) {
	for _, offset := range []time.Duration{48 * time.Hour, -48 * time.Hour} {
		fixed := time.Now().Add(offset).Truncate(time.Second)
		clock := func() time.Time { return fixed }
		ctx := context.Background()

		var calls atomic.Int32
		srv := srcServer(t, 3600, &calls)
		st := memory.New()
		ccSetup(t, st, srv.URL)
		src, _ := TokenSource(ctx, st, "g", WithHTTPClient(srv.Client()), WithClock(clock))
		tok, err := src.Token(ctx)
		if err != nil {
			t.Fatalf("client_credentials offset %v: %v", offset, err)
		}
		checkStamp(t, "client_credentials", tok, fixed)

		rs := newRotServer(t, "rt-fake-0", 0)
		wst := &wStore{Store: memory.New()}
		rtSetup(t, wst, rs.URL, "rt-fake-0")
		rsrc, _ := TokenSource(ctx, wst, "g", WithHTTPClient(rs.Client()), WithClock(clock), WithLockDir(t.TempDir()))
		rtok, err := rsrc.Token(ctx)
		if err != nil {
			t.Fatalf("refresh_token offset %v: %v", offset, err)
		}
		checkStamp(t, "refresh_token", rtok, fixed)
	}
}

func checkStamp(t *testing.T, kind string, tok Token, fixed time.Time) {
	t.Helper()
	d := tok.ExpiresAt.Sub(fixed.Add(time.Hour))
	if d < -5*time.Second || d > 0 {
		t.Errorf("%s: ExpiresAt %v, want within 5s below clock+1h (%v)", kind, tok.ExpiresAt, fixed.Add(time.Hour))
	}
}

// TokenWithIdentity carries the stored envelope's User and AuthzID; Token
// keeps returning the bare token.
func TestTokenWithIdentity(t *testing.T) {
	ctx := context.Background()
	var calls atomic.Int32
	srv := srcServer(t, 3600, &calls)
	st := &wStore{Store: memory.New()}
	g := ccSetup(t, st, srv.URL)
	raw, err := EncodeCachedToken(CachedToken{
		Token: Token{AccessToken: "stored-at", ExpiresAt: time.Now().Add(time.Hour)},
		User:  "user@example.test", AuthzID: "authz@example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	put(t, st, TokenKey("g", g), string(raw))
	src, err := TokenSource(ctx, st, "g", WithHTTPClient(srv.Client()), WithLockDir(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // persisted read, then in-memory hit
		ct, err := src.TokenWithIdentity(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if ct.AccessToken != "stored-at" || ct.User != "user@example.test" || ct.AuthzID != "authz@example.test" {
			t.Errorf("call %d: %+v", i, ct)
		}
		if tok, err := src.Token(ctx); err != nil || tok != ct.Token {
			t.Errorf("Token %+v err %v, want %+v", tok, err, ct.Token)
		}
	}
	if calls.Load() != 0 {
		t.Errorf("minted despite fresh stored token: %d calls", calls.Load())
	}

	// Minted token: no identity, same token as Token would return.
	src.Invalidate()
	ct, err := src.TokenWithIdentity(ctx)
	if err != nil || ct.AccessToken != "at-1" || ct.User != "" || ct.AuthzID != "" {
		t.Errorf("minted: %+v err %v", ct, err)
	}
}
