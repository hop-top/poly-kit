package bus

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
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
	var total atomic.Int32
	n := NewNetworkAdapter(New())
	n.backoffWait = func(ctx context.Context, attempt int, _ time.Duration) bool {
		total.Add(1)
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
	// No wait restarts from scratch while the links stay short (a failed
	// dial inside a loop may raise one wait above the next, so the order
	// is not asserted).
	for _, a := range got {
		if a < 1 {
			t.Fatalf("a wait restarted: %v", got)
		}
	}
	// Every link drops at once, so each one counts: the attempt reached
	// is at least the number of links that came up before it.
	// Every link but the first follows a wait: no redial without one.
	if up, w := accepts.Load(), total.Load(); up > w+1 {
		t.Fatalf("%d links after %d waits: a redial skipped its wait", up, w)
	}
	if last := got[drops-1]; last < drops/2 {
		t.Fatalf("after %d waits the attempt is %d; want it to track the short links", drops, last)
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
	// A cap (and so a stability window) of an hour: the first link
	// counts as short however slow the machine; the wait seam keeps the
	// test fast.
	n := NewNetworkAdapter(b, WithBackoff(time.Millisecond, time.Hour))
	atExit := make(chan struct{}, 4)
	var held atomic.Bool
	n.testHook = func(event string) {
		if event == "reconnect-exit" && held.CompareAndSwap(false, true) {
			atExit <- struct{}{}
			<-release
		}
	}
	var waits, minAttempt atomic.Int32
	minAttempt.Store(1 << 30)
	n.backoffWait = func(ctx context.Context, attempt int, _ time.Duration) bool {
		waits.Add(1)
		for {
			m := minAttempt.Load()
			if int32(attempt) >= m || minAttempt.CompareAndSwap(m, int32(attempt)) {
				break
			}
		}
		return ctx.Err() == nil
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
	n.mu.RLock()
	l := n.conns[url]
	n.mu.RUnlock()
	if l == nil {
		t.Fatal("no link")
	}
	_ = first.Close(statusNormal, "duplicate")
	events.nextTopic(t, TopicLinkDisconnected) // the drop was seen meanwhile
	// The reader closes done after it asked for the redial: the request
	// met the held loop.
	select {
	case <-l.done:
	case <-time.After(failAfter):
		t.Fatal("the reader never finished")
	}
	close(release)
	select {
	case <-accepted:
	case <-time.After(failAfter):
		t.Fatal("the drop was never redialed")
	}
	// The pending run waited its backoff before that dial.
	if w := waits.Load(); w < 1 {
		t.Fatalf("the pending run dialed without waiting (%d waits)", w)
	}
	if a := minAttempt.Load(); a < 1 {
		t.Fatalf("the pending run waited at attempt %d, want >= 1", a)
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

// Close during a backoff wait returns promptly, not after the wait. The
// test closes only once the dropped link's reader has asked for the
// redial, so the loop is in its wait.
func TestNetworkAdapter_CloseDuringBackoffIsPrompt(t *testing.T) {
	dropFirst := make(chan struct{})
	accepted := make(chan struct{}, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		accepted <- struct{}{}
		<-dropFirst
		_ = c.Close(statusNormal, "duplicate")
	}))
	t.Cleanup(srv.Close)
	url := "ws" + srv.URL[4:]
	n := NewNetworkAdapter(New(), WithBackoff(20*time.Second, 30*time.Second))
	n.Dial(url)
	<-accepted
	var l *link
	for deadline := time.Now().Add(failAfter); l == nil; {
		if time.Now().After(deadline) {
			t.Fatal("the link never registered")
		}
		n.mu.RLock()
		l = n.conns[url]
		n.mu.RUnlock()
		runtime.Gosched()
	}
	close(dropFirst)
	select {
	case <-l.done:
	case <-time.After(failAfter):
		t.Fatal("the reader never finished")
	}
	n.mu.RLock()
	waiting := n.reconnecting[url]
	n.mu.RUnlock()
	if !waiting {
		t.Fatal("the redial is not waiting")
	}
	if d := closeWithin(t, n); d > 500*time.Millisecond {
		t.Fatalf("close took %v during a backoff wait", d)
	}
}

// A link that drops after the loop that dialed it has exited starts a
// new loop, and that loop waits the backoff before it dials. The test
// drops the link only once no loop runs, then checks the order.
func TestNetworkAdapter_RedialAfterDropWaitsFirst(t *testing.T) {
	var (
		mu  sync.Mutex
		log []string
	)
	note := func(s string) { mu.Lock(); log = append(log, s); mu.Unlock() }
	dropFirst := make(chan struct{})
	accepted := make(chan struct{}, 4)
	var first atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		note("accept")
		accepted <- struct{}{}
		if first.CompareAndSwap(false, true) {
			<-dropFirst
			_ = c.Close(statusNormal, "duplicate")
			return
		}
		for {
			if _, _, err := c.Read(context.Background()); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	url := "ws" + srv.URL[4:]
	n := NewNetworkAdapter(New())
	n.backoffWait = func(ctx context.Context, _ int, _ time.Duration) bool {
		note("wait")
		return ctx.Err() == nil
	}
	n.Dial(url)
	<-accepted
	for deadline := time.Now().Add(failAfter); ; runtime.Gosched() {
		n.mu.RLock()
		idle := !n.reconnecting[url] && n.conns[url] != nil
		n.mu.RUnlock()
		if idle {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the dialing loop never exited")
		}
	}
	close(dropFirst)
	select {
	case <-accepted:
	case <-time.After(failAfter):
		t.Fatal("the drop was never redialed")
	}
	mu.Lock()
	got := append([]string(nil), log...)
	mu.Unlock()
	if len(got) < 3 || got[1] != "wait" {
		t.Fatalf("order %v, want accept, wait, accept: the redial waits first", got)
	}
	closeWithin(t, n)
}
