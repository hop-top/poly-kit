package bus

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// reconnectHeld drops the client's link and holds the reconnect at the
// hub's auth gate until publishWhileDown has run.
func reconnectHeld(t *testing.T, auth *gateAuth, cl node, url string, events *collector, publishWhileDown func()) got {
	t.Helper()
	auth.arm()
	dropLink(t, cl.n, url)
	events.nextTopic(t, TopicLinkDisconnected)
	publishWhileDown()
	auth.release()
	return events.nextTopic(t, TopicLinkConnected)
}

func TestResume_ReplaysMissedInOrder(t *testing.T) {
	auth := &gateAuth{}
	h := newHub(t, WithAuth(auth))
	cl := newClient(t, WithAuth(OpenAuth()), WithBackoff(time.Millisecond, time.Millisecond))
	events := collect(cl.bus, "#")
	connect(t, cl, h.url)
	events.nextTopic(t, TopicLinkConnected)
	publish(t, h.bus, "acme.crm.deal.created", "")
	publish(t, h.bus, "acme.crm.deal.updated", "")
	events.nextTopic(t, "acme.crm.deal.created")
	events.nextTopic(t, "acme.crm.deal.updated")

	g := reconnectHeld(t, auth, cl, h.url, events, func() {
		publish(t, h.bus, "acme.crm.deal.won", "")
		publish(t, h.bus, "acme.crm.deal.lost", "")
	})
	if field(g, "resume") != ResumeReplaying {
		t.Fatalf("connected = %v", g.Payload)
	}
	publish(t, h.bus, "acme.crm.deal.closed", "")
	for i, want := range []Topic{"acme.crm.deal.won", "acme.crm.deal.lost", "acme.crm.deal.closed"} {
		if g := events.nextTopic(t, want); g.pos.Offset != uint64(i+3) {
			t.Fatalf("%s at offset %d", want, g.pos.Offset)
		}
	}
	events.none(t)
	if p, _ := cl.n.Position(h.url); p.Offset != 5 || p.Stream != h.n.log.ID() {
		t.Fatalf("position = %+v", p)
	}
}

func TestResume_OutOfRangeSendsReset(t *testing.T) {
	auth := &gateAuth{}
	h := newHub(t, WithAuth(auth), WithStreamLog(2))
	cl := newClient(t, WithAuth(OpenAuth()), WithBackoff(time.Millisecond, time.Millisecond))
	events := collect(cl.bus, "#")
	connect(t, cl, h.url)
	events.nextTopic(t, TopicLinkConnected)
	publish(t, h.bus, "acme.crm.deal.created", "")
	events.nextTopic(t, "acme.crm.deal.created")

	g := reconnectHeld(t, auth, cl, h.url, events, func() {
		for i := 0; i < 5; i++ {
			publish(t, h.bus, "acme.crm.deal.updated", "")
		}
	})
	if field(g, "resume") != ResumeReset {
		t.Fatalf("connected = %v", g.Payload)
	}
	inv := events.nextTopic(t, TopicStreamInvalidated)
	if field(inv, "reason") != ReasonEvicted || field(inv, "after") != "1" || field(inv, "head") != "6" || field(inv, "peer") != h.url {
		t.Fatalf("invalidated = %v", inv.Payload)
	}
	publish(t, h.bus, "acme.crm.deal.closed", "")
	events.nextTopic(t, "acme.crm.deal.closed")
	events.none(t)
}

func TestResume_AheadResetMovesPositionToHead(t *testing.T) {
	h := newHub(t, WithAuth(OpenAuth()))
	for i := 0; i < 3; i++ {
		publish(t, h.bus, "acme.crm.deal.created", "")
	}
	cl := newClient(t, WithAuth(OpenAuth()), WithResumeFrom(h.url, StreamPosition{Stream: h.n.log.ID(), Offset: 99}))
	events := collect(cl.bus, "#")
	connect(t, cl, h.url)
	events.nextTopic(t, TopicLinkConnected)
	if g := events.nextTopic(t, TopicStreamInvalidated); field(g, "reason") != ReasonAhead {
		t.Fatalf("invalidated = %v", g.Payload)
	}
	publish(t, h.bus, "acme.crm.deal.updated", "")
	if g := events.nextTopic(t, "acme.crm.deal.updated"); g.pos.Offset != 4 {
		t.Fatalf("offset = %d", g.pos.Offset)
	}
}

