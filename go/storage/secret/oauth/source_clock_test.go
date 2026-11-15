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
