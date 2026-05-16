package bus

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublish_SyncHandler(t *testing.T) {
	b := New()
	var got Event
	b.Subscribe("test.event", func(_ context.Context, e Event) error {
		got = e
		return nil
	})

	e := NewEvent("test.event", "src", "hello")
	if err := b.Publish(context.Background(), e); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if got.Source != "src" {
		t.Errorf("source = %q, want src", got.Source)
	}
}

func TestPublish_SyncVeto(t *testing.T) {
	b := New()
	veto := errors.New("nope")
	b.Subscribe("test.event", func(_ context.Context, _ Event) error {
		return veto
	})

	var secondCalled bool
	b.Subscribe("test.event", func(_ context.Context, _ Event) error {
		secondCalled = true
		return nil
	})

	err := b.Publish(context.Background(), NewEvent("test.event", "src", nil))
	if !errors.Is(err, veto) {
		t.Errorf("expected veto error, got %v", err)
	}
	if secondCalled {
		t.Error("second handler should not run after veto")
	}
}

func TestPublish_AsyncHandler(t *testing.T) {
	b := New()
	var called atomic.Bool
	b.SubscribeAsync("test.event", func(_ context.Context, _ Event) {
		called.Store(true)
	})

	if err := b.Publish(context.Background(), NewEvent("test.event", "src", nil)); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// Wait for async handler.
	_ = b.Close(context.Background())
	if !called.Load() {
		t.Error("async handler was not called")
	}
}

func TestPublish_AsyncDoesNotBlockPublisher(t *testing.T) {
	b := New()
	started := make(chan struct{})
	b.SubscribeAsync("test.event", func(_ context.Context, _ Event) {
		close(started)
		time.Sleep(100 * time.Millisecond)
	})

	if err := b.Publish(context.Background(), NewEvent("test.event", "src", nil)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	<-started // async handler started; Publish already returned
}

func TestUnsubscribe(t *testing.T) {
	b := New()
	var count int
	unsub := b.Subscribe("test.event", func(_ context.Context, _ Event) error {
		count++
		return nil
	})

	_ = b.Publish(context.Background(), NewEvent("test.event", "src", nil))
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}

	unsub()
	_ = b.Publish(context.Background(), NewEvent("test.event", "src", nil))
	if count != 1 {
		t.Errorf("count = %d, want 1 after unsubscribe", count)
	}
}

func TestPublish_WildcardSubscribe(t *testing.T) {
	b := New()
	var got []string

	b.Subscribe("llm.*", func(_ context.Context, e Event) error {
		got = append(got, string(e.Topic))
		return nil
	})

	_ = b.Publish(context.Background(), NewEvent("llm.request", "src", nil))
	_ = b.Publish(context.Background(), NewEvent("llm.response", "src", nil))
	_ = b.Publish(context.Background(), NewEvent("tool.exec", "src", nil)) // should not match

	if len(got) != 2 {
		t.Fatalf("expected 2 events, got %d: %v", len(got), got)
	}
}

func TestPublish_HashWildcard(t *testing.T) {
	b := New()
	var got []string

	b.Subscribe("llm.#", func(_ context.Context, e Event) error {
		got = append(got, string(e.Topic))
		return nil
	})

	_ = b.Publish(context.Background(), NewEvent("llm.request", "src", nil))
	_ = b.Publish(context.Background(), NewEvent("llm.request.start", "src", nil))
	_ = b.Publish(context.Background(), NewEvent("llm", "src", nil))
	_ = b.Publish(context.Background(), NewEvent("tool.exec", "src", nil))

	if len(got) != 3 {
		t.Fatalf("expected 3 events, got %d: %v", len(got), got)
	}
}

func TestPublish_ExactDoesNotMatchOther(t *testing.T) {
	b := New()
	var called bool
	b.Subscribe("llm.request", func(_ context.Context, _ Event) error {
		called = true
		return nil
	})

	_ = b.Publish(context.Background(), NewEvent("llm.response", "src", nil))
	if called {
		t.Error("exact subscribe should not match different topic")
	}
}

func TestConcurrentPublishSubscribe(t *testing.T) {
	b := New()
	var count atomic.Int64

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unsub := b.Subscribe("test.event", func(_ context.Context, _ Event) error {
				count.Add(1)
				return nil
			})
			_ = b.Publish(context.Background(), NewEvent("test.event", "src", nil))
			unsub()
		}()
	}
	wg.Wait()

	if count.Load() == 0 {
		t.Error("expected some handler calls from concurrent access")
	}
}

func TestClose_RejectsPublish(t *testing.T) {
	b := New()
	_ = b.Close(context.Background())

	err := b.Publish(context.Background(), NewEvent("test", "src", nil))
	if !errors.Is(err, ErrBusClosed) {
		t.Errorf("expected ErrBusClosed, got %v", err)
	}
}

func TestClose_WaitsForAsync(t *testing.T) {
	b := New()
	done := make(chan struct{})

	b.SubscribeAsync("test.event", func(_ context.Context, _ Event) {
		time.Sleep(50 * time.Millisecond)
		close(done)
	})

	_ = b.Publish(context.Background(), NewEvent("test.event", "src", nil))
	if err := b.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}

	select {
	case <-done:
	default:
		t.Error("Close should have waited for async handler")
	}
}

func TestClose_RespectsDeadline(t *testing.T) {
	b := New()

	b.SubscribeAsync("test.event", func(_ context.Context, _ Event) {
		time.Sleep(5 * time.Second) // intentionally long
	})

	_ = b.Publish(context.Background(), NewEvent("test.event", "src", nil))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := b.Close(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
}

func TestAsyncPool_BoundedGoroutines(t *testing.T) {
	const (
		maxAsync = 8
		events   = 200
	)

	b := New(WithMaxAsync(maxAsync))

	var peak atomic.Int64
	var active atomic.Int64

	b.SubscribeAsync("load.#", func(_ context.Context, _ Event) {
		cur := active.Add(1)
		// track peak concurrency
		for {
			old := peak.Load()
			if cur <= old || peak.CompareAndSwap(old, cur) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		active.Add(-1)
	})

	for i := 0; i < events; i++ {
		if err := b.Publish(context.Background(), NewEvent("load.test", "src", nil)); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}

	if err := b.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}

	if p := peak.Load(); p > int64(maxAsync) {
		t.Errorf("peak concurrency = %d, want <= %d", p, maxAsync)
	}
}

func TestWithMaxAsync_DefaultBounds(t *testing.T) {
	// Default bus without WithMaxAsync uses defaultMaxAsync (256).
	b := New().(*memBus)
	if cap(b.sem) != defaultMaxAsync {
		t.Errorf("default semaphore cap = %d, want %d", cap(b.sem), defaultMaxAsync)
	}
}

func TestWithMaxAsync_CustomValue(t *testing.T) {
	b := New(WithMaxAsync(32)).(*memBus)
	if cap(b.sem) != 32 {
		t.Errorf("semaphore cap = %d, want 32", cap(b.sem))
	}
}

func TestWithMaxAsync_ZeroFallsBackToDefault(t *testing.T) {
	b := New(WithMaxAsync(0)).(*memBus)
	if cap(b.sem) != defaultMaxAsync {
		t.Errorf("semaphore cap = %d, want %d", cap(b.sem), defaultMaxAsync)
	}
}
