//go:build integration

package bus

import (
	"context"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// --- Fix 1: Close/Publish WaitGroup race ---
// Publish holds RLock through wg.Add; Close acquires write lock before
// setting closed. This test must pass under -race with no panics.

func TestClosePublishRace(t *testing.T) {
	b := New()

	var handled atomic.Int64
	b.SubscribeAsync("#", func(_ context.Context, _ Event) {
		time.Sleep(time.Millisecond)
		handled.Add(1)
	})

	const n = 100
	for i := range n {
		go func() {
			_ = b.Publish(context.Background(), NewEvent(
				Topic("race.test"), "regression", i,
			))
		}()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := b.Close(ctx)
	if err != nil && err != context.DeadlineExceeded {
		t.Fatalf("Close returned unexpected error: %v", err)
	}

	t.Logf("async handlers completed: %d", handled.Load())
}

// --- Fix 2: New() takes no args ---
// If someone re-adds ErrFunc param, this compile-time regression breaks.

func TestNewNoArgs(t *testing.T) {
	b := New()

	var got Event
	b.Subscribe("test.topic", func(_ context.Context, e Event) error {
		got = e
		return nil
	})

	ev := NewEvent(Topic("test.topic"), "regression", "payload")
	if err := b.Publish(context.Background(), ev); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if got.Topic != "test.topic" {
		t.Fatalf("expected topic test.topic, got %s", got.Topic)
	}

	if err := b.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// --- Fix 3: # must be final segment (MQTT convention) ---

func TestHashMustBeFinalSegment(t *testing.T) {
	tests := []struct {
		topic   Topic
		pattern string
		want    bool
	}{
		{"a.b.c", "a.#.c", false}, // # not last → reject
		{"a.b.c", "a.#", true},    // # last → match trailing
		{"a", "#", true},          // # alone → match everything
		{"a.b", "#.b", false},     // # not last → reject
	}

	for _, tt := range tests {
		got := tt.topic.Match(tt.pattern)
		if got != tt.want {
			t.Errorf("Topic(%q).Match(%q) = %v, want %v",
				tt.topic, tt.pattern, got, tt.want)
		}
	}
}

// --- Fix 4: JSONLSinkFile mode 0o600 ---

func TestJSONLSinkFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file mode not meaningful on Windows")
	}

	path := t.TempDir() + "/events.jsonl"
	sink, err := NewJSONLSinkFile(path)
	if err != nil {
		t.Fatalf("NewJSONLSinkFile: %v", err)
	}
	defer sink.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}

	mode := info.Mode().Perm()
	if mode != 0o600 {
		t.Errorf("file mode = %o, want 0600", mode)
	}
}

// --- Fix 5: truncate docstring accuracy ---

func TestTruncateBehavior(t *testing.T) {
	long := strings.Repeat("x", 130)
	result := truncate(long, 120)
	if len([]rune(result)) != 123 {
		t.Errorf("truncate(130 runes, 120) length = %d, want 123 (120 + '...')",
			len([]rune(result)))
	}
	if !strings.HasSuffix(result, "...") {
		t.Error("truncated string should end with '...'")
	}

	short := strings.Repeat("y", 50)
	if truncate(short, 120) != short {
		t.Error("string within limit should be returned unchanged")
	}

	if truncate("", 120) != "" {
		t.Error("empty string should be returned unchanged")
	}
}

// --- Fix 6: bounded async goroutine pool (T-0735) ---

// Peak concurrency must never exceed pool size.
func TestRegressionPoolPeakConcurrencyBounded(t *testing.T) {
	const (
		poolSize = 4
		events   = 500
	)

	b := New(WithMaxAsync(poolSize))

	var peak atomic.Int64
	var active atomic.Int64

	b.SubscribeAsync("#", func(_ context.Context, _ Event) {
		cur := active.Add(1)
		for {
			old := peak.Load()
			if cur <= old || peak.CompareAndSwap(old, cur) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		active.Add(-1)
	})

	for i := range events {
		if err := b.Publish(context.Background(), NewEvent("pool.test", "regression", i)); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}

	if err := b.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}

	if p := peak.Load(); p > int64(poolSize) {
		t.Errorf("peak concurrency = %d, want <= %d", p, poolSize)
	}
	t.Logf("peak concurrency: %d (limit %d)", peak.Load(), poolSize)
}

// All events must be delivered; no drops under saturation.
func TestRegressionPoolAllEventsDelivered(t *testing.T) {
	const (
		poolSize = 4
		events   = 500
	)

	b := New(WithMaxAsync(poolSize))

	var delivered atomic.Int64

	b.SubscribeAsync("#", func(_ context.Context, _ Event) {
		time.Sleep(10 * time.Millisecond)
		delivered.Add(1)
	})

	for i := range events {
		if err := b.Publish(context.Background(), NewEvent("deliver.test", "regression", i)); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}

	if err := b.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}

	if d := delivered.Load(); d != int64(events) {
		t.Errorf("delivered = %d, want %d", d, events)
	}
}

// Close during pool saturation must complete without deadlock.
// Proves priority inversion fix: RLock released before sem acquire.
func TestRegressionCloseDuringSaturation(t *testing.T) {
	const poolSize = 4

	b := New(WithMaxAsync(poolSize))

	started := make(chan struct{})
	block := make(chan struct{})

	// Fill the pool with blocking handlers.
	b.SubscribeAsync("#", func(_ context.Context, _ Event) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-block
	})

	// Saturate the pool.
	for i := range poolSize {
		if err := b.Publish(context.Background(), NewEvent("sat.test", "regression", i)); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
		<-started // wait for handler to be running
	}

	// Close in background; must not deadlock.
	closeDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		closeDone <- b.Close(ctx)
	}()

	// Unblock all handlers after short delay.
	time.Sleep(50 * time.Millisecond)
	close(block)

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close deadlocked during pool saturation")
	}
}

// After Close returns, no new async handlers should launch.
func TestRegressionCloseStopsNewGoroutines(t *testing.T) {
	b := New(WithMaxAsync(4))

	var postClose atomic.Int64

	b.SubscribeAsync("#", func(_ context.Context, _ Event) {
		postClose.Add(1)
	})

	if err := b.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Publish after close should be rejected; no handler invocations.
	for i := range 50 {
		_ = b.Publish(context.Background(), NewEvent("post.close", "regression", i))
	}

	// Small window for any leaked goroutines to execute.
	time.Sleep(50 * time.Millisecond)

	if n := postClose.Load(); n != 0 {
		t.Errorf("handlers invoked after Close: %d, want 0", n)
	}
}

// WithMaxAsync(0) falls back to default; bus must not panic.
func TestRegressionWithMaxAsyncZeroNoPanic(t *testing.T) {
	b := New(WithMaxAsync(0))

	var delivered atomic.Int64
	b.SubscribeAsync("#", func(_ context.Context, _ Event) {
		delivered.Add(1)
	})

	for i := range 10 {
		if err := b.Publish(context.Background(), NewEvent("zero.pool", "regression", i)); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}

	if err := b.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}

	if d := delivered.Load(); d != 10 {
		t.Errorf("delivered = %d, want 10", d)
	}

	// Verify default pool size was used.
	mb := New(WithMaxAsync(0)).(*memBus)
	if cap(mb.sem) != defaultMaxAsync {
		t.Errorf("semaphore cap = %d, want %d", cap(mb.sem), defaultMaxAsync)
	}
}