func TestResume_StreamRestartSendsReset(t *testing.T) {
	h := newHub(t, WithAuth(OpenAuth()))
	cl := newClient(t, WithAuth(OpenAuth()), WithResumeFrom(h.url, StreamPosition{Stream: "stm_gone", Offset: 4}))
	events := collect(cl.bus, "#")
	connect(t, cl, h.url)
	if g := events.nextTopic(t, TopicLinkConnected); field(g, "resume") != ResumeReset {
		t.Fatalf("connected = %v", g.Payload)
	}
	if g := events.nextTopic(t, TopicStreamInvalidated); field(g, "reason") != ReasonStream || field(g, "stream") != h.n.log.ID() {
		t.Fatalf("invalidated = %v", g.Payload)
	}
}

func TestResume_FirstConnectToLegacyWithPositionInvalidates(t *testing.T) {
	h := newHub(t) // no auth: no handshake
	seeded := newClient(t, WithResumeFrom(h.url, StreamPosition{Stream: "stm_x", Offset: 7}))
	events := collect(seeded.bus, "#")
	connect(t, seeded, h.url)
	if g := events.nextTopic(t, TopicLinkConnected); field(g, "resume") != ResumeUnsupported {
		t.Fatalf("connected = %v", g.Payload)
	}
	if g := events.nextTopic(t, TopicStreamInvalidated); field(g, "reason") != ReasonUnsupported || field(g, "stream") != "" {
		t.Fatalf("invalidated = %v", g.Payload)
	}
	if _, held := seeded.n.Position(h.url); held {
		t.Fatal("a legacy link must clear the position")
	}
}

func TestResume_ReconnectToLegacyInvalidates(t *testing.T) {
	h := newHub(t)
	cl := newClient(t, WithBackoff(time.Millisecond, time.Millisecond))
	events := collect(cl.bus, "#")
	connect(t, cl, h.url)
	events.nextTopic(t, TopicLinkConnected)
	dropLink(t, cl.n, h.url)
	events.nextTopic(t, TopicLinkDisconnected)
	events.nextTopic(t, TopicLinkConnected)
	if g := events.nextTopic(t, TopicStreamInvalidated); field(g, "reason") != ReasonUnsupported {
		t.Fatalf("invalidated = %v", g.Payload)
	}
}

func TestStream_HeartbeatKeepsQuietSubscriptionFromResetting(t *testing.T) {
	auth := &gateAuth{}
	clock := newManualClock()
	hook := make(chan string, 16)
	drained := make(chan string, 64)
	h := newHub(t, WithAuth(auth), WithStreamLog(4), withClock(clock), withHook(drained))
	cl := newClient(t, WithAuth(OpenAuth()), WithSubscribe("acme.quiet.#"), withHook(hook),
		withClock(newManualClock()), WithBackoff(time.Millisecond, time.Millisecond))
	events := collect(cl.bus, "#")
	connect(t, cl, h.url)
	events.nextTopic(t, TopicLinkConnected)
	for i := 1; i <= 10; i++ {
		// One at a time, so the writer keeps up and never lags.
		publish(t, h.bus, "acme.busy.deal.created", "")
		waitHook(t, drained, "drained:"+formatOffset(uint64(i)))
	}
	clock.tick(t)
	waitHook(t, hook, "position")
	if p, _ := cl.n.Position(h.url); p.Offset != 10 {
		t.Fatalf("position = %+v, want the hub's head", p)
	}
	g := reconnectHeld(t, auth, cl, h.url, events, func() {})
	if field(g, "resume") != ResumeReplaying {
		t.Fatalf("connected = %v, want a replay, not a reset", g.Payload)
	}
	events.none(t)
}

