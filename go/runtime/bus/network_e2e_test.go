package bus

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestE2E_Bidirectional(t *testing.T) {
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

	// A → B
	var bReceived atomic.Bool
	busB.Subscribe("from.a", func(_ context.Context, e Event) error {
		bReceived.Store(true)
		return nil
	})

	_ = busA.Publish(context.Background(), NewEvent("from.a", "A", "hello-b"))

	waitFor(t, &bReceived, "B should receive event from A")

	// B → A
	var aReceived atomic.Bool
	busA.Subscribe("from.b", func(_ context.Context, e Event) error {
		aReceived.Store(true)
		return nil
	})

	_ = busB.Publish(context.Background(), NewEvent("from.b", "B", "hello-a"))

	waitFor(t, &aReceived, "A should receive event from B")
}

func TestE2E_NoLoops(t *testing.T) {
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

	var countA, countB atomic.Int32
	busA.Subscribe("ping", func(_ context.Context, e Event) error {
		countA.Add(1)
		return nil
	})
	busB.Subscribe("ping", func(_ context.Context, e Event) error {
		countB.Add(1)
		return nil
	})

	// Publish on A.
	_ = busA.Publish(context.Background(), NewEvent("ping", "A", nil))

	time.Sleep(500 * time.Millisecond)

	// A sees 1 (local), B sees 1 (forwarded). No loops.
	if c := countA.Load(); c != 1 {
		t.Errorf("A count = %d, want 1", c)
	}
	if c := countB.Load(); c != 1 {
		t.Errorf("B count = %d, want 1", c)
	}
}

func TestE2E_TopicFilterSelectiveForwarding(t *testing.T) {
	// A allows only "task.*", B allows only "track.*"
	busA := New()
	adapterA := NewNetworkAdapter(busA,
		WithOriginID("A"),
		WithFilter(TopicFilter{Allow: []string{"task.*"}}),
	)

	srv := httptest.NewServer(adapterA.Handler())
	defer srv.Close()
	defer adapterA.Close()

	busB := New()
	adapterB := NewNetworkAdapter(busB,
		WithOriginID("B"),
		WithFilter(TopicFilter{Allow: []string{"track.*"}}),
	)
	defer adapterB.Close()

	wsURL := "ws" + srv.URL[4:]
	if err := adapterB.Connect(context.Background(), wsURL); err != nil {
		t.Fatalf("connect: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	var aGotTrack, aGotTask, bGotTrack, bGotTask atomic.Bool

	busA.Subscribe("track.created", func(_ context.Context, _ Event) error {
		aGotTrack.Store(true)
		return nil
	})
	busA.Subscribe("task.created", func(_ context.Context, _ Event) error {
		aGotTask.Store(true)
		return nil
	})
	busB.Subscribe("track.created", func(_ context.Context, _ Event) error {
		bGotTrack.Store(true)
		return nil
	})
	busB.Subscribe("task.created", func(_ context.Context, _ Event) error {
		bGotTask.Store(true)
		return nil
	})

	// A publishes task.created → A's filter allows "task.*" → B receives.
	_ = busA.Publish(context.Background(), NewEvent("task.created", "A", nil))
	// B publishes track.created → B's filter allows "track.*" → A receives.
	_ = busB.Publish(context.Background(), NewEvent("track.created", "B", nil))
	// A publishes track.created → A's filter blocks (only task.*) → B doesn't get it from network.
	_ = busA.Publish(context.Background(), NewEvent("track.created", "A", nil))
	// B publishes task.created → B's filter blocks (only track.*) → A doesn't get it from network.
	_ = busB.Publish(context.Background(), NewEvent("task.created", "B", nil))

	time.Sleep(500 * time.Millisecond)

	// B should get task.created from A (via network) + its own local publish.
	if !bGotTask.Load() {
		t.Error("B should receive task.created (from A's allowed forward + local)")
	}
	// A should get track.created from B (via network) + its own local publish.
	if !aGotTrack.Load() {
		t.Error("A should receive track.created (from B's allowed forward + local)")
	}
}

func waitFor(t *testing.T, flag *atomic.Bool, msg string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for !flag.Load() {
		select {
		case <-deadline:
			t.Fatalf("timeout: %s", msg)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}
