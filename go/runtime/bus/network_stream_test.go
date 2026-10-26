package bus

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

const capsFrame = `{"auth":"open","caps":{"streams":1,"frames":["kit","cloudevents"]},"origin":"raw"}`

func TestHandshake_PlainAckForLegacyDialer(t *testing.T) {
	h := newHub(t, WithAuth(OpenAuth()))
	c := rawDial(t, h.url, `{"auth":"open"}`)
	if got := string(rawRead(t, c)); got != `{"type":"auth_ok"}` {
		t.Fatalf("ack = %s", got)
	}
	publish(t, h.bus, "acme.crm.deal.created", "")
	m := rawJSON(t, c)
	if _, ok := m["offset"]; ok || m["topic"] != "acme.crm.deal.created" {
		t.Fatalf("legacy frame = %v", m)
	}
}

func TestHandshake_AdvertisesCaps(t *testing.T) {
	h := newHub(t, WithAuth(OpenAuth()), WithOriginID("hub"))
	c := rawDial(t, h.url, capsFrame)
	ack := rawJSON(t, c)
	caps := ack["caps"].(map[string]any)
	st := ack["stream"].(map[string]any)
	if caps["streams"] != float64(1) || ack["origin"] != "hub" || st["id"] != h.n.log.ID() || st["heartbeat_ms"] != float64(15000) {
		t.Fatalf("ack = %v", ack)
	}
	publish(t, h.bus, "acme.crm.deal.created", "")
	if m := rawJSON(t, c); m["offset"] != "1" {
		t.Fatalf("stream frame = %v", m)
	}

	cl := newClient(t, WithAuth(OpenAuth()))
	connect(t, cl, h.url)
	st2, _ := cl.n.LinkState(h.url)
	if !st2.Connected || !st2.Streams || st2.Stream != h.n.log.ID() || st2.Resume != ResumeNone {
		t.Fatalf("link state = %+v", st2)
	}
}

func TestHandshake_NoAuthIsLegacy(t *testing.T) {
	h := newHub(t)
	cl := newClient(t)
	events := collect(cl.bus, "kit.runtime.#")
	connect(t, cl, h.url)
	g := events.nextTopic(t, TopicLinkConnected)
	if field(g, "resume") != ResumeUnsupported {
		t.Fatalf("connected = %v", g.Payload)
	}
	events.none(t) // first connect, no position: no invalidation
	if st, _ := cl.n.LinkState(h.url); st.Streams {
		t.Fatal("no-auth link negotiated streams")
	}
}

func TestHandshake_NoHookAdvertisesZero(t *testing.T) {
	type plainBus struct{ Bus }
	b := plainBus{New()}
	n := NewNetworkAdapter(b, WithAuth(OpenAuth()))
	srv := httptest.NewServer(n.Handler())
	t.Cleanup(func() { _ = n.Close(); srv.Close() })
	c := rawDial(t, "ws"+srv.URL[4:], capsFrame)
	ack := rawJSON(t, c)
	if ack["caps"].(map[string]any)["streams"] != float64(0) || ack["stream"] != nil {
		t.Fatalf("ack = %v", ack)
	}
}

func TestFrameFormat_NegotiatedPerPeer(t *testing.T) {
	h := newHub(t, WithAuth(OpenAuth()), WithFrameFormat(FrameCloudEvents))
	kitOnly := rawDial(t, h.url, `{"auth":"open","caps":{"streams":1,"frames":["kit"]}}`)
	both := rawDial(t, h.url, capsFrame)
	legacy := rawDial(t, h.url, `{"auth":"open"}`)
	for _, c := range []*websocket.Conn{kitOnly, both, legacy} {
		rawRead(t, c) // ack
	}
	publish(t, h.bus, "acme.crm.deal.created", "")
	if m := rawJSON(t, kitOnly); m["specversion"] != nil || m["offset"] != "1" {
		t.Errorf("kit-only peer got %v", m)
	}
	if m := rawJSON(t, both); m["specversion"] != "1.0" || m["kitoffset"] != "1" {
		t.Errorf("cloudevents peer got %v", m)
	}
	if m := rawJSON(t, legacy); m["specversion"] != nil {
		t.Errorf("legacy handshake peer got %v", m)
	}

	accepted := make(chan string, 1)
	noAuth := newHub(t, WithFrameFormat(FrameCloudEvents), withHook(accepted))
	c := rawDial(t, noAuth.url, "")
	if ev := <-accepted; ev != "accepted" {
		t.Fatal(ev)
	}
	publish(t, noAuth.bus, "acme.crm.deal.created", "")
	if m := rawJSON(t, c); m["specversion"] != "1.0" {
		t.Errorf("no-handshake peer got %v, want the configured format", m)
	}
}

