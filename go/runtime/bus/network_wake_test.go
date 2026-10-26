package bus

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Wakes of a reconnect loop's backoff wait (see requestRedial). The
// first link flaps, so the loop enters a backoff wait; the wait seam
// holds it until something wakes it.

// flapFirst upgrades every connection, closes the first one at once and
// keeps the rest open. It signals each upgrade.
func flapFirst(t *testing.T) (string, chan struct{}, *atomic.Int32) {
	t.Helper()
	accepted := make(chan struct{}, 16)
	var accepts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		first := accepts.Add(1) == 1
		accepted <- struct{}{}
		if first {
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
	return "ws" + srv.URL[4:], accepted, &accepts
}

// heldInBackoff dials url and returns once the redial loop sits in a
// backoff wait the seam holds.
func heldInBackoff(t *testing.T, url string, accepted chan struct{}) *NetworkAdapter {
	t.Helper()
	n := NewNetworkAdapter(New(), WithBackoff(time.Hour, time.Hour))
	waiting := make(chan struct{}, 16)
	n.backoffWait = func(context.Context, int, time.Duration) bool {
		waiting <- struct{}{}
		return false // hold until woken or canceled
	}
	n.Dial(url)
	recvWithin(t, accepted, "first link")
	recvWithin(t, waiting, "the redial never entered its backoff wait")
	return n
}

// backoff-wait --Disconnect, Dial--> woken, dials now (the reviewer's
// TestK12RR4_DialDuringBackoffWaitIsServedNow).
func TestNetworkAdapter_DialDuringBackoffWaitIsServedNow(t *testing.T) {
	url, accepted, _ := flapFirst(t)
	n := heldInBackoff(t, url, accepted)
	_ = n.Disconnect(url)
	n.Dial(url)
	recvWithin(t, accepted, "dial during the backoff wait not served now")
	closeWithin(t, n)
}

// backoff-wait --Dial (address still registered)--> woken, dials now.
func TestNetworkAdapter_DialOfABackingOffAddressIsServedNow(t *testing.T) {
	url, accepted, _ := flapFirst(t)
	n := heldInBackoff(t, url, accepted)
	n.Dial(url)
	recvWithin(t, accepted, "dial of a backing-off address not served now")
	closeWithin(t, n)
}

// backoff-wait --Connect--> woken; the loop's own dial finds the link.
func TestNetworkAdapter_ConnectDuringBackoffWaitWakesTheLoop(t *testing.T) {
	url, accepted, accepts := flapFirst(t)
	n := heldInBackoff(t, url, accepted)
	if err := n.Connect(context.Background(), url); err != nil {
		t.Fatalf("connect: %v", err)
	}
	recvWithin(t, accepted, "connect")
	loopIdle(t, n, url)
	if a := accepts.Load(); a > 3 {
		t.Fatalf("%d links: a woken loop must not add links", a)
	}
	closeWithin(t, n)
}

// backoff-wait --Disconnect--> woken, the loop exits, nothing dials.
func TestNetworkAdapter_DisconnectDuringBackoffWaitExits(t *testing.T) {
	url, accepted, accepts := flapFirst(t)
	n := heldInBackoff(t, url, accepted)
	_ = n.Disconnect(url)
	loopIdle(t, n, url)
	if a := accepts.Load(); a != 1 {
		t.Fatalf("%d links after disconnect, want 1", a)
	}
	closeWithin(t, n)
}

// backoff-wait --Close--> canceled, nothing dials.
func TestNetworkAdapter_CloseDuringBackoffWaitDialsNothing(t *testing.T) {
	url, accepted, accepts := flapFirst(t)
	n := heldInBackoff(t, url, accepted)
	closeWithin(t, n)
	n.mu.RLock()
	loops := len(n.reconnecting)
	n.mu.RUnlock()
	if a := accepts.Load(); a != 1 || loops != 0 {
		t.Fatalf("after close: %d links, %d loops; want 1, 0", a, loops)
	}
}

// backoff-wait --automatic redial (a drop)--> not woken: a drop honors
// the full backoff.
func TestNetworkAdapter_AutoRedialDuringBackoffWaitIsNotWoken(t *testing.T) {
	url, accepted, accepts := flapFirst(t)
	n := heldInBackoff(t, url, accepted)
	before := n.wakesSent.Load()
	n.requestRedial(url, true)
	if got := n.wakesSent.Load(); got != before {
		t.Fatalf("an automatic redial woke the wait (%d wakes)", got-before)
	}
	n.mu.RLock()
	waiting := n.reconnecting[url]
	n.mu.RUnlock()
	if !waiting || accepts.Load() != 1 {
		t.Fatalf("loop running=%v, %d links; want still waiting, 1 link", waiting, accepts.Load())
	}
	closeWithin(t, n)
}

// The same in real time: with a 60 s backoff, a Dial after Disconnect
// during the wait links within 2 s.
func TestNetworkAdapter_DialDuringA60sBackoffLinksWithin2s(t *testing.T) {
	url, accepted, _ := flapFirst(t)
	b := New()
	n := NewNetworkAdapter(b, WithBackoff(60*time.Second, 60*time.Second))
	events := collect(b, "kit.runtime.link.#")
	n.Dial(url)
	recvWithin(t, accepted, "first link")
	events.nextTopic(t, TopicLinkConnected)
	events.nextTopic(t, TopicLinkDisconnected)
	loopWaiting := func() bool {
		n.mu.RLock()
		defer n.mu.RUnlock()
		return n.reconnecting[url]
	}
	for deadline := time.Now().Add(failAfter); !loopWaiting(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no redial loop")
		}
	}
	start := time.Now()
	_ = n.Disconnect(url)
	n.Dial(url)
	select {
	case <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("not linked within 2 s of the dial")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("linked after %v", d)
	}
	closeWithin(t, n)
}
