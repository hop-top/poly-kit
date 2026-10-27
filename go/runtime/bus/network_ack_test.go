package bus

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type ackOutcome struct {
	ack Ack
	err error
}

// publishAcked runs PublishAcked in the background.
func publishAcked(ctx context.Context, n *NetworkAdapter, topic Topic, addrs ...string) <-chan ackOutcome {
	ch := make(chan ackOutcome, 1)
	go func() {
		a, err := n.PublishAcked(ctx, NewEvent(topic, "test", nil), addrs...)
		ch <- ackOutcome{a, err}
	}()
	return ch
}

func waitAck(t *testing.T, ch <-chan ackOutcome) ackOutcome {
	t.Helper()
	select {
	case o := <-ch:
		return o
	case <-time.After(failAfter):
		t.Fatal("PublishAcked did not return")
		return ackOutcome{}
	}
}

func pendingAck(t *testing.T, ch <-chan ackOutcome) {
	t.Helper()
	select {
	case o := <-ch:
		t.Fatalf("PublishAcked returned early: %+v %v", o.ack, o.err)
	case <-time.After(50 * time.Millisecond):
	}
}

func wantNack(t *testing.T, o ackOutcome, code string) *AckError {
	t.Helper()
	var ae *AckError
	if !errors.As(o.err, &ae) || ae.Code != code || !errors.Is(o.err, ErrNack) {
		t.Fatalf("err = %v, want nack %s", o.err, code)
	}
	return ae
}

// ackPair connects an acks client to a hub.
func ackPair(t *testing.T, hubOpts []NetworkOption, clOpts ...NetworkOption) (node, node, *collector) {
	t.Helper()
	h := newHub(t, append([]NetworkOption{WithAuth(OpenAuth())}, hubOpts...)...)
	cl := newClient(t, append([]NetworkOption{WithAuth(OpenAuth()), WithAcks(), WithBackoff(time.Millisecond, time.Millisecond)}, clOpts...)...)
	events := collect(cl.bus, "kit.runtime.#")
	connect(t, cl, h.url)
	events.nextTopic(t, TopicLinkConnected)
	return h, cl, events
}

func TestAcks_HubAcknowledges(t *testing.T) {
	h, cl, _ := ackPair(t, nil)
	got := collect(h.bus, "acme.#")
	o := waitAck(t, publishAcked(context.Background(), cl.n, "acme.crm.deal.created"))
	if o.err != nil {
		t.Fatal(o.err)
	}
	if len(o.ack.Peers) != 1 || o.ack.Peers[0].Peer != h.url || o.ack.Peers[0].Code != "" || o.ack.Offset != 1 {
		t.Fatalf("ack = %+v", o.ack)
	}
	got.nextTopic(t, "acme.crm.deal.created")
	if ls, _ := cl.n.LinkState(h.url); !ls.Streams {
		t.Fatal("not a stream link")
	}
}

func TestAcks_HubVetoIsRejected(t *testing.T) {
	h, cl, _ := ackPair(t, nil)
	h.bus.Subscribe("acme.#", func(context.Context, Event) error { return errors.New("no deals today") })
	ae := wantNack(t, waitAck(t, publishAcked(context.Background(), cl.n, "acme.crm.deal.created")), AckRejected)
	if ae.Peer != h.url || ae.Message == "" {
		t.Fatalf("nack = %+v", ae)
	}
	// The next event is acknowledged again: a veto is per event.
	h2 := collect(h.bus, "other.#")
	if o := waitAck(t, publishAcked(context.Background(), cl.n, "other.crm.deal.created")); o.err != nil {
		t.Fatal(o.err)
	}
	h2.nextTopic(t, "other.crm.deal.created")
}

func TestAcks_HubAsyncHandlerHoldsTheAck(t *testing.T) {
	h, cl, _ := ackPair(t, nil)
	release := newGate(t)
	h.bus.SubscribeAsync("acme.#", func(context.Context, Event) { <-release.ch })
	ch := publishAcked(context.Background(), cl.n, "acme.crm.deal.created")
	pendingAck(t, ch)
	release.open()
	if o := waitAck(t, ch); o.err != nil {
		t.Fatal(o.err)
	}
}