func TestSubscribe_PatternAndScopeFilter(t *testing.T) {
	h := newHub(t, WithAuth(OpenAuth()))
	cl := newClient(t, WithAuth(OpenAuth()), WithSubscribe("acme.crm.#"), WithScope(Scope{Workspaces: []string{"ws1"}}))
	events := collect(cl.bus, "acme.#")
	connect(t, cl, h.url)
	publish(t, h.bus, "acme.crm.deal.created", "ws1")
	publish(t, h.bus, "acme.crm.deal.updated", "ws2")
	publish(t, h.bus, "acme.billing.invoice.paid", "ws1")
	publish(t, h.bus, "acme.crm.lead.created", "")
	publish(t, h.bus, "acme.crm.end.reached", "ws1")
	for _, want := range []Topic{"acme.crm.deal.created", "acme.crm.lead.created", "acme.crm.end.reached"} {
		g := events.nextTopic(t, want)
		if !g.hasPos || g.pos.Stream != h.n.log.ID() {
			t.Fatalf("position = %+v", g.pos)
		}
	}
	events.none(t)
}

func TestSubscribe_ReplacesImplicitAllAndAckAt(t *testing.T) {
	h := newHub(t, WithAuth(OpenAuth()))
	cl := newClient(t, WithAuth(OpenAuth()))
	events := collect(cl.bus, "acme.#")
	connect(t, cl, h.url)
	publish(t, h.bus, "acme.crm.deal.created", "")
	publish(t, h.bus, "acme.billing.invoice.paid", "")
	events.nextTopic(t, "acme.crm.deal.created")
	events.nextTopic(t, "acme.billing.invoice.paid")
	at, err := cl.n.Subscribe(context.Background(), h.url, "acme.billing.#")
	if err != nil || at != 2 {
		t.Fatalf("subscribe = %d, %v", at, err)
	}
	publish(t, h.bus, "acme.crm.deal.created", "")
	publish(t, h.bus, "acme.billing.invoice.sent", "")
	g := events.nextTopic(t, "acme.billing.invoice.sent")
	if g.pos.Offset != 4 {
		t.Fatalf("offset = %d", g.pos.Offset)
	}
	if _, err := cl.n.SetScope(context.Background(), h.url, Scope{Workspaces: []string{}}); err != nil {
		t.Fatal(err)
	}
	publish(t, h.bus, "acme.billing.invoice.sent", "ws1")
	publish(t, h.bus, "acme.billing.invoice.paid", "")
	events.nextTopic(t, "acme.billing.invoice.paid")
}

func TestSubscribe_ErrorCodes(t *testing.T) {
	h := newHub(t, WithAuth(OpenAuth()), WithMaxSubscriptions(2),
		WithSubscribeAuthorizer(func(_ context.Context, _ Peer, s Subscription) error {
			for _, p := range s.Patterns {
				if p == "secret.#" {
					return errors.New("no secrets")
				}
			}
			return nil
		}))
	c := rawDial(t, h.url, capsFrame)
	rawRead(t, c)
	cases := []struct{ frame, id, code string }{
		{`{"type":"subscribe","id":"a","patterns":["a.#.b"]}`, "a", CodeInvalidPattern},
		{`{"type":"subscribe","id":"b","patterns":["a","b","c"]}`, "b", CodeLimit},
		{`{"type":"subscribe","id":"c","patterns":["secret.#"]}`, "c", CodeForbidden},
		{`{"type":"subscribe","id":"d","scope":{"workspaces":3}}`, "d", CodeInvalidScope},
		{`{"type":"resume","id":"e"}`, "e", CodeUnknownType},
		{`{"type":"position","id":"f","stream":"s","offset":"1"}`, "f", CodeBadRequest},
		{`{"type":"subscribe","patterns":["a"]}`, "", CodeBadRequest},
	}
	for _, tc := range cases {
		rawSend(t, c, tc.frame)
		m := rawJSON(t, c)
		id, _ := m["id"].(string)
		if m["type"] != "error" || m["code"] != tc.code || id != tc.id {
			t.Errorf("%s -> %v, want %s", tc.frame, m, tc.code)
		}
	}
	rawSend(t, c, `{"type":"subscribe","id":"ok","patterns":["a","b"]}`)
	if m := rawJSON(t, c); m["type"] != "ack" || m["id"] != "ok" || m["at"] != "0" {
		t.Fatalf("ack = %v", m)
	}
}

