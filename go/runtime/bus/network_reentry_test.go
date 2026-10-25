package bus

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestSubscribe_FromAsyncHandlerOnLinkConnected(t *testing.T) {
	h := newHub(t, WithAuth(OpenAuth()))
	cl := newClient(t, WithAuth(OpenAuth()))
	ch := make(chan error, 1)
	var once sync.Once
	cl.bus.SubscribeAsync(string(TopicLinkConnected), func(ctx context.Context, _ Event) {
		once.Do(func() {
			_, err := cl.n.Subscribe(ctx, h.url, "acme.billing.#")
			ch <- err
		})
	})
	events := collect(cl.bus, "acme.#")
	connect(t, cl, h.url)
	if err := recvErr(t, ch); err != nil {
		t.Fatalf("async subscribe = %v, want success", err)
	}
	publish(t, h.bus, "acme.crm.deal.created", "")
	publish(t, h.bus, "acme.billing.invoice.paid", "")
	events.nextTopic(t, "acme.billing.invoice.paid")
}

func TestSubscribe_ReentrantWithBackgroundUnblocksOnClose(t *testing.T) {
	h := newHub(t, WithAuth(OpenAuth()))
	cl := newClient(t, WithAuth(OpenAuth()))
	ch := make(chan error, 1)
	started := make(chan struct{})
	var once sync.Once
	cl.bus.Subscribe("acme.crm.deal.created", func(context.Context, Event) error {
		once.Do(func() {
			close(started)
			// Background hides the delivery marker: the call waits for a
			// reply only this goroutine could read, until the link closes.
			_, err := cl.n.Subscribe(context.Background(), h.url, "acme.other.#")
			ch <- err
		})
		return nil
	})
	connect(t, cl, h.url)
	publish(t, h.bus, "acme.crm.deal.created", "")
	<-started
	within(t, func() { _ = cl.n.Disconnect(h.url) })
	if err := recvErr(t, ch); !errors.Is(err, errLinkClosed) {
		t.Fatalf("err = %v, want errLinkClosed", err)
	}
}

func TestOutbox_WriteFailureKeepsTheFrame(t *testing.T) {
	auth := &gateAuth{}
	h := newHub(t, WithAuth(auth))
	hubEvents := collect(h.bus, "acme.#")
	cl := newClient(t, WithAuth(OpenAuth()), WithOutbox(), WithBackoff(time.Millisecond, time.Millisecond))
	var fail sync.Once
	failed := make(chan struct{})
	cl.n.writeFault = func([]byte) error {
		err := errors.New("injected write failure")
		ok := false
		fail.Do(func() { ok = true; close(failed) })
		if ok {
			return err
		}
		return nil
	}
	events := collect(cl.bus, "kit.runtime.link.#")
	connect(t, cl, h.url)
	events.nextTopic(t, TopicLinkConnected)
	auth.arm()
	publish(t, cl.bus, "acme.crm.deal.created", "") // first write fails
	<-failed
	events.nextTopic(t, TopicLinkDisconnected)
	auth.release()
	events.nextTopic(t, TopicLinkConnected)
	hubEvents.nextTopic(t, "acme.crm.deal.created")
}

func TestHandshake_AckTimeoutIsNotAnAuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		for { // read the auth frame, never answer
			if _, _, err := c.Read(r.Context()); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	url := "ws" + srv.URL[4:]
	cl := newClient(t, WithAuth(OpenAuth()), func(n *NetworkAdapter) { n.ackTimeout = 50 * time.Millisecond })
	events := collect(cl.bus, "kit.runtime.#")
	err := cl.n.Connect(context.Background(), url)
	if err == nil || isFinalConnectErr(err) {
		t.Fatalf("connect err = %v, want a retryable error", err)
	}
	if g := events.nextTopic(t, TopicLinkDisconnected); field(g, "code") != "timeout" || field(g, "final") != false {
		t.Fatalf("disconnected = %v", g.Payload)
	}
}

func TestConnect_RacingDisconnectReturnsAnError(t *testing.T) {
	auth := &gateAuth{entered: make(chan struct{}, 1)}
	auth.arm()
	h := newHub(t, WithAuth(auth))
	cl := newClient(t, WithAuth(OpenAuth()))
	done := make(chan error, 1)
	go func() { done <- cl.n.Connect(context.Background(), h.url) }()
	<-auth.entered // the dialer is inside the handshake
	if err := cl.n.Disconnect(h.url); err != nil {
		t.Fatal(err)
	}
	auth.release()
	select {
	case err := <-done:
		if !errors.Is(err, ErrDisconnected) {
			t.Fatalf("connect err = %v, want ErrDisconnected", err)
		}
	case <-time.After(failAfter):
		t.Fatal("connect never returned")
	}
	if _, ok := cl.n.LinkState(h.url); ok {
		t.Fatal("the address came back")
	}
}
