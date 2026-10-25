package bus

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fakeAcceptor answers every auth frame with ack, verbatim, and counts
// connections.
func fakeAcceptor(t *testing.T, ack string) (url string, accepts *atomic.Int32) {
	t.Helper()
	accepts = &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		accepts.Add(1)
		ctx := r.Context()
		if _, _, err := c.Read(ctx); err != nil {
			return
		}
		_ = c.Write(ctx, websocket.MessageText, []byte(ack))
		for {
			if _, _, err := c.Read(ctx); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return "ws" + srv.URL[4:], accepts
}

func TestHandshake_NoHookDialerConnectsToOrderedHub(t *testing.T) {
	type plainBus struct{ Bus }
	h := newHub(t, WithAuth(OpenAuth()))
	b := plainBus{New()}
	n := NewNetworkAdapter(b, WithAuth(OpenAuth()))
	t.Cleanup(func() { _ = n.Close() })
	events := collect(b, "acme.#")
	if err := n.Connect(context.Background(), h.url); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if st, _ := n.LinkState(h.url); !st.Connected || st.Streams {
		t.Fatalf("link state = %+v, want a connected legacy link", st)
	}
	publish(t, h.bus, "acme.crm.deal.created", "")
	if g := events.nextTopic(t, "acme.crm.deal.created"); g.hasPos {
		t.Fatal("a legacy link carries no position")
	}
}

func TestHandshake_ProtocolErrorIsFinalAndSurfaced(t *testing.T) {
	url, accepts := fakeAcceptor(t, `{"type":"auth_ok","caps":{"streams":1,"frames":["kit"]}}`)
	errs := make(chan error, 4)
	b := New(WithNetwork(url), WithNetworkOption(WithAuth(OpenAuth()),
		WithNetworkErrorFunc(func(err error) { errs <- err })))
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	n := b.(*networkedBus).network
	if err := <-errs; !isProtocolError(err) {
		t.Fatalf("reported %v, want a protocol error", err)
	}
	st, _ := n.LinkState(url)
	if !st.Final || st.Code != "protocol" {
		t.Fatalf("link state = %+v", st)
	}
	n.mu.RLock()
	retrying := n.reconnecting[url]
	n.mu.RUnlock()
	if retrying || accepts.Load() != 1 {
		t.Fatalf("retrying=%v accepts=%d, want a final failure", retrying, accepts.Load())
	}
}

func TestHandshake_HugeHeartbeatIsRejectedNotPanicking(t *testing.T) {
	url, _ := fakeAcceptor(t, `{"type":"auth_ok","caps":{"streams":1,"frames":["kit"]},"stream":{"id":"s","oldest":"1","head":"0","heartbeat_ms":10000000000000}}`)
	cl := newClient(t, WithAuth(OpenAuth()))
	events := collect(cl.bus, "kit.runtime.#")
	if err := cl.n.Connect(context.Background(), url); !isProtocolError(err) {
		t.Fatalf("connect err = %v, want a protocol error", err)
	}
	if g := events.nextTopic(t, TopicLinkDisconnected); field(g, "final") != true || field(g, "code") != "protocol" {
		t.Fatalf("disconnected = %v", g.Payload)
	}
}

func TestHandshake_AuthRejectIsSurfacedOnceAndRetryable(t *testing.T) {
	auth := &gateAuth{}
	auth.setReject(true)
	h := newHub(t, WithAuth(auth))
	var errCount atomic.Int32
	cl := newClient(t, WithAuth(OpenAuth()), WithNetworkErrorFunc(func(error) { errCount.Add(1) }))
	events := collect(cl.bus, "kit.runtime.#")
	for i := 0; i < 2; i++ {
		if err := cl.n.Connect(context.Background(), h.url); !errors.Is(err, ErrAuthFailed) {
			t.Fatalf("connect err = %v", err)
		}
	}
	if g := events.nextTopic(t, TopicLinkDisconnected); field(g, "final") != false || field(g, "code") != "auth" {
		t.Fatalf("disconnected = %v", g.Payload)
	}
	events.none(t) // the repeat is not published again
	if errCount.Load() != 2 {
		t.Fatalf("error func called %d times, want 2", errCount.Load())
	}
	auth.release()
	connect(t, cl, h.url)
	if st, _ := cl.n.LinkState(h.url); !st.Connected || st.Final {
		t.Fatalf("link state = %+v", st)
	}
}

func TestHandshake_SuccessfulConnectClearsFinal(t *testing.T) {
	var deny atomic.Bool
	deny.Store(true)
	h := newHub(t, WithAuth(OpenAuth()), WithSubscribeAuthorizer(func(context.Context, Peer, Subscription) error {
		if deny.Load() {
			return errors.New("no")
		}
		return nil
	}))
	cl := newClient(t, WithAuth(OpenAuth()))
	if err := cl.n.Connect(context.Background(), h.url); !errors.Is(err, ErrStream) {
		t.Fatalf("connect err = %v", err)
	}
	deny.Store(false)
	connect(t, cl, h.url)
	if st, _ := cl.n.LinkState(h.url); st.Final {
		t.Fatalf("link state = %+v, want Final cleared", st)
	}
}

// reentrant subscribes to topic on the client and, from inside the
// handler, calls Subscribe on the delivering address.
func reentrant(b Bus, n *NetworkAdapter, addr string, topic Topic) chan error {
	ch := make(chan error, 1)
	var once sync.Once
	b.Subscribe(string(topic), func(ctx context.Context, _ Event) error {
		once.Do(func() {
			_, err := n.Subscribe(ctx, addr, "acme.other.#")
			ch <- err
		})
		return nil
	})
	return ch
}

// within runs fn, failing (not hanging) if it does not return in time.
func within(t *testing.T, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); fn() }()
	select {
	case <-done:
	case <-time.After(failAfter):
		t.Fatal("blocked: deadlock")
	}
}

