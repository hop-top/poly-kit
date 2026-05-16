package bus

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestNetworkAdapter_Relay_3Node_Fanout verifies star-topology relay
// (T-0182): hub re-forwards inbound network events to peers OTHER than
// the origin. Without WithRelay, A→hub→B is broken because hub's
// outbound forwarder skips network-tagged events. With WithRelay, B
// must receive A's publish through the hub.
func TestNetworkAdapter_Relay_3Node_Fanout(t *testing.T) {
	// Hub: relay enabled.
	hubBus := New()
	hub := NewNetworkAdapter(hubBus, WithOriginID("hub"), WithRelay(true))
	srv := httptest.NewServer(hub.Handler())
	defer srv.Close()
	defer hub.Close()

	wsURL := "ws" + srv.URL[4:]

	// Peer A — publisher.
	busA := New()
	adapterA := NewNetworkAdapter(busA, WithOriginID("peer-A"))
	defer adapterA.Close()
	if err := adapterA.Connect(context.Background(), wsURL); err != nil {
		t.Fatalf("A connect: %v", err)
	}

	// Peer B — subscriber.
	busB := New()
	adapterB := NewNetworkAdapter(busB, WithOriginID("peer-B"))
	defer adapterB.Close()
	if err := adapterB.Connect(context.Background(), wsURL); err != nil {
		t.Fatalf("B connect: %v", err)
	}

	// Allow connections + first messages (peerOriginID learning) to settle.
	time.Sleep(150 * time.Millisecond)

	var bGot atomic.Int32
	busB.Subscribe("test.ping", func(_ context.Context, _ Event) error {
		bGot.Add(1)
		return nil
	})

	// A publishes — hub must relay to B (excluding A).
	if err := busA.Publish(context.Background(), NewEvent("test.ping", "A", "hello")); err != nil {
		t.Fatalf("A publish: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for bGot.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("relay: B did not receive A's event through hub")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Settle for any phantom re-deliveries.
	time.Sleep(200 * time.Millisecond)
	if c := bGot.Load(); c != 1 {
		t.Errorf("relay fanout: expected exactly 1 delivery to B, got %d", c)
	}
}

// TestNetworkAdapter_Relay_OriginExcluded verifies relay excludes the
// origin peer: A's published event must NOT echo back to A even though
// the hub re-forwards inbound events.
func TestNetworkAdapter_Relay_OriginExcluded(t *testing.T) {
	hubBus := New()
	hub := NewNetworkAdapter(hubBus, WithOriginID("hub"), WithRelay(true))
	srv := httptest.NewServer(hub.Handler())
	defer srv.Close()
	defer hub.Close()

	wsURL := "ws" + srv.URL[4:]

	busA := New()
	adapterA := NewNetworkAdapter(busA, WithOriginID("peer-A"))
	defer adapterA.Close()
	if err := adapterA.Connect(context.Background(), wsURL); err != nil {
		t.Fatalf("A connect: %v", err)
	}

	busB := New()
	adapterB := NewNetworkAdapter(busB, WithOriginID("peer-B"))
	defer adapterB.Close()
	if err := adapterB.Connect(context.Background(), wsURL); err != nil {
		t.Fatalf("B connect: %v", err)
	}

	time.Sleep(150 * time.Millisecond)

	var aGot atomic.Int32
	busA.Subscribe("echo.test", func(_ context.Context, _ Event) error {
		aGot.Add(1)
		return nil
	})

	_ = busA.Publish(context.Background(), NewEvent("echo.test", "A", nil))

	time.Sleep(400 * time.Millisecond)
	// A sees its own local publish (count=1) but never the relayed copy.
	if c := aGot.Load(); c != 1 {
		t.Errorf("origin exclusion: A count = %d, want 1 (local only, no echo)", c)
	}
}

// TestNetworkAdapter_Relay_Disabled_Default confirms the default off
// behavior: without WithRelay, hub does NOT re-forward A→B.
func TestNetworkAdapter_Relay_Disabled_Default(t *testing.T) {
	hubBus := New()
	hub := NewNetworkAdapter(hubBus, WithOriginID("hub")) // relay off
	srv := httptest.NewServer(hub.Handler())
	defer srv.Close()
	defer hub.Close()

	wsURL := "ws" + srv.URL[4:]

	busA := New()
	adapterA := NewNetworkAdapter(busA, WithOriginID("peer-A"))
	defer adapterA.Close()
	if err := adapterA.Connect(context.Background(), wsURL); err != nil {
		t.Fatalf("A connect: %v", err)
	}

	busB := New()
	adapterB := NewNetworkAdapter(busB, WithOriginID("peer-B"))
	defer adapterB.Close()
	if err := adapterB.Connect(context.Background(), wsURL); err != nil {
		t.Fatalf("B connect: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	var bGot atomic.Int32
	busB.Subscribe("noreplay.ping", func(_ context.Context, _ Event) error {
		bGot.Add(1)
		return nil
	})

	_ = busA.Publish(context.Background(), NewEvent("noreplay.ping", "A", nil))

	time.Sleep(300 * time.Millisecond)
	if c := bGot.Load(); c != 0 {
		t.Errorf("relay-off default: B should not receive A's event, got %d", c)
	}
}
