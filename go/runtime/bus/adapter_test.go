package bus

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestMemoryAdapter_ImplementsAdapter(t *testing.T) {
	var _ Adapter = NewMemoryAdapter()
}

func TestMemoryAdapter_PublishSubscribe(t *testing.T) {
	a := NewMemoryAdapter()
	var got Event
	a.Subscribe("test.event", func(_ context.Context, e Event) error {
		got = e
		return nil
	})

	e := NewEvent("test.event", "src", "hello")
	if err := a.Publish(context.Background(), e); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if got.Source != "src" {
		t.Errorf("source = %q, want src", got.Source)
	}
}

func TestMemoryAdapter_AsyncHandler(t *testing.T) {
	a := NewMemoryAdapter()
	var called atomic.Bool
	a.SubscribeAsync("test.event", func(_ context.Context, _ Event) {
		called.Store(true)
	})

	if err := a.Publish(context.Background(), NewEvent("test.event", "src", nil)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	_ = a.Close(context.Background())
	if !called.Load() {
		t.Error("async handler was not called")
	}
}

func TestWithAdapter_MemoryAdapter(t *testing.T) {
	b := New(WithAdapter(NewMemoryAdapter()))
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
	_ = b.Close(context.Background())
}

func TestNew_DefaultStillWorks(t *testing.T) {
	b := New()
	var count int
	b.Subscribe("x", func(_ context.Context, _ Event) error {
		count++
		return nil
	})
	_ = b.Publish(context.Background(), NewEvent("x", "src", nil))
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
	_ = b.Close(context.Background())
}

func TestSQLiteAdapter_ImplementsAdapter(t *testing.T) {
	path := t.TempDir() + "/test.db"
	a, err := NewSQLiteAdapter(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	var _ Adapter = a
	_ = a.Close(context.Background())
}

func TestSQLiteAdapter_PublishAndDeliver(t *testing.T) {
	path := t.TempDir() + "/test.db"
	a, err := NewSQLiteAdapter(path, WithPollInterval(10*time.Millisecond))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer a.Close(context.Background())

	var got atomic.Value
	a.Subscribe("test.event", func(_ context.Context, e Event) error {
		got.Store(e)
		return nil
	})

	e := NewEvent("test.event", "src", "hello")
	if err := a.Publish(context.Background(), e); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// Wait for polling to deliver.
	deadline := time.After(2 * time.Second)
	for {
		if v := got.Load(); v != nil {
			ev := v.(Event)
			if ev.Source != "src" {
				t.Errorf("source = %q, want src", ev.Source)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for event delivery")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestSQLiteAdapter_WildcardSubscribe(t *testing.T) {
	path := t.TempDir() + "/test.db"
	a, err := NewSQLiteAdapter(path, WithPollInterval(10*time.Millisecond))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer a.Close(context.Background())

	var count atomic.Int64
	a.Subscribe("llm.*", func(_ context.Context, _ Event) error {
		count.Add(1)
		return nil
	})

	_ = a.Publish(context.Background(), NewEvent("llm.request", "src", nil))
	_ = a.Publish(context.Background(), NewEvent("llm.response", "src", nil))
	_ = a.Publish(context.Background(), NewEvent("tool.exec", "src", nil))

	deadline := time.After(2 * time.Second)
	for {
		if count.Load() >= 2 {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("delivered %d, want 2", count.Load())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestSQLiteAdapter_ClosePreventsPublish(t *testing.T) {
	path := t.TempDir() + "/test.db"
	a, err := NewSQLiteAdapter(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = a.Close(context.Background())

	err = a.Publish(context.Background(), NewEvent("test", "src", nil))
	if err != ErrBusClosed {
		t.Errorf("expected ErrBusClosed, got %v", err)
	}
}

func TestSQLiteAdapter_AsyncHandler(t *testing.T) {
	path := t.TempDir() + "/test.db"
	a, err := NewSQLiteAdapter(path, WithPollInterval(10*time.Millisecond))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer a.Close(context.Background())

	var called atomic.Bool
	a.SubscribeAsync("test.event", func(_ context.Context, _ Event) {
		called.Store(true)
	})

	_ = a.Publish(context.Background(), NewEvent("test.event", "src", nil))

	deadline := time.After(2 * time.Second)
	for {
		if called.Load() {
			return
		}
		select {
		case <-deadline:
			t.Fatal("async handler not called")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestSQLiteAdapter_Unsubscribe(t *testing.T) {
	path := t.TempDir() + "/test.db"
	a, err := NewSQLiteAdapter(path, WithPollInterval(10*time.Millisecond))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer a.Close(context.Background())

	var count atomic.Int64
	unsub := a.Subscribe("test.event", func(_ context.Context, _ Event) error {
		count.Add(1)
		return nil
	})

	_ = a.Publish(context.Background(), NewEvent("test.event", "src", nil))

	// Wait for first delivery.
	deadline := time.After(2 * time.Second)
	for count.Load() < 1 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for first event")
		case <-time.After(5 * time.Millisecond):
		}
	}

	unsub()

	_ = a.Publish(context.Background(), NewEvent("test.event", "src", nil))
	time.Sleep(50 * time.Millisecond) // give poller time
	if count.Load() > 1 {
		t.Errorf("received %d events after unsubscribe, want 1", count.Load())
	}
}

func TestWithAdapter_SQLite(t *testing.T) {
	path := t.TempDir() + "/test.db"
	a, err := NewSQLiteAdapter(path, WithPollInterval(10*time.Millisecond))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	b := New(WithAdapter(a))
	defer b.Close(context.Background())

	var got atomic.Value
	b.Subscribe("test.event", func(_ context.Context, e Event) error {
		got.Store(e)
		return nil
	})

	_ = b.Publish(context.Background(), NewEvent("test.event", "src", "via-bus"))

	deadline := time.After(2 * time.Second)
	for {
		if v := got.Load(); v != nil {
			return
		}
		select {
		case <-deadline:
			t.Fatal("timed out")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestNew_TypedNilAdapterFallsBack(t *testing.T) {
	b := New(WithAdapter((*SQLiteAdapter)(nil)))
	var count int
	b.Subscribe("x", func(_ context.Context, _ Event) error {
		count++
		return nil
	})
	_ = b.Publish(context.Background(), NewEvent("x", "src", nil))
	if count != 1 {
		t.Errorf("count = %d, want 1 (typed-nil should fall back to memory)", count)
	}
	_ = b.Close(context.Background())
}

// --- Regression tests ---

func TestSQLiteAdapter_HandlerCanUnsubscribe(t *testing.T) {
	path := t.TempDir() + "/test.db"
	a, err := NewSQLiteAdapter(path, WithPollInterval(10*time.Millisecond))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer a.Close(context.Background())

	var delivered atomic.Bool
	var unsub Unsubscribe
	unsub = a.Subscribe("test.event", func(_ context.Context, _ Event) error {
		unsub() // calls back into adapter while handler is running
		delivered.Store(true)
		return nil
	})

	if err := a.Publish(context.Background(), NewEvent("test.event", "src", nil)); err != nil {
		t.Fatalf("publish: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for !delivered.Load() {
		select {
		case <-deadline:
			t.Fatal("timed out — possible deadlock in deliverPending")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestSQLiteAdapter_CloseWaitsForAsync(t *testing.T) {
	path := t.TempDir() + "/test.db"
	a, err := NewSQLiteAdapter(path, WithPollInterval(10*time.Millisecond))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	var completed atomic.Bool
	a.SubscribeAsync("test.event", func(_ context.Context, _ Event) {
		time.Sleep(100 * time.Millisecond)
		completed.Store(true)
	})

	if err := a.Publish(context.Background(), NewEvent("test.event", "src", nil)); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// Wait for poll to pick up the event.
	deadline := time.After(2 * time.Second)
poll:
	for !completed.Load() {
		select {
		case <-deadline:
			break poll
		case <-time.After(5 * time.Millisecond):
		}
	}

	// Close should wait for async handler to finish.
	_ = a.Close(context.Background())

	if !completed.Load() {
		t.Error("async handler did not complete before Close returned")
	}
}