func recvErr(t *testing.T, ch chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(failAfter):
		t.Fatal("handler never returned: deadlock")
		return nil
	}
}

func TestSubscribe_ReentrantFromLinkConnected(t *testing.T) {
	h := newHub(t, WithAuth(OpenAuth()))
	cl := newClient(t, WithAuth(OpenAuth()))
	ch := reentrant(cl.bus, cl.n, h.url, TopicLinkConnected)
	within(t, func() { _ = cl.n.Connect(context.Background(), h.url) })
	if err := recvErr(t, ch); !errors.Is(err, ErrReentrantRequest) {
		t.Fatalf("err = %v", err)
	}
}

func TestSubscribe_ReentrantFromInvalidated(t *testing.T) {
	h := newHub(t, WithAuth(OpenAuth()))
	cl := newClient(t, WithAuth(OpenAuth()), WithResumeFrom(h.url, StreamPosition{Stream: "stm_gone", Offset: 1}))
	ch := reentrant(cl.bus, cl.n, h.url, TopicStreamInvalidated)
	within(t, func() { _ = cl.n.Connect(context.Background(), h.url) })
	if err := recvErr(t, ch); !errors.Is(err, ErrReentrantRequest) {
		t.Fatalf("err = %v", err)
	}
}

func TestSubscribe_ReentrantFromDeliveredEvent(t *testing.T) {
	h := newHub(t, WithAuth(OpenAuth()))
	cl := newClient(t, WithAuth(OpenAuth()))
	ch := reentrant(cl.bus, cl.n, h.url, "acme.crm.deal.created")
	connect(t, cl, h.url)
	publish(t, h.bus, "acme.crm.deal.created", "")
	if err := recvErr(t, ch); !errors.Is(err, ErrReentrantRequest) {
		t.Fatalf("err = %v", err)
	}
	// Another goroutine may still subscribe.
	if _, err := cl.n.Subscribe(context.Background(), h.url, "acme.other.#"); err != nil {
		t.Fatal(err)
	}
}
