package bus

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestWithNetwork_AutoConnects(t *testing.T) {
	// Set up a server-side bus.
	busA := New()
	adapterA := NewNetworkAdapter(busA, WithOriginID("srv"))
	srv := httptest.NewServer(adapterA.Handler())
	defer srv.Close()
	defer adapterA.Close()

	wsURL := "ws" + srv.URL[4:]

	// Create busB with WithNetwork option.
	busB := New(WithNetwork(wsURL))
	defer busB.Close(context.Background())

	time.Sleep(100 * time.Millisecond)

	// Verify events flow from B to A.
	var received atomic.Bool
	busA.Subscribe("net.test", func(_ context.Context, _ Event) error {
		received.Store(true)
		return nil
	})

	_ = busB.Publish(context.Background(), NewEvent("net.test", "B", nil))

	deadline := time.After(2 * time.Second)
	for !received.Load() {
		select {
		case <-deadline:
			t.Fatal("WithNetwork: event not delivered")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestNetworkAdapter_ConnectDisconnect(t *testing.T) {
	busA := New()
	adapterA := NewNetworkAdapter(busA, WithOriginID("A"))

	srv := httptest.NewServer(adapterA.Handler())
	defer srv.Close()

	busB := New()
	adapterB := NewNetworkAdapter(busB, WithOriginID("B"))
	defer adapterB.Close()

	wsURL := "ws" + srv.URL[4:] // http → ws
	if err := adapterB.Connect(context.Background(), wsURL); err != nil {
		t.Fatalf("connect: %v", err)
	}

	adapterB.mu.RLock()
	count := len(adapterB.conns)
	adapterB.mu.RUnlock()
	if count != 1 {
		t.Fatalf("expected 1 connection, got %d", count)
	}

	if err := adapterB.Disconnect(wsURL); err != nil {
		t.Fatalf("disconnect: %v", err)
	}

	adapterB.mu.RLock()
	count = len(adapterB.conns)
	adapterB.mu.RUnlock()
	if count != 0 {
		t.Fatalf("expected 0 connections after disconnect, got %d", count)
	}
}

func TestNetworkAdapter_Close(t *testing.T) {
	busA := New()
	adapter := NewNetworkAdapter(busA, WithOriginID("A"))

	if err := adapter.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Double close should be safe.
	if err := adapter.Close(); err != nil {
		t.Fatalf("double close: %v", err)
	}

	// Connect after close should fail.
	err := adapter.Connect(context.Background(), "ws://localhost:9999")
	if err != ErrBusClosed {
		t.Errorf("expected ErrBusClosed, got %v", err)
	}
}

func TestNetworkAdapter_OutboundForward(t *testing.T) {
	busA := New()
	adapterA := NewNetworkAdapter(busA, WithOriginID("A"))

	srv := httptest.NewServer(adapterA.Handler())
	defer srv.Close()
	defer adapterA.Close()

	busB := New()
	adapterB := NewNetworkAdapter(busB, WithOriginID("B"))
	defer adapterB.Close()

	wsURL := "ws" + srv.URL[4:]
	if err := adapterB.Connect(context.Background(), wsURL); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Give connection time to establish read loop.
	time.Sleep(50 * time.Millisecond)

	// Subscribe on A's bus; publish on B → should arrive at A.
	var received atomic.Bool
	busA.Subscribe("test.event", func(_ context.Context, e Event) error {
		received.Store(true)
		return nil
	})

	_ = busB.Publish(context.Background(), NewEvent("test.event", "B", "hello"))

	// Wait for delivery.
	deadline := time.After(2 * time.Second)
	for !received.Load() {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for event delivery")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestNetworkAdapter_FilterBlocks(t *testing.T) {
	busA := New()
	adapterA := NewNetworkAdapter(busA, WithOriginID("A"))

	srv := httptest.NewServer(adapterA.Handler())
	defer srv.Close()
	defer adapterA.Close()

	// B only allows "task.*" topics.
	busB := New()
	adapterB := NewNetworkAdapter(busB,
		WithOriginID("B"),
		WithFilter(TopicFilter{Allow: []string{"task.*"}}),
	)
	defer adapterB.Close()

	wsURL := "ws" + srv.URL[4:]
	if err := adapterB.Connect(context.Background(), wsURL); err != nil {
		t.Fatalf("connect: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	var received atomic.Bool
	busA.Subscribe("llm.request", func(_ context.Context, e Event) error {
		received.Store(true)
		return nil
	})

	// Publish non-matching topic on B.
	_ = busB.Publish(context.Background(), NewEvent("llm.request", "B", nil))

	time.Sleep(200 * time.Millisecond)
	if received.Load() {
		t.Error("filter should have blocked llm.request")
	}
}

func TestNetworkAdapter_OriginIDUniqueness(t *testing.T) {
	// Verify crypto/rand-based origin IDs are unique across many instances.
	seen := make(map[string]bool, 1000)
	for range 1000 {
		b := New()
		a := NewNetworkAdapter(b)
		a.mu.RLock()
		id := a.originID
		a.mu.RUnlock()
		if seen[id] {
			t.Fatalf("duplicate originID: %s", id)
		}
		seen[id] = true
		_ = a.Close()
	}
}

func TestNetworkAdapter_LoopPrevention(t *testing.T) {
	busA := New()
	adapterA := NewNetworkAdapter(busA, WithOriginID("A"))

	srv := httptest.NewServer(adapterA.Handler())
	defer srv.Close()
	defer adapterA.Close()

	busB := New()
	adapterB := NewNetworkAdapter(busB, WithOriginID("B"))
	defer adapterB.Close()

	wsURL := "ws" + srv.URL[4:]
	if err := adapterB.Connect(context.Background(), wsURL); err != nil {
		t.Fatalf("connect: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	// Count how many times A sees the event.
	var count atomic.Int32
	busA.Subscribe("loop.test", func(_ context.Context, e Event) error {
		count.Add(1)
		return nil
	})

	// Publish on B → A receives. A should NOT re-forward back to B.
	_ = busB.Publish(context.Background(), NewEvent("loop.test", "B", nil))

	time.Sleep(300 * time.Millisecond)
	if c := count.Load(); c != 1 {
		t.Errorf("expected exactly 1 delivery, got %d (loop detected)", c)
	}
}