func TestStream_DeadLinkDetectedAfterThreeSilentTicks(t *testing.T) {
	h := newHub(t, WithAuth(OpenAuth()), withClock(newManualClock())) // never ticks
	clock := newManualClock()
	cl := newClient(t, WithAuth(OpenAuth()), withClock(clock), WithBackoff(time.Hour, time.Hour))
	events := collect(cl.bus, "#")
	connect(t, cl, h.url)
	events.nextTopic(t, TopicLinkConnected)
	clock.tick(t)
	clock.tick(t)
	events.none(t)
	clock.tick(t)
	g := events.nextTopic(t, TopicLinkDisconnected)
	if field(g, "code") != "timeout" || field(g, "final") != false {
		t.Fatalf("disconnected = %v", g.Payload)
	}
}

func TestStream_LaggedLinkGetsReset(t *testing.T) {
	block := make(chan struct{})
	blocked := make(chan struct{}, 1)
	first := true
	h := newHub(t, WithAuth(OpenAuth()), WithStreamLog(2), WithLinkFilter(func(_ Peer, e Event) bool {
		if first && e.Topic == "acme.crm.deal.created" {
			first = false
			blocked <- struct{}{}
			<-block // the writer stalls here
		}
		return true
	}))
	cl := newClient(t, WithAuth(OpenAuth()))
	events := collect(cl.bus, "#")
	connect(t, cl, h.url)
	events.nextTopic(t, TopicLinkConnected)
	publish(t, h.bus, "acme.crm.deal.created", "")
	<-blocked
	for i := 0; i < 5; i++ {
		publish(t, h.bus, "acme.crm.deal.updated", "")
	}
	close(block)
	events.nextTopic(t, "acme.crm.deal.created")
	g := events.nextTopic(t, TopicStreamInvalidated)
	if field(g, "reason") != ReasonLagged || field(g, "after") != "1" || field(g, "head") != "6" {
		t.Fatalf("invalidated = %v", g.Payload)
	}
	publish(t, h.bus, "acme.crm.deal.closed", "")
	events.nextTopic(t, "acme.crm.deal.closed")
	if st, _ := cl.n.LinkState(h.url); !st.Connected {
		t.Fatal("a lagged link must stay up")
	}
}

func TestLegacy_SkipsAreCounted(t *testing.T) {
	block := make(chan struct{})
	blocked := make(chan struct{}, 1)
	skips := make(chan uint64, 1)
	first := true
	accepted := make(chan string, 1)
	h := newHub(t, WithStreamLog(2), withHook(accepted),
		WithSkipFunc(func(_ string, n uint64) { skips <- n }),
		WithLinkFilter(func(_ Peer, e Event) bool {
			if first {
				first = false
				blocked <- struct{}{}
				<-block
			}
			return true
		}))
	c := rawDial(t, h.url, "")
	<-accepted
	publish(t, h.bus, "acme.crm.deal.created", "")
	<-blocked
	for i := 0; i < 5; i++ {
		publish(t, h.bus, "acme.crm.deal.updated", "")
	}
	close(block)
	rawJSON(t, c)
	if n := <-skips; n != 3 {
		t.Fatalf("skipped %d, want 3", n)
	}
	h.n.mu.RLock()
	var total uint64
	for _, s := range h.n.skipped {
		total += s.Load()
	}
	h.n.mu.RUnlock()
	if total != 3 {
		t.Fatalf("Skipped total = %d", total)
	}
}

func TestRelay_PreservesOffsetsPerHub(t *testing.T) {
	a := newHub(t, WithAuth(OpenAuth()), WithOriginID("A"))
	b := newHub(t, WithAuth(OpenAuth()), WithOriginID("B"), WithRelay(true))
	c := newClient(t, WithAuth(OpenAuth()), WithOriginID("C"))
	events := collect(c.bus, "acme.#")
	connect(t, c, b.url)
	publish(t, b.bus, "acme.crm.deal.created", "") // B offset 1
	connect(t, b, a.url)
	publish(t, a.bus, "acme.crm.deal.updated", "") // A offset 1, B offset 2
	publish(t, a.bus, "acme.crm.deal.won", "")     // A offset 2, B offset 3
	for i, want := range []Topic{"acme.crm.deal.created", "acme.crm.deal.updated", "acme.crm.deal.won"} {
		g := events.nextTopic(t, want)
		if g.pos.Stream != b.n.log.ID() || g.pos.Offset != uint64(i+1) {
			t.Fatalf("%s at %+v, want B's stream offset %d", want, g.pos, i+1)
		}
	}
}