func TestAcks_LocalAsyncHandlerHoldsTheAck(t *testing.T) {
	_, cl, _ := ackPair(t, nil)
	release := newGate(t)
	cl.bus.SubscribeAsync("acme.#", func(context.Context, Event) { <-release.ch })
	ch := publishAcked(context.Background(), cl.n, "acme.crm.deal.created")
	pendingAck(t, ch)
	release.open()
	if o := waitAck(t, ch); o.err != nil {
		t.Fatal(o.err)
	}
}

// gate holds handlers until opened. Cleanup opens it before the
// adapters close, so a failing test never leaves a handler blocked.
type gate struct {
	ch   chan struct{}
	once sync.Once
}

func newGate(t *testing.T) *gate {
	g := &gate{ch: make(chan struct{})}
	t.Cleanup(g.open)
	return g
}

func (g *gate) open() { g.once.Do(func() { close(g.ch) }) }

// hubAcksFrame is a raw dialer's auth frame asking for acks.
const hubAcksFrame = `{"auth":"open","caps":{"streams":1,"frames":["kit"],"acks":1},"origin":"dialer","stream":{"id":"stm_dialer"}}`

func eventFrame(off int, id string, topic string) string {
	b, _ := json.Marshal(map[string]any{"id": id, "topic": topic, "source": "dialer",
		"timestamp": "2026-10-25T12:00:00Z", "payload": nil, "origin": "dialer", "offset": formatOffset(uint64(off))})
	return string(b)
}

// noFrame checks that nothing arrives for a while. Reading with a
// deadline closes the connection, so it must be a test's last read.
func noFrame(t *testing.T, c *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, data, err := c.Read(ctx); err == nil {
		t.Fatalf("unexpected frame %s", data)
	}
}

func TestAcks_PubacksGoOutInOffsetOrder(t *testing.T) {
	h := newHub(t, WithAuth(OpenAuth()))
	first, secondDone := newGate(t), make(chan struct{})
	h.bus.SubscribeAsync("acme.first", func(context.Context, Event) { <-first.ch })
	h.bus.Subscribe("acme.second", func(context.Context, Event) error { close(secondDone); return nil })
	c := rawDial(t, h.url, hubAcksFrame)
	if ack := rawJSON(t, c); ack["caps"].(map[string]any)["acks"] != float64(1) {
		t.Fatalf("ack = %v", ack)
	}
	rawSend(t, c, eventFrame(1, "e1", "acme.first"))
	rawSend(t, c, eventFrame(2, "e2", "acme.second"))
	// e2 is done before e1; its puback still waits behind e1's.
	select {
	case <-secondDone:
	case <-time.After(failAfter):
		t.Fatal("hub never handled e2")
	}
	first.open()
	for i, id := range []string{"e1", "e2"} {
		p := rawJSON(t, c)
		if p["type"] != "puback" || p["offset"] != formatOffset(uint64(i+1)) || p["id"] != id || p["status"] != "ok" {
			t.Fatalf("puback %d = %v", i, p)
		}
	}
}

