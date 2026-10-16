package bus

import (
	"context"
	"sync/atomic"
	"testing"
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

// nopAdapter delivers nothing; a typed-nil *nopAdapter stands for an
// adapter constructor that failed and returned nil.
type nopAdapter struct{}

func (*nopAdapter) Publish(context.Context, Event) error            { return nil }
func (*nopAdapter) Subscribe(string, Handler) Unsubscribe           { return func() {} }
func (*nopAdapter) SubscribeAsync(string, AsyncHandler) Unsubscribe { return func() {} }
func (*nopAdapter) Close(context.Context) error                     { return nil }

func TestNew_TypedNilAdapterFallsBack(t *testing.T) {
	b := New(WithAdapter((*nopAdapter)(nil)))
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