func TestRelay_UpstreamResetBypassesFilters(t *testing.T) {
	a := newHub(t, WithAuth(OpenAuth()))
	publish(t, a.bus, "acme.crm.deal.created", "")
	b := newHub(t, WithAuth(OpenAuth()), WithRelay(true), WithFilter(TopicFilter{Allow: []string{"acme.#"}}),
		WithResumeFrom(a.url, StreamPosition{Stream: a.n.log.ID(), Offset: 99}))
	c := newClient(t, WithAuth(OpenAuth()), WithSubscribe("acme.crm.#"))
	events := collect(c.bus, "#")
	connect(t, c, b.url)
	events.nextTopic(t, TopicLinkConnected)
	connect(t, b, a.url) // ahead: B invalidates and logs a gap marker
	g := events.nextTopic(t, TopicStreamInvalidated)
	if field(g, "reason") != ReasonUpstream || field(g, "peer") != b.url {
		t.Fatalf("invalidated = %v", g.Payload)
	}
	if p, _ := c.n.Position(b.url); p.Offset != 1 {
		t.Fatalf("position = %+v, want the marker's offset", p)
	}
}

func TestStream_ReservedTopicsDroppedInbound(t *testing.T) {
	h := newHub(t, WithAuth(OpenAuth()))
	events := collect(h.bus, "#")
	c := rawDial(t, h.url, capsFrame)
	rawRead(t, c)
	rawSend(t, c, `{"topic":"kit.runtime.stream.invalidated","source":"x","payload":{},"origin":"raw","offset":"1"}`)
	rawSend(t, c, `{"topic":"kit.runtime.link.connected","source":"x","payload":{},"origin":"raw","offset":"2"}`)
	rawSend(t, c, `{"topic":"acme.crm.deal.created","source":"x","payload":{},"origin":"raw","offset":"3"}`)
	events.nextTopic(t, "acme.crm.deal.created")
}

func TestStream_MissingOffsetIsAProtocolError(t *testing.T) {
	h := newHub(t, WithAuth(OpenAuth()))
	c := rawDial(t, h.url, capsFrame)
	rawRead(t, c)
	rawSend(t, c, `{"topic":"acme.crm.deal.created","source":"x","payload":{},"origin":"raw"}`)
	ctx, cancel := context.WithTimeout(context.Background(), failAfter)
	defer cancel()
	if _, _, err := c.Read(ctx); err == nil || !isCloseStatus(err, statusProtocol) {
		t.Fatalf("read err = %v, want close 1002", err)
	}
}

func TestStream_DisconnectIsFinal(t *testing.T) {
	h := newHub(t, WithAuth(OpenAuth()))
	cl := newClient(t, WithAuth(OpenAuth()))
	events := collect(cl.bus, "#")
	connect(t, cl, h.url)
	events.nextTopic(t, TopicLinkConnected)
	if err := cl.n.Disconnect(h.url); err != nil {
		t.Fatal(err)
	}
	if g := events.nextTopic(t, TopicLinkDisconnected); field(g, "final") != true {
		t.Fatalf("disconnected = %v", g.Payload)
	}
}

func TestOutbox_FlushesInOrderAfterReconnect(t *testing.T) {
	auth := &gateAuth{}
	h := newHub(t, WithAuth(auth))
	hubEvents := collect(h.bus, "acme.#")
	cl := newClient(t, WithAuth(OpenAuth()), WithOutbox(), WithBackoff(time.Millisecond, time.Millisecond))
	events := collect(cl.bus, "kit.runtime.link.#")
	connect(t, cl, h.url)
	events.nextTopic(t, TopicLinkConnected)
	publish(t, cl.bus, "acme.crm.deal.created", "")
	hubEvents.nextTopic(t, "acme.crm.deal.created")
	reconnectHeld(t, auth, cl, h.url, events, func() {
		publish(t, cl.bus, "acme.crm.deal.updated", "")
		publish(t, cl.bus, "acme.crm.deal.won", "")
	})
	publish(t, cl.bus, "acme.crm.deal.closed", "")
	for _, want := range []Topic{"acme.crm.deal.updated", "acme.crm.deal.won", "acme.crm.deal.closed"} {
		hubEvents.nextTopic(t, want)
	}
}

