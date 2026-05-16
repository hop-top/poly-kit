//go:build integration

package bus

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestIntegration_FullLifecycle(t *testing.T) {
	b := New()

	// Track deliveries.
	var syncCalls atomic.Int64
	var asyncCalls atomic.Int64

	b.Subscribe("app.event", func(_ context.Context, e Event) error {
		syncCalls.Add(1)
		return nil
	})
	b.SubscribeAsync("app.event", func(_ context.Context, e Event) {
		asyncCalls.Add(1)
	})

	// Publish several events.
	for i := 0; i < 5; i++ {
		if err := b.Publish(context.Background(), NewEvent("app.event", "test", i)); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}

	// Close and wait for async handlers.
	if err := b.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}

	if got := syncCalls.Load(); got != 5 {
		t.Errorf("sync calls = %d, want 5", got)
	}
	if got := asyncCalls.Load(); got != 5 {
		t.Errorf("async calls = %d, want 5", got)
	}

	// Publish after close must fail.
	err := b.Publish(context.Background(), NewEvent("app.event", "test", nil))
	if !errors.Is(err, ErrBusClosed) {
		t.Errorf("publish after close: got %v, want ErrBusClosed", err)
	}
}

func TestIntegration_VetoBlocksAsync(t *testing.T) {
	b := New()

	veto := errors.New("vetoed")
	var asyncFired atomic.Bool

	b.Subscribe("order.create", func(_ context.Context, _ Event) error {
		return veto
	})
	b.SubscribeAsync("order.create", func(_ context.Context, _ Event) {
		asyncFired.Store(true)
	})

	err := b.Publish(context.Background(), NewEvent("order.create", "test", nil))
	if !errors.Is(err, veto) {
		t.Fatalf("expected veto, got %v", err)
	}

	// Give async a moment to fire (it should NOT).
	time.Sleep(20 * time.Millisecond)
	if asyncFired.Load() {
		t.Error("async handler fired despite sync veto")
	}

	_ = b.Close(context.Background())
}

func TestIntegration_MixedWildcard(t *testing.T) {
	b := New()

	type result struct {
		pattern string
		topic   Topic
	}
	var mu sync.Mutex
	var results []result

	record := func(pattern string) Handler {
		return func(_ context.Context, e Event) error {
			mu.Lock()
			results = append(results, result{pattern, e.Topic})
			mu.Unlock()
			return nil
		}
	}

	b.Subscribe("app.user.login", record("exact"))
	b.Subscribe("app.user.*", record("star"))
	b.Subscribe("app.#", record("hash"))
	b.Subscribe("other.topic", record("other"))

	topics := []Topic{"app.user.login", "app.user.logout", "app.config.update", "other.topic"}
	for _, tp := range topics {
		if err := b.Publish(context.Background(), NewEvent(tp, "test", nil)); err != nil {
			t.Fatalf("publish %s: %v", tp, err)
		}
	}

	_ = b.Close(context.Background())

	// Build delivery matrix: pattern -> set of topics received.
	matrix := make(map[string]map[Topic]bool)
	mu.Lock()
	for _, r := range results {
		if matrix[r.pattern] == nil {
			matrix[r.pattern] = make(map[Topic]bool)
		}
		matrix[r.pattern][r.topic] = true
	}
	mu.Unlock()

	// exact: only app.user.login
	if got := matrix["exact"]; len(got) != 1 || !got["app.user.login"] {
		t.Errorf("exact: got %v", got)
	}
	// star: app.user.login + app.user.logout
	if got := matrix["star"]; len(got) != 2 {
		t.Errorf("star: got %v, want 2 topics", got)
	}
	// hash: app.user.login, app.user.logout, app.config.update
	if got := matrix["hash"]; len(got) != 3 {
		t.Errorf("hash: got %v, want 3 topics", got)
	}
	// other: only other.topic
	if got := matrix["other"]; len(got) != 1 || !got["other.topic"] {
		t.Errorf("other: got %v", got)
	}
}

func TestIntegration_ConcurrentStress(t *testing.T) {
	b := New()

	var delivered atomic.Int64

	// Spin up 10 goroutines that subscribe/unsubscribe.
	var subWg sync.WaitGroup
	for i := 0; i < 10; i++ {
		subWg.Add(1)
		go func() {
			defer subWg.Done()
			for j := 0; j < 20; j++ {
				unsub := b.Subscribe("stress.*", func(_ context.Context, _ Event) error {
					delivered.Add(1)
					return nil
				})
				unsub()
			}
		}()
	}

	// Spin up 50 goroutines that publish.
	var pubWg sync.WaitGroup
	for i := 0; i < 50; i++ {
		pubWg.Add(1)
		go func(n int) {
			defer pubWg.Done()
			for j := 0; j < 10; j++ {
				_ = b.Publish(context.Background(), NewEvent("stress.event", "test", n))
			}
		}(i)
	}

	pubWg.Wait()
	subWg.Wait()

	// No panics or races is the primary assertion (run with -race).
	_ = b.Close(context.Background())
	t.Logf("delivered %d events under stress", delivered.Load())
}

func TestIntegration_CloseDrainUnderLoad(t *testing.T) {
	b := New()

	var completed atomic.Int64
	const numEvents = 200

	b.SubscribeAsync("drain.event", func(_ context.Context, _ Event) {
		time.Sleep(time.Millisecond)
		completed.Add(1)
	})

	// Publish many async events.
	for i := 0; i < numEvents; i++ {
		if err := b.Publish(context.Background(), NewEvent("drain.event", "test", i)); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}

	// Close with generous deadline — all should drain.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := b.Close(ctx)
	if err != nil {
		t.Fatalf("close: %v (completed %d/%d)", err, completed.Load(), numEvents)
	}

	if got := completed.Load(); got != numEvents {
		t.Errorf("completed = %d, want %d", got, numEvents)
	}

	// Verify with a tight deadline — should hit context error.
	b2 := New()
	b2.SubscribeAsync("drain.event", func(_ context.Context, _ Event) {
		time.Sleep(time.Second)
	})
	_ = b2.Publish(context.Background(), NewEvent("drain.event", "test", nil))

	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel2()

	err = b2.Close(ctx2)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded on tight close, got %v", err)
	}
}
