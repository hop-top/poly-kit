package bus

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestNetworkAuth_ValidToken(t *testing.T) {
	auth := &StaticTokenAuth{Token_: "secret123"}

	busA := New()
	adapterA := NewNetworkAdapter(busA, WithOriginID("A"), WithAuth(auth))

	srv := httptest.NewServer(adapterA.Handler())
	defer srv.Close()
	defer adapterA.Close()

	busB := New()
	adapterB := NewNetworkAdapter(busB, WithOriginID("B"), WithAuth(auth))
	defer adapterB.Close()

	wsURL := "ws" + srv.URL[4:]
	if err := adapterB.Connect(context.Background(), wsURL); err != nil {
		t.Fatalf("connect with valid auth: %v", err)
	}

	// Verify events flow after auth.
	var received atomic.Bool
	busA.Subscribe("auth.test", func(_ context.Context, e Event) error {
		received.Store(true)
		return nil
	})

	time.Sleep(50 * time.Millisecond)
	_ = busB.Publish(context.Background(), NewEvent("auth.test", "B", nil))

	deadline := time.After(2 * time.Second)
	for !received.Load() {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for event after auth")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestNetworkAuth_InvalidToken(t *testing.T) {
	serverAuth := &StaticTokenAuth{Token_: "correct"}
	clientAuth := &StaticTokenAuth{Token_: "wrong"}

	busA := New()
	adapterA := NewNetworkAdapter(busA, WithOriginID("A"), WithAuth(serverAuth))

	srv := httptest.NewServer(adapterA.Handler())
	defer srv.Close()
	defer adapterA.Close()

	busB := New()
	adapterB := NewNetworkAdapter(busB, WithOriginID("B"), WithAuth(clientAuth))
	defer adapterB.Close()

	wsURL := "ws" + srv.URL[4:]
	// Connect succeeds at TCP level but server closes immediately.
	err := adapterB.Connect(context.Background(), wsURL)
	if err != nil {
		// Some implementations may surface the error immediately.
		return
	}

	// Even if Connect didn't error, events should not flow.
	var received atomic.Bool
	busA.Subscribe("auth.fail", func(_ context.Context, e Event) error {
		received.Store(true)
		return nil
	})

	time.Sleep(100 * time.Millisecond)
	_ = busB.Publish(context.Background(), NewEvent("auth.fail", "B", nil))

	time.Sleep(300 * time.Millisecond)
	if received.Load() {
		t.Error("event should not be delivered with invalid auth")
	}
}

func TestNetworkAuth_MissingClientAuth(t *testing.T) {
	serverAuth := &StaticTokenAuth{Token_: "secret"}

	busA := New()
	adapterA := NewNetworkAdapter(busA, WithOriginID("A"), WithAuth(serverAuth))

	srv := httptest.NewServer(adapterA.Handler())
	defer srv.Close()
	defer adapterA.Close()

	// Client has no auth configured.
	busB := New()
	adapterB := NewNetworkAdapter(busB, WithOriginID("B"))
	defer adapterB.Close()

	wsURL := "ws" + srv.URL[4:]
	_ = adapterB.Connect(context.Background(), wsURL)

	// Server expects auth; events should not flow.
	var received atomic.Bool
	busA.Subscribe("noauth.test", func(_ context.Context, e Event) error {
		received.Store(true)
		return nil
	})

	time.Sleep(100 * time.Millisecond)
	_ = busB.Publish(context.Background(), NewEvent("noauth.test", "B", nil))

	time.Sleep(300 * time.Millisecond)
	if received.Load() {
		t.Error("event should not be delivered without auth")
	}
}

func TestStaticTokenAuth_Verify(t *testing.T) {
	auth := &StaticTokenAuth{Token_: "abc"}

	if err := auth.Verify("abc"); err != nil {
		t.Errorf("valid token rejected: %v", err)
	}
	if err := auth.Verify("xyz"); err == nil {
		t.Error("invalid token should be rejected")
	}
}
