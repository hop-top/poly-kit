package bus

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// The redial state machine (see requestRedial), one test per transition
// that the other reconnect tests do not already pin. Deterministic: the
// tests hold the acceptor or the loop at the step that matters.

// gatedAcceptor upgrades a connection only once gate is closed, signals
// entered as each request arrives and accepted after each upgrade, and
// keeps every link open until the adapter closes it.
func gatedAcceptor(t *testing.T) (url string, gate chan struct{}, entered, accepted chan struct{}, accepts *atomic.Int32) {
	t.Helper()
	gate = make(chan struct{})
	entered = make(chan struct{}, 16)
	accepted = make(chan struct{}, 16)
	accepts = &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-gate
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		accepts.Add(1)
		accepted <- struct{}{}
		for {
			if _, _, err := c.Read(context.Background()); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return "ws" + srv.URL[4:], gate, entered, accepted, accepts
}

func recvWithin(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(failAfter):
		t.Fatal(what)
	}
}

// loopIdle waits until no reconnect loop runs for addr.
func loopIdle(t *testing.T, n *NetworkAdapter, addr string) {
	t.Helper()
	for deadline := time.Now().Add(failAfter); ; runtime.Gosched() {
		n.mu.RLock()
		running := n.reconnecting[addr]
		n.mu.RUnlock()
		if !running {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the reconnect loop never exited")
		}
	}
}

// idle --dial--> running (dial now): the first dial does not wait.
func TestRedialFSM_DialFromIdleDialsNow(t *testing.T) {
	url, gate, entered, accepted, _ := gatedAcceptor(t)
	close(gate)
	n := NewNetworkAdapter(New(), WithBackoff(time.Hour, time.Hour))
	var waited atomic.Bool
	n.backoffWait = func(context.Context, int, time.Duration) bool {
		if !n.closed.Load() { // a dial that close refused may wait after it
			waited.Store(true)
		}
		return false
	}
	n.Dial(url)
	recvWithin(t, entered, "no dial")
	recvWithin(t, accepted, "no link")
	loopIdle(t, n, url)
	if waited.Load() {
		t.Fatal("a dial from idle waited")
	}
	closeWithin(t, n)
}

// running (dialing) --disconnect, dial--> the new peer is served: the
// loop's in-flight dial finds its address forgotten, and the dial
// request's mark keeps the loop going. No hooks: the acceptor holds the
// upgrade while the request comes in.
func TestRedialFSM_DialAfterDisconnectDuringDialIsServed(t *testing.T) {
	url, gate, entered, accepted, accepts := gatedAcceptor(t)
	n := NewNetworkAdapter(New(), WithBackoff(time.Millisecond, 4*time.Millisecond))
	n.Dial(url)
	recvWithin(t, entered, "the loop never dialed")
	_ = n.Disconnect(url)
	n.Dial(url)
	close(gate)
	recvWithin(t, accepted, "first upgrade")
	recvWithin(t, accepted, "dial after disconnect lost: no second link")
	loopIdle(t, n, url)
	n.mu.RLock()
	up := n.conns[url] != nil
	n.mu.RUnlock()
	if !up || accepts.Load() != 2 {
		t.Fatalf("link up=%v accepts=%d, want one live link after two upgrades", up, accepts.Load())
	}
	closeWithin(t, n)
}

// running (at exit) --disconnect, dial--> the loop runs again, now, for
// the new peer (the reviewer's D1 on the exit seam).
func TestRedialFSM_DialDuringLoopExitIsServed(t *testing.T) {
	url, gate, _, accepted, _ := gatedAcceptor(t)
	close(gate)
	n := NewNetworkAdapter(New(), WithBackoff(time.Hour, time.Hour))
	atExit, release := make(chan struct{}, 1), make(chan struct{})
	var held atomic.Bool
	n.testHook = func(e string) {
		if e == "reconnect-exit" && held.CompareAndSwap(false, true) {
			atExit <- struct{}{}
			<-release
		}
	}
	var waited atomic.Bool
	n.backoffWait = func(context.Context, int, time.Duration) bool {
		if !n.closed.Load() {
			waited.Store(true)
		}
		return false
	}
	n.Dial(url)
	recvWithin(t, accepted, "first link")
	recvWithin(t, atExit, "the loop never reached its exit")
	_ = n.Disconnect(url)
	n.Dial(url)
	close(release)
	recvWithin(t, accepted, "dial after disconnect lost")
	loopIdle(t, n, url)
	if waited.Load() {
		t.Fatal("a dial request waited the backoff")
	}
	closeWithin(t, n)
}

// running (at exit, mark set) --disconnect--> the mark is cleared and
// the loop exits without dialing.
func TestRedialFSM_DisconnectClearsMarkAndStopsLoop(t *testing.T) {
	url, gate, _, accepted, accepts := gatedAcceptor(t)
	close(gate)
	n := NewNetworkAdapter(New(), WithBackoff(time.Millisecond, 4*time.Millisecond))
	atExit, release := make(chan struct{}, 1), make(chan struct{})
	var held atomic.Bool
	n.testHook = func(e string) {
		if e == "reconnect-exit" && held.CompareAndSwap(false, true) {
			atExit <- struct{}{}
			<-release
		}
	}
	n.Dial(url)
	recvWithin(t, accepted, "first link")
	recvWithin(t, atExit, "the loop never reached its exit")
	n.requestRedial(url, true) // a mark, as a drop would leave
	_ = n.Disconnect(url)
	n.mu.RLock()
	_, marked := n.pendingRedial[url]
	n.mu.RUnlock()
	if marked {
		t.Fatal("disconnect left the mark")
	}
	close(release)
	loopIdle(t, n, url)
	if a := accepts.Load(); a != 1 {
		t.Fatalf("%d links: the loop dialed a forgotten address", a)
	}
	closeWithin(t, n)
}

// running (at exit, mark set) --close--> marks are cleared, the loop
// exits, and nothing dials after close.
func TestRedialFSM_CloseClearsMarksAndNothingDials(t *testing.T) {
	url, gate, _, accepted, accepts := gatedAcceptor(t)
	close(gate)
	n := NewNetworkAdapter(New(), WithBackoff(time.Millisecond, 4*time.Millisecond))
	atExit, release := make(chan struct{}, 1), make(chan struct{})
	var held atomic.Bool
	n.testHook = func(e string) {
		if e == "reconnect-exit" && held.CompareAndSwap(false, true) {
			atExit <- struct{}{}
			<-release
		}
	}
	n.Dial(url)
	recvWithin(t, accepted, "first link")
	recvWithin(t, atExit, "the loop never reached its exit")
	n.requestRedial(url, false)
	done := make(chan struct{})
	go func() { _ = n.Close(); close(done) }()
	for deadline := time.Now().Add(failAfter); !n.closed.Load(); runtime.Gosched() {
		if time.Now().After(deadline) {
			t.Fatal("close never started")
		}
	}
	close(release)
	recvWithin(t, done, "close never returned")
	n.mu.RLock()
	marks, loops := len(n.pendingRedial), len(n.reconnecting)
	n.mu.RUnlock()
	if marks != 0 || loops != 0 || accepts.Load() != 1 {
		t.Fatalf("after close: %d marks, %d loops, %d links; want 0, 0, 1", marks, loops, accepts.Load())
	}
}
