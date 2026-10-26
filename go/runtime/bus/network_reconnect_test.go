package bus

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// flappingAcceptor upgrades every connection and closes it at once with
// 1000 duplicate, after hold, as an acceptor does to a second client
// keyed under a linked address. It counts upgrades.
func flappingAcceptor(t *testing.T, hold time.Duration) (string, *atomic.Int32) {
	t.Helper()
	var accepts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		accepts.Add(1)
		time.Sleep(hold)
		_ = c.Close(statusNormal, "duplicate")
	}))
	t.Cleanup(srv.Close)
	return "ws" + srv.URL[4:], &accepts
}

// A link that drops right after it comes up must not be redialed at
// once: the redial waits the backoff, and the backoff keeps growing
// across short-lived links. With the default 100 ms base, 2 s holds a
// handful of attempts; an immediate redial makes thousands.
func TestNetworkAdapter_ReconnectBacksOffAgainstAFlappingAcceptor(t *testing.T) {
	url, accepts := flappingAcceptor(t, 0)
	n := NewNetworkAdapter(New())
	n.Dial(url)
	time.Sleep(2 * time.Second)
	done := make(chan struct{})
	go func() { _ = n.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(failAfter):
		t.Fatal("Close did not stop the reconnect loop")
	}
	if got := accepts.Load(); got < 2 || got > 15 {
		t.Fatalf("%d connections in 2 s, want 2..15 (backoff from 100 ms, growing)", got)
	}
}

// The attempt count resets once a link has stayed up for the stability
// window (the maximum backoff): a healthy link that drops is redialed
// from the base delay, a flapping one keeps backing off.
func TestNetworkAdapter_ReconnectBackoffResetsAfterAStableLink(t *testing.T) {
	short, _ := flappingAcceptor(t, 0)
	long, longAccepts := flappingAcceptor(t, 150*time.Millisecond)
	n := NewNetworkAdapter(New(), WithBackoff(10*time.Millisecond, 100*time.Millisecond))
	t.Cleanup(func() { _ = n.Close() })
	n.Dial(short)
	n.Dial(long)
	deadline := time.Now().Add(failAfter)
	for longAccepts.Load() < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("stable peer reconnected %d times", longAccepts.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
	flaps := func(addr string) int {
		ps := n.peer(addr)
		ps.mu.Lock()
		defer ps.mu.Unlock()
		return ps.flaps
	}
	if f := flaps(long); f != 0 {
		t.Fatalf("a link up for 150 ms (window 100 ms) left %d flaps, want 0", f)
	}
	if f := flaps(short); f < 2 {
		t.Fatalf("a flapping link left %d flaps, want the count to grow", f)
	}
}