func TestAcks_RedeliveryIsDeduped(t *testing.T) {
	h := newHub(t, WithAuth(OpenAuth()))
	var seen atomic.Int32
	h.bus.Subscribe("acme.#", func(context.Context, Event) error { seen.Add(1); return nil })
	h.bus.Subscribe("acme.bad", func(context.Context, Event) error { return errors.New("nope") })
	c := rawDial(t, h.url, hubAcksFrame)
	rawJSON(t, c)
	rawSend(t, c, eventFrame(1, "e1", "acme.ok"))
	rawSend(t, c, eventFrame(2, "e2", "acme.bad"))
	rawJSON(t, c)
	if p := rawJSON(t, c); p["status"] != "err" || p["code"] != AckRejected {
		t.Fatalf("puback = %v", p)
	}
	_ = c.Close(websocket.StatusNormalClosure, "")

	// Same dialer stream on a new link: 1 and 2 are sent again, 3 is new.
	c2 := rawDial(t, h.url, hubAcksFrame)
	rawJSON(t, c2)
	rawSend(t, c2, eventFrame(1, "e1", "acme.ok"))
	rawSend(t, c2, eventFrame(2, "e2", "acme.bad"))
	rawSend(t, c2, eventFrame(3, "e3", "acme.ok"))
	if p := rawJSON(t, c2); p["offset"] != "1" || p["status"] != "ok" {
		t.Fatalf("puback = %v", p)
	}
	if p := rawJSON(t, c2); p["offset"] != "2" || p["status"] != "err" || p["code"] != AckRejected {
		t.Fatalf("redelivered failure acked as %v", p)
	}
	if p := rawJSON(t, c2); p["offset"] != "3" || p["status"] != "ok" {
		t.Fatalf("puback = %v", p)
	}
	if n := seen.Load(); n != 3 {
		t.Fatalf("hub handled %d events, want 3 (e1, e2, e3 once each)", n)
	}
}

func TestAcks_OldDialerGetsNoPuback(t *testing.T) {
	h := newHub(t, WithAuth(OpenAuth()))
	got := collect(h.bus, "acme.#")
	c := rawDial(t, h.url, `{"auth":"open","caps":{"streams":1,"frames":["kit"]},"origin":"dialer"}`)
	if ack := rawJSON(t, c); ack["caps"].(map[string]any)["acks"] != nil {
		t.Fatalf("ack advertises acks to a dialer that did not ask: %v", ack)
	}
	rawSend(t, c, eventFrame(1, "e1", "acme.crm.deal.created"))
	got.nextTopic(t, "acme.crm.deal.created")
	noFrame(t, c)
}

func TestAcks_AcksWithoutStreamIDIsRefused(t *testing.T) {
	h := newHub(t, WithAuth(OpenAuth()))
	c := rawDial(t, h.url, `{"auth":"open","caps":{"streams":1,"frames":["kit"],"acks":1},"origin":"dialer"}`)
	if f := rawJSON(t, c); f["type"] != "session_error" || f["code"] != CodeBadRequest {
		t.Fatalf("reply = %v", f)
	}
}

func TestAcks_OldAcceptorIsUnsupported(t *testing.T) {
	_, cl, _ := ackPair(t, []NetworkOption{func(n *NetworkAdapter) { n.refuseAcks = true }})
	o := waitAck(t, publishAcked(context.Background(), cl.n, "acme.crm.deal.created"))
	wantNack(t, o, AckUnsupported)
	if !errors.Is(o.err, ErrAcksUnsupported) {
		t.Fatalf("err = %v, want ErrAcksUnsupported", o.err)
	}
}

func TestAcks_OldAcceptorSendsNoPuback(t *testing.T) {
	h := newHub(t, WithAuth(OpenAuth()), func(n *NetworkAdapter) { n.refuseAcks = true })
	c := rawDial(t, h.url, hubAcksFrame)
	if ack := rawJSON(t, c); ack["caps"].(map[string]any)["acks"] != nil {
		t.Fatalf("ack = %v", ack)
	}
	rawSend(t, c, eventFrame(1, "e1", "acme.crm.deal.created"))
	noFrame(t, c)
}

func TestAcks_WithoutWithAcksIsUnsupported(t *testing.T) {
	cl := newClient(t)
	if _, err := cl.n.PublishAcked(context.Background(), NewEvent("acme.x", "t", nil)); !errors.Is(err, ErrAcksUnsupported) {
		t.Fatalf("err = %v", err)
	}
	if AckPublisherOf(New()) == nil {
		t.Fatal("a memory bus confirms local delivery")
	}
	nb := &networkedBus{Bus: New(), network: cl.n}
	if AckPublisherOf(nb) != nil {
		t.Fatal("a networked bus without WithAcks cannot confirm delivery to its peers")
	}
}