func TestOutbox_OverflowDropsOldestAndCounts(t *testing.T) {
	auth := &gateAuth{}
	h := newHub(t, WithAuth(auth))
	hubEvents := collect(h.bus, "acme.#")
	drops := make(chan uint64, 1)
	cl := newClient(t, WithAuth(OpenAuth()), WithOutbox(), WithStreamLog(2),
		WithOutboxDropFunc(func(_ string, n uint64) { drops <- n }), WithBackoff(time.Millisecond, time.Millisecond))
	events := collect(cl.bus, "kit.runtime.link.#")
	connect(t, cl, h.url)
	events.nextTopic(t, TopicLinkConnected)
	reconnectHeld(t, auth, cl, h.url, events, func() {
		for _, topic := range []Topic{"acme.crm.deal.a", "acme.crm.deal.b", "acme.crm.deal.c", "acme.crm.deal.d"} {
			publish(t, cl.bus, topic, "")
		}
	})
	hubEvents.nextTopic(t, "acme.crm.deal.c")
	hubEvents.nextTopic(t, "acme.crm.deal.d")
	if n := <-drops; n != 2 || cl.n.OutboxDropped(h.url) != 2 {
		t.Fatalf("dropped %d / %d, want 2", n, cl.n.OutboxDropped(h.url))
	}
}

func TestOutbox_OffKeepsDropBehaviour(t *testing.T) {
	auth := &gateAuth{}
	h := newHub(t, WithAuth(auth))
	hubEvents := collect(h.bus, "acme.#")
	cl := newClient(t, WithAuth(OpenAuth()), WithBackoff(time.Millisecond, time.Millisecond))
	events := collect(cl.bus, "kit.runtime.link.#")
	connect(t, cl, h.url)
	events.nextTopic(t, TopicLinkConnected)
	reconnectHeld(t, auth, cl, h.url, events, func() { publish(t, cl.bus, "acme.crm.deal.lost", "") })
	publish(t, cl.bus, "acme.crm.deal.won", "")
	hubEvents.nextTopic(t, "acme.crm.deal.won")
}

func TestOutbox_OfflineStartKeepsEvents(t *testing.T) {
	auth := &gateAuth{}
	auth.setReject(true)
	hb := New()
	hub := NewNetworkAdapter(hb, WithAuth(auth))
	srv := httptest.NewServer(hub.Handler())
	t.Cleanup(func() { _ = hub.Close(); srv.Close() })
	hubEvents := collect(hb, "acme.#")
	url := "ws" + srv.URL[4:]

	cb := New(WithNetwork(url), WithNetworkOption(WithAuth(OpenAuth()), WithOutbox(), WithBackoff(time.Millisecond, 5*time.Millisecond)))
	t.Cleanup(func() { _ = cb.Close(context.Background()) })
	publish(t, cb, "acme.crm.deal.created", "")
	publish(t, cb, "acme.crm.deal.updated", "")
	auth.release()
	hubEvents.nextTopic(t, "acme.crm.deal.created")
	hubEvents.nextTopic(t, "acme.crm.deal.updated")
}

func waitHook(t *testing.T, ch chan string, want string) {
	t.Helper()
	deadline := time.After(failAfter)
	for {
		select {
		case ev := <-ch:
			if ev == want {
				return
			}
		case <-deadline:
			t.Fatalf("hook %q never fired", want)
		}
	}
}

func isCloseStatus(err error, code websocket.StatusCode) bool {
	var ce websocket.CloseError
	return errors.As(err, &ce) && ce.Code == code
}
