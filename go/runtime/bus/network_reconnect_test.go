package bus

import (
	"context"
	"fmt"
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
// once: every redial after a drop waits the backoff first, and the
// backoff keeps growing across short-lived links. Deterministic: the
// wait seam records each wait's attempt and returns at once, and the
// stability window (the 30 s default cap) is never reached.
func TestNetworkAdapter_ReconnectBacksOffAgainstAFlappingAcceptor(t *testing.T) {
	url, accepts := flappingAcceptor(t, 0)
	const drops = 20
	waits := make(chan int, 1024)
	n := NewNetworkAdapter(New())
	n.backoffWait = func(ctx context.Context, attempt int, _ time.Duration) bool {
		select {
		case waits <- attempt:
		default: // the test has what it needs
		}
		return ctx.Err() == nil
	}
	n.Dial(url)
	var got []int
	for len(got) < drops {
		select {
		case a := <-waits:
			got = append(got, a)
		case <-time.After(failAfter):
			t.Fatalf("redials stopped after %d waits: %v", len(got), got)
		}
	}
	closeWithin(t, n)
	if got[0] != 1 {
		t.Fatalf("first redial waited at attempt %d, want 1 (the first short link)", got[0])
	}
	for i := 1; i < len(got); i++ {
		if got[i] < got[i-1] {
			t.Fatalf("backoff went back from %d to %d: %v", got[i-1], got[i], got)
		}
	}
	// Every link drops at once, so each one counts: the attempt reached
	// is at least the number of links that came up before it.
	if last, up := got[drops-1], int(accepts.Load()); last < drops/2 || up > drops+1 {
		t.Fatalf("after %d waits: attempt %d, %d links; want the attempt to track the links and no redial without a wait", drops, last, up)
	}
}

func closeWithin(t *testing.T, n *NetworkAdapter) time.Duration {
	t.Helper()
	start := time.Now()
	done := make(chan struct{})
	go func() { _ = n.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(failAfter):
		t.Fatal("Close did not stop the reconnect loop")
	}
	return time.Since(start)
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

// A link that drops before the loop that dialed it has finished must
// still be redialed: the drop's request is kept, and the loop consumes
// it before it exits. Deterministic: the loop is held at its exit while
// the link drops.
func TestNetworkAdapter_RedialPendingDuringLoopExit(t *testing.T) {
	release := make(chan struct{})
	accepted := make(chan *websocket.Conn, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		accepted <- c
		for { // read until the adapter closes, answering its close
			if _, _, err := c.Read(context.Background()); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	url := "ws" + srv.URL[4:]
	b := New()
	n := NewNetworkAdapter(b, WithBackoff(time.Millisecond, 4*time.Millisecond))
	atExit := make(chan struct{}, 4)
	var held atomic.Bool
	n.testHook = func(event string) {
		if event == "reconnect-exit" && held.CompareAndSwap(false, true) {
			atExit <- struct{}{}
			<-release
		}
	}
	events := collect(b, "kit.runtime.link.#")
	n.Dial(url)
	first := <-accepted
	events.nextTopic(t, TopicLinkConnected)
	select {
	case <-atExit: // the loop that dialed is about to exit
	case <-time.After(failAfter):
		t.Fatal("the loop never reached its exit")
	}
	_ = first.Close(statusNormal, "duplicate")
	events.nextTopic(t, TopicLinkDisconnected) // the drop was seen meanwhile
	close(release)
	select {
	case <-accepted:
	case <-time.After(failAfter):
		t.Fatal("the drop was never redialed")
	}
	closeWithin(t, n)
}

// Many adapters flapping against one acceptor all keep redialing: no
// drop is ever lost to a loop on its way out. Liveness, waited on
// through each adapter's own link events.
func TestNetworkAdapter_RedialNeverLostUnderManyFlappingPeers(t *testing.T) {
	url, _ := flappingAcceptor(t, 0)
	const peers = 40
	errs := make(chan error, peers)
	for i := 0; i < peers; i++ {
		b := New()
		n := NewNetworkAdapter(b, WithBackoff(5*time.Millisecond, 20*time.Millisecond))
		up := collect(b, string(TopicLinkConnected))
		n.Dial(url)
		go func(i int) {
			// Each adapter must link three times; it closes as soon as
			// it has, so the run stays small.
			for k := 0; k < 3; k++ {
				select {
				case <-up.ch:
				case <-time.After(10 * time.Second):
					_ = n.Close()
					errs <- fmt.Errorf("adapter %d stopped redialing after %d links", i, k)
					return
				}
			}
			_ = n.Close()
			errs <- nil
		}(i)
	}
	for i := 0; i < peers; i++ {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
}

// Close during a backoff wait returns promptly, not after the wait.
func TestNetworkAdapter_CloseDuringBackoffIsPrompt(t *testing.T) {
	url, accepts := flappingAcceptor(t, 0)
	b := New()
	n := NewNetworkAdapter(b, WithBackoff(20*time.Second, 30*time.Second))
	events := collect(b, "kit.runtime.link.#")
	n.Dial(url)
	events.nextTopic(t, TopicLinkConnected)
	events.nextTopic(t, TopicLinkDisconnected) // now in the 20 s+ wait
	if d := closeWithin(t, n); d > 500*time.Millisecond {
		t.Fatalf("close took %v during a backoff wait", d)
	}
	_ = accepts
}