func TestAcks_Timeout(t *testing.T) {
	h, cl, _ := ackPair(t, nil)
	release := newGate(t)
	h.bus.Subscribe("acme.#", func(context.Context, Event) error { <-release.ch; return nil })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	wantNack(t, waitAck(t, publishAcked(ctx, cl.n, "acme.crm.deal.created")), AckTimeout)
}

func TestAcks_DefaultTimeoutWithoutDeadline(t *testing.T) {
	h, cl, _ := ackPair(t, nil, WithAckTimeout(50*time.Millisecond))
	release := newGate(t)
	h.bus.Subscribe("acme.#", func(context.Context, Event) error { <-release.ch; return nil })
	wantNack(t, waitAck(t, publishAcked(context.Background(), cl.n, "acme.crm.deal.created")), AckTimeout)
}

func TestAcks_DisconnectIsUnavailable(t *testing.T) {
	h, cl, _ := ackPair(t, nil)
	release := newGate(t)
	h.bus.Subscribe("acme.#", func(context.Context, Event) error { <-release.ch; return nil })
	ch := publishAcked(context.Background(), cl.n, "acme.crm.deal.created")
	pendingAck(t, ch)
	go func() { _ = cl.n.Disconnect(h.url) }()
	wantNack(t, waitAck(t, ch), AckUnavailable)
}

func TestAcks_UnknownAddressIsUnavailable(t *testing.T) {
	_, cl, _ := ackPair(t, nil)
	wantNack(t, waitAck(t, publishAcked(context.Background(), cl.n, "acme.x", "ws://nowhere")), AckUnavailable)
}

func TestAcks_TopicFilterIsFiltered(t *testing.T) {
	_, cl, _ := ackPair(t, nil, WithFilter(TopicFilter{Deny: []string{"acme.private.#"}}))
	wantNack(t, waitAck(t, publishAcked(context.Background(), cl.n, "acme.private.x")), AckFiltered)
}

func TestAcks_EvictedBeforeSend(t *testing.T) {
	auth := &gateAuth{}
	h := newHub(t, WithAuth(auth))
	cl := newClient(t, WithAuth(OpenAuth()), WithAcks(), WithStreamLog(2), WithBackoff(time.Millisecond, time.Millisecond))
	events := collect(cl.bus, "kit.runtime.#")
	connect(t, cl, h.url)
	events.nextTopic(t, TopicLinkConnected)
	var ch <-chan ackOutcome
	reconnectHeld(t, auth, cl, h.url, events, func() {
		ch = publishAcked(context.Background(), cl.n, "acme.crm.deal.created")
		pendingAck(t, ch)
		publish(t, cl.bus, "acme.crm.deal.updated", "")
		publish(t, cl.bus, "acme.crm.deal.won", "")
	})
	wantNack(t, waitAck(t, ch), AckEvicted)
}

// TestAcks_LostLinkIsSentAgain drops the link while the hub still holds
// the event: the reconnect sends it again from the last acknowledged
// offset, the hub answers the new copy with the first copy's result,
// and its handler runs once.
func TestAcks_LostLinkIsSentAgain(t *testing.T) {
	auth := &gateAuth{}
	h := newHub(t, WithAuth(auth))
	var runs atomic.Int32
	entered, release := make(chan struct{}, 1), newGate(t)
	h.bus.Subscribe("acme.#", func(context.Context, Event) error {
		if runs.Add(1) == 1 {
			entered <- struct{}{}
			<-release.ch
		}
		return nil
	})
	cl := newClient(t, WithAuth(OpenAuth()), WithAcks(), WithBackoff(time.Millisecond, time.Millisecond))
	events := collect(cl.bus, "kit.runtime.#")
	connect(t, cl, h.url)
	events.nextTopic(t, TopicLinkConnected)
	ch := publishAcked(context.Background(), cl.n, "acme.crm.deal.created")
	select {
	case <-entered:
	case <-time.After(failAfter):
		t.Fatal("hub never handled the event")
	}
	reconnectHeld(t, auth, cl, h.url, events, func() {})
	pendingAck(t, ch)
	release.open()
	if o := waitAck(t, ch); o.err != nil {
		t.Fatal(o.err)
	}
	if n := runs.Load(); n != 1 {
		t.Fatalf("hub handled the event %d times", n)
	}
}

