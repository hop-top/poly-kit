package util

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetry_SucceedsFirstTry(t *testing.T) {
	calls := 0
	err := Retry(context.Background(), RetryConfig{MaxAttempts: 3}, func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestRetry_SucceedsThirdAttempt(t *testing.T) {
	calls := 0
	err := Retry(context.Background(), RetryConfig{
		MaxAttempts: 5,
		BaseDelay:   time.Millisecond,
	}, func() error {
		calls++
		if calls < 3 {
			return errors.New("not yet")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestRetry_RespectsMaxAttempts(t *testing.T) {
	calls := 0
	err := Retry(context.Background(), RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   time.Millisecond,
	}, func() error {
		calls++
		return errors.New("fail")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestRetry_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls int64

	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()

	err := Retry(ctx, RetryConfig{
		BaseDelay: 50 * time.Millisecond,
	}, func() error {
		atomic.AddInt64(&calls, 1)
		return errors.New("fail")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestBackoff_ExponentialGrowth(t *testing.T) {
	cfg := RetryConfig{
		BaseDelay: 10 * time.Millisecond,
		MaxDelay:  1 * time.Second,
		Jitter:    false,
	}

	prev := cfg.Backoff(0)
	for i := 1; i < 5; i++ {
		cur := cfg.Backoff(i)
		if cur < prev {
			t.Fatalf("attempt %d: %v < %v (not growing)", i, cur, prev)
		}
		prev = cur
	}
}

func TestBackoff_CapsAtMaxDelay(t *testing.T) {
	cfg := RetryConfig{
		BaseDelay: 10 * time.Millisecond,
		MaxDelay:  100 * time.Millisecond,
		Jitter:    false,
	}

	got := cfg.Backoff(100)
	if got > cfg.MaxDelay {
		t.Fatalf("Backoff(100) = %v, exceeds max %v", got, cfg.MaxDelay)
	}
}

func TestBackoff_JitterAddsRandomness(t *testing.T) {
	cfg := RetryConfig{
		BaseDelay: 100 * time.Millisecond,
		MaxDelay:  10 * time.Second,
		Jitter:    true,
	}

	seen := make(map[time.Duration]bool)
	for i := 0; i < 20; i++ {
		seen[cfg.Backoff(5)] = true
	}
	if len(seen) < 2 {
		t.Fatal("jitter produced identical values across 20 calls")
	}
}
