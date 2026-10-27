package bus

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// A relay hub never sends an event back to the link it arrived on,
// whatever origin the frame claims. The raw peer here would accept its
// own frame, so only the hub's exclusion keeps it away.
func TestNetworkAdapter_Relay_NoEchoToArrivalLink(t *testing.T) {
	// Without auth there is no ack: Connect can return before the hub
	// registers the link, so wait for that before sending.
	accepted := make(chan string, 16)
	hub := NewNetworkAdapter(New(), WithOriginID("hub"), WithRelay(true), withHook(accepted))
	srv := httptest.NewServer(hub.Handler())
	defer srv.Close()
	defer hub.Close()
	url := "ws" + srv.URL[4:]

	busB := New()
	b := NewNetworkAdapter(busB, WithOriginID("B"))
	defer b.Close()
	if err := b.Connect(context.Background(), url); err != nil {
		t.Fatal(err)
	}
	waitHook(t, accepted, "accepted")
	var bGot atomic.Int32
	busB.Subscribe("relay.test.event.sent", func(context.Context, Event) error {
		bGot.Add(1)
		return nil
	})

	c := rawDial(t, url, `{"topic":"warmup.test.event.sent","source":"raw","origin":"raw-A"}`)
	rawSend(t, c, `{"topic":"relay.test.event.sent","source":"raw","origin":"spoofed"}`)

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			break
		}
		t.Fatalf("echoed to the arrival link: %s", data)
	}
	deadline := time.Now().Add(failAfter)
	for bGot.Load() != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("B got %d, want 1", bGot.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A frame without an origin must not loop around a ring of relay hubs:
// the receiving hub stamps it with the link's origin, else its own, so
// loop prevention applies. Three hubs each dial the next; the counts
// must settle (a mesh may deliver twice, a loop never stops).
func TestNetworkAdapter_Relay_EmptyOriginNoLoop(t *testing.T) {
	var (
		buses [3]Bus
		hubs  [3]*NetworkAdapter
		urls  [3]string
		n     [3]atomic.Int32
	)
	for i := range hubs {
		buses[i] = New()
		hubs[i] = NewNetworkAdapter(buses[i], WithOriginID(fmt.Sprintf("h%d", i)), WithRelay(true))
		srv := httptest.NewServer(hubs[i].Handler())
		defer srv.Close()
		defer hubs[i].Close()
		urls[i] = "ws" + srv.URL[4:]
		i := i
		buses[i].Subscribe("loop.test.event.sent", func(context.Context, Event) error { n[i].Add(1); return nil })
	}
	for i := range hubs {
		if err := hubs[i].Connect(context.Background(), urls[(i+1)%3]); err != nil {
			t.Fatal(err)
		}
	}
	c := rawDial(t, urls[0], "")
	rawSend(t, c, `{"topic":"loop.test.event.sent","source":"raw","origin":""}`)
	time.Sleep(500 * time.Millisecond)
	before := [3]int32{n[0].Load(), n[1].Load(), n[2].Load()}
	time.Sleep(300 * time.Millisecond)
	for i := range n {
		if got := n[i].Load(); got == 0 || got > 2 || got != before[i] {
			t.Fatalf("hub %d: %d deliveries (was %d 300 ms earlier), want 1 or 2 and settled", i, got, before[i])
		}
	}
}

// Two inbound connections reported under one address (a proxy-facing
// handler that rewrites RemoteAddr) must not leave a link Close cannot
// reach: the second is refused, and Close ends every reader.
func TestNetworkAdapter_Handler_DuplicateAddrRefused(t *testing.T) {
	accepted := make(chan string, 16)
	hub := NewNetworkAdapter(New(), WithOriginID("hub"), withHook(accepted))
	h := hub.Handler()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = "10.0.0.1:1"
		h.ServeHTTP(w, r)
	}))
	defer srv.Close()
	url := "ws" + srv.URL[4:]

	first := rawDial(t, url, `{"topic":"a.test.event.sent","source":"raw","origin":"one"}`)
	waitHook(t, accepted, "accepted") // the first holds the address
	second := rawDial(t, url, "")
	ctx, cancel := context.WithTimeout(context.Background(), failAfter)
	defer cancel()
	if _, _, err := second.Read(ctx); websocket.CloseStatus(err) != statusNormal {
		t.Fatalf("second link: %v, want a 1000 close", err)
	}

	done := make(chan struct{})
	go func() { _ = hub.Close(); close(done) }()
	// Reading answers the hub's close, so Close need not wait out its
	// close-handshake bound. The first link ends either way (a 1001, or
	// EOF when the canceled reader wins the race); it must end.
	if _, _, err := first.Read(ctx); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first link still open after Close: %v", err)
	}
	select {
	case <-done:
	case <-time.After(failAfter):
		t.Fatal("Close did not end every reader")
	}
}

// The filter applies to relayed events too: a hub that denies a topic
// never relays it, even though it arrived from a peer.
func TestNetworkAdapter_Relay_FilterAppliesToRelayedEvents(t *testing.T) {
	// No auth, so no ack: wait until the hub registered both links.
	accepted := make(chan string, 16)
	hub := newHub(t, WithOriginID("hub"), WithRelay(true), WithFilter(TopicFilter{Deny: []string{"secret.#"}}), withHook(accepted))
	a, b := newClient(t, WithOriginID("A")), newClient(t, WithOriginID("B"))
	connect(t, a, hub.url)
	connect(t, b, hub.url)
	waitHook(t, accepted, "accepted")
	waitHook(t, accepted, "accepted")
	var secret, public atomic.Int32
	b.bus.Subscribe("secret.#", func(context.Context, Event) error { secret.Add(1); return nil })
	b.bus.Subscribe("public.#", func(context.Context, Event) error { public.Add(1); return nil })
	_ = a.bus.Publish(context.Background(), NewEvent("secret.test.event.sent", "A", nil))
	_ = a.bus.Publish(context.Background(), NewEvent("public.test.event.sent", "A", nil))
	deadline := time.Now().Add(failAfter)
	for public.Load() != 1 {
		if time.Now().After(deadline) {
			t.Fatal("the allowed topic was not relayed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	if n := secret.Load(); n != 0 {
		t.Fatalf("a denied topic was relayed %d times", n)
	}
}

// Credentials in a dial address never reach the error callback: the
// reported error drops userinfo, query and fragment.
func TestNetworkAdapter_HandshakeErrorRedactsCredentials(t *testing.T) {
	auth := &gateAuth{}
	auth.setReject(true)
	h := newHub(t, WithAuth(auth))
	errs := make(chan error, 1)
	cl := newClient(t, WithAuth(OpenAuth()), WithNetworkErrorFunc(func(err error) { errs <- err }))
	host := h.url[len("ws://"):]
	addr := "ws://user:hunter2@" + host + "/bus?token=abc#frag"
	if err := cl.n.Connect(context.Background(), addr); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("connect err = %v", err)
	}
	select {
	case err := <-errs:
		want := "bus: handshake with ws://" + host + "/bus: bus: network auth failed"
		if err.Error() != want {
			t.Fatalf("reported %q, want %q", err, want)
		}
	case <-time.After(failAfter):
		t.Fatal("no error reported")
	}
}