func TestAcks_PendingCapBlocks(t *testing.T) {
	h, cl, _ := ackPair(t, nil, WithMaxPendingAcks(1))
	release := newGate(t)
	var seen atomic.Int32
	h.bus.Subscribe("acme.#", func(context.Context, Event) error {
		if seen.Add(1) == 1 {
			<-release.ch
		}
		return nil
	})
	first := publishAcked(context.Background(), cl.n, "acme.first")
	pendingAck(t, first)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if o := waitAck(t, publishAcked(ctx, cl.n, "acme.second")); !errors.Is(o.err, context.DeadlineExceeded) {
		t.Fatalf("second = %+v %v, want the slot wait to time out", o.ack, o.err)
	}
	third := publishAcked(context.Background(), cl.n, "acme.third")
	pendingAck(t, third)
	release.open()
	if o := waitAck(t, first); o.err != nil {
		t.Fatal(o.err)
	}
	if o := waitAck(t, third); o.err != nil {
		t.Fatal(o.err)
	}
	if n := seen.Load(); n != 2 {
		t.Fatalf("hub saw %d events; the blocked one must not be published", n)
	}
}

// cumulativeAcceptor is a hand-written acceptor that answers two event
// frames with a single puback for the second.
func cumulativeAcceptor(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx := r.Context()
		if _, _, err := c.Read(ctx); err != nil {
			return
		}
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"type":"auth_ok","caps":{"streams":1,"frames":["kit"],"acks":1},"origin":"hub","stream":{"id":"stm_hub","oldest":"1","head":"0","heartbeat_ms":15000}}`))
		var last map[string]any
		for i := 0; i < 2; i++ {
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			_ = json.Unmarshal(data, &last)
		}
		_ = c.Write(ctx, websocket.MessageText, encodePuback(pubackInfo{Offset: mustOffset(t, last["offset"].(string)), Status: pubackOK}))
		<-ctx.Done()
	}))
	t.Cleanup(srv.Close)
	return "ws" + srv.URL[4:]
}

func TestAcks_CumulativePubackResolvesEarlierFrames(t *testing.T) {
	url := cumulativeAcceptor(t)
	cl := newClient(t, WithAuth(OpenAuth()), WithAcks())
	connect(t, cl, url)
	first := publishAcked(context.Background(), cl.n, "acme.first")
	pendingAck(t, first)
	second := publishAcked(context.Background(), cl.n, "acme.second")
	for _, ch := range []<-chan ackOutcome{first, second} {
		if o := waitAck(t, ch); o.err != nil {
			t.Fatal(o.err)
		}
	}
}

func acksAuth(token, stream string) string {
	return `{"auth":"` + token + `","caps":{"streams":1,"frames":["kit"],"acks":1},"origin":"o-` + token + `","stream":{"id":"` + stream + `"}}`
}

// principalAuth names a principal per token.
type principalAuth map[string]string

func (principalAuth) Token() (string, error) { return "open", nil }
func (p principalAuth) Verify(tok string) error {
	_, err := p.VerifyPrincipal(tok)
	return err
}
func (p principalAuth) VerifyPrincipal(tok string) (string, error) {
	if who, ok := p[tok]; ok {
		return who, nil
	}
	return "", ErrAuthFailed
}

// floodStreams opens n dialer streams with token, one frame each.
func floodStreams(t *testing.T, url, token string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		a := rawDial(t, url, acksAuth(token, "stm_flood"+formatOffset(uint64(i))))
		rawJSON(t, a)
		rawSend(t, a, eventFrame(1, "ea", "acme.flood"))
		rawJSON(t, a)
		_ = a.Close(websocket.StatusNormalClosure, "")
	}
}

// TestAcks_PrincipalFloodKeepsOtherPrincipalsDedupe: a principal that
// opens more streams than its bound only evicts its own dedupe memory;
// another principal's copies are still recognized and not published
// twice.
func TestAcks_PrincipalFloodKeepsOtherPrincipalsDedupe(t *testing.T) {
	h := newHub(t, WithAuth(principalAuth{"tok-a": "alice", "tok-b": "bob"}))
	// Room for alice's bound and bob, not for alice's whole flood: only
	// the per-principal bound keeps alice from reaching the overall one.
	h.n.ackSeen.maxStreams = ackSeenStreamsPerPrincipal + 4
	var seen atomic.Int32
	h.bus.Subscribe("acme.b", func(context.Context, Event) error { seen.Add(1); return nil })

	b := rawDial(t, h.url, acksAuth("tok-b", "stm_b"))
	rawJSON(t, b)
	rawSend(t, b, eventFrame(1, "eb", "acme.b"))
	rawJSON(t, b)
	_ = b.Close(websocket.StatusNormalClosure, "")

	floodStreams(t, h.url, "tok-a", ackSeenStreamsPerPrincipal+16)

	b2 := rawDial(t, h.url, acksAuth("tok-b", "stm_b"))
	rawJSON(t, b2)
	rawSend(t, b2, eventFrame(1, "eb", "acme.b"))
	if p := rawJSON(t, b2); p["offset"] != "1" || p["status"] != "ok" {
		t.Fatalf("puback = %v", p)
	}
	if n := seen.Load(); n != 1 {
		t.Fatalf("bob's event published %d times after alice flooded streams", n)
	}
}

// TestAcks_SharedTokenFloodOnlyDuplicates: dialers that share a token
// cannot be told apart, so a flood can evict another's memory. The
// evicted frame is published again and answered with its new result,
// never a false ok.
func TestAcks_SharedTokenFloodOnlyDuplicates(t *testing.T) {
	h := newHub(t, WithAuth(OpenAuth()))
	h.n.ackSeen.maxStreams = 4
	var seen atomic.Int32
	h.bus.Subscribe("acme.b", func(context.Context, Event) error { seen.Add(1); return errors.New("nope") })

	b := rawDial(t, h.url, acksAuth("open", "stm_b"))
	rawJSON(t, b)
	rawSend(t, b, eventFrame(1, "eb", "acme.b"))
	if p := rawJSON(t, b); p["status"] != "err" {
		t.Fatalf("puback = %v", p)
	}
	_ = b.Close(websocket.StatusNormalClosure, "")

	floodStreams(t, h.url, "open", 8)

	b2 := rawDial(t, h.url, acksAuth("open", "stm_b"))
	rawJSON(t, b2)
	rawSend(t, b2, eventFrame(1, "eb", "acme.b"))
	if p := rawJSON(t, b2); p["status"] != "err" || p["code"] != AckRejected {
		t.Fatalf("evicted copy answered %v, want its new result", p)
	}
	if n := seen.Load(); n != 2 {
		t.Fatalf("published %d times; an evicted copy is published again (want 2)", n)
	}
}

// TestAcks_SameStreamIDUnderTwoCredentials: two clients of one
// principal with different tokens that claim the same dialer stream id
// are two streams; overlapping offsets never suppress each other's
// events.
func TestAcks_SameStreamIDUnderTwoCredentials(t *testing.T) {
	h := newHub(t, WithAuth(principalAuth{"tok-phone": "alice", "tok-laptop": "alice"}))
	got := make(chan string, 8)
	h.bus.Subscribe("acme.#", func(_ context.Context, e Event) error { got <- e.ID; return nil })
	phone := rawDial(t, h.url, acksAuth("tok-phone", "stm_same"))
	rawJSON(t, phone)
	laptop := rawDial(t, h.url, acksAuth("tok-laptop", "stm_same"))
	rawJSON(t, laptop)
	for off := 1; off <= 2; off++ {
		rawSend(t, phone, eventFrame(off, "phone-"+formatOffset(uint64(off)), "acme.x"))
		rawJSON(t, phone)
		rawSend(t, laptop, eventFrame(off, "laptop-"+formatOffset(uint64(off)), "acme.x"))
		rawJSON(t, laptop)
	}
	seen := map[string]bool{}
	for len(seen) < 4 {
		select {
		case id := <-got:
			seen[id] = true
		case <-time.After(failAfter):
			t.Fatalf("published %v; the other credential's events were suppressed", seen)
		}
	}
}

// TestAcks_SharedTokenBoundIsTheOverallBound: without a principal, a
// token's streams are bounded only by the overall bound, so dialers that
// share it do not evict each other before that.
func TestAcks_SharedTokenBoundIsTheOverallBound(t *testing.T) {
	h := newHub(t, WithAuth(OpenAuth()))
	var seen atomic.Int32
	h.bus.Subscribe("acme.b", func(context.Context, Event) error { seen.Add(1); return nil })
	b := rawDial(t, h.url, acksAuth("open", "stm_b"))
	rawJSON(t, b)
	rawSend(t, b, eventFrame(1, "eb", "acme.b"))
	rawJSON(t, b)
	_ = b.Close(websocket.StatusNormalClosure, "")

	floodStreams(t, h.url, "open", ackSeenStreamsPerPrincipal+16)

	b2 := rawDial(t, h.url, acksAuth("open", "stm_b"))
	rawJSON(t, b2)
	rawSend(t, b2, eventFrame(1, "eb", "acme.b"))
	rawJSON(t, b2)
	if n := seen.Load(); n != 1 {
		t.Fatalf("published %d times below the overall bound", n)
	}
}

// TestAcks_DedupeKeysHoldNoTokens: the dedupe memory is keyed by
// digests, never by a raw token.
func TestAcks_DedupeKeysHoldNoTokens(t *testing.T) {
	h := newHub(t, WithAuth(principalAuth{"s3cret-token": "alice"}))
	c := rawDial(t, h.url, acksAuth("s3cret-token", "stm_c"))
	rawJSON(t, c)
	rawSend(t, c, eventFrame(1, "e1", "acme.x"))
	rawJSON(t, c)
	o := newHub(t, WithAuth(OpenAuth()))
	c2 := rawDial(t, o.url, acksAuth("open-s3cret", "stm_c"))
	rawJSON(t, c2)
	rawSend(t, c2, eventFrame(1, "e1", "acme.x"))
	rawJSON(t, c2)
	for _, n := range []*NetworkAdapter{h.n, o.n} {
		n.ackSeen.mu.Lock()
		for k := range n.ackSeen.streams {
			for _, part := range []string{k.peer, k.cred} {
				if strings.Contains(part, "s3cret") || strings.Contains(part, "alice") {
					t.Errorf("dedupe key %q holds a raw identity", part)
				}
			}
		}
		if len(n.ackSeen.streams) != 1 {
			t.Errorf("dedupe memory holds %d streams, want 1", len(n.ackSeen.streams))
		}
		n.ackSeen.mu.Unlock()
	}
}

// TestAcks_PrunedFailureIsNeverOK: a copy whose result was pruned is
// answered err/unknown, not ok.
func TestAcks_PrunedFailureIsNeverOK(t *testing.T) {
	h := newHub(t, WithAuth(OpenAuth()))
	h.n.ackSeen.maxErrors = 1
	h.bus.Subscribe("acme.#", func(context.Context, Event) error { return errors.New("nope") })
	c := rawDial(t, h.url, hubAcksFrame)
	rawJSON(t, c)
	rawSend(t, c, eventFrame(1, "e1", "acme.x"))
	rawSend(t, c, eventFrame(2, "e2", "acme.x"))
	rawJSON(t, c)
	rawJSON(t, c)
	rawSend(t, c, eventFrame(1, "e1", "acme.x"))
	if p := rawJSON(t, c); p["status"] != "err" || p["code"] != AckUnknown {
		t.Fatalf("copy of a pruned failure answered %v", p)
	}
}