func TestSubscribe_AuthorizerRefusesHandshakeAndLegacy(t *testing.T) {
	deny := WithSubscribeAuthorizer(func(_ context.Context, _ Peer, s Subscription) error {
		if s.Scope == nil {
			return errors.New("scope required")
		}
		return nil
	})
	h := newHub(t, WithAuth(OpenAuth()), deny)

	cl := newClient(t, WithAuth(OpenAuth()))
	events := collect(cl.bus, "kit.runtime.#")
	err := cl.n.Connect(context.Background(), h.url)
	var se *StreamError
	if !errors.As(err, &se) || se.Code != CodeForbidden || !errors.Is(err, ErrStream) {
		t.Fatalf("connect err = %v", err)
	}
	g := events.nextTopic(t, TopicLinkDisconnected)
	if field(g, "final") != true || field(g, "code") != CodeForbidden {
		t.Fatalf("disconnected = %v", g.Payload)
	}
	if st, _ := cl.n.LinkState(h.url); !st.Final || st.Code != CodeForbidden {
		t.Fatalf("link state = %+v", st)
	}

	legacy := rawDial(t, h.url, `{"auth":"open"}`)
	ctx, cancel := context.WithTimeout(context.Background(), failAfter)
	defer cancel()
	if _, _, err := legacy.Read(ctx); websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("legacy read err = %v, want 1008 without an ack", err)
	}

	scoped := newClient(t, WithAuth(OpenAuth()), WithScope(Scope{Workspaces: []string{"w"}}))
	connect(t, scoped, h.url)
}

func TestSubscribe_LinkFilter(t *testing.T) {
	h := newHub(t, WithAuth(OpenAuth()), WithLinkFilter(func(_ Peer, e Event) bool { return e.WorkspaceID != "" }))
	cl := newClient(t, WithAuth(OpenAuth()))
	events := collect(cl.bus, "acme.#")
	connect(t, cl, h.url)
	publish(t, h.bus, "acme.crm.deal.created", "")
	publish(t, h.bus, "acme.crm.deal.updated", "ws1")
	events.nextTopic(t, "acme.crm.deal.updated")
}

func TestSubscribe_ResentOnReconnect(t *testing.T) {
	auth := &gateAuth{}
	h := newHub(t, WithAuth(auth))
	cl := newClient(t, WithAuth(OpenAuth()), WithBackoff(time.Millisecond, time.Millisecond))
	events := collect(cl.bus, "#")
	connect(t, cl, h.url)
	events.nextTopic(t, TopicLinkConnected)
	if _, err := cl.n.Subscribe(context.Background(), h.url, "acme.billing.#"); err != nil {
		t.Fatal(err)
	}
	dropLink(t, cl.n, h.url)
	events.nextTopic(t, TopicLinkDisconnected)
	events.nextTopic(t, TopicLinkConnected)
	publish(t, h.bus, "acme.crm.deal.created", "")
	publish(t, h.bus, "acme.billing.invoice.paid", "")
	events.nextTopic(t, "acme.billing.invoice.paid")
}

func TestStream_SinglePublisherOrderAndVeto(t *testing.T) {
	h := newHub(t, WithAuth(OpenAuth()))
	h.bus.Subscribe("acme.crm.deal.vetoed", func(context.Context, Event) error { return errors.New("veto") })
	cl := newClient(t, WithAuth(OpenAuth()))
	events := collect(cl.bus, "acme.#")
	connect(t, cl, h.url)
	const n = 300
	for i := 0; i < n; i++ {
		publish(t, h.bus, "acme.crm.deal.created", "")
		_ = h.bus.Publish(context.Background(), NewEvent("acme.crm.deal.vetoed", "test", nil))
	}
	for i := 1; i <= n; i++ {
		g := events.nextTopic(t, "acme.crm.deal.created")
		if g.pos.Offset != uint64(i) {
			t.Fatalf("event %d has offset %d", i, g.pos.Offset)
		}
	}
	events.none(t)
}
