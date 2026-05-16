package notify

import (
	"math"
	"testing"
	"time"
)

func TestExponentialBackoff_MonotonicNoJitter(t *testing.T) {
	t.Parallel()
	base := 100 * time.Millisecond
	b := ExponentialBackoff(base, 2.0, false)

	var prev time.Duration
	for attempt := 0; attempt <= 10; attempt++ {
		got := b(attempt)
		want := time.Duration(float64(base) * math.Pow(2.0, float64(attempt)))
		if got != want {
			t.Fatalf("attempt=%d: got %v, want %v", attempt, got, want)
		}
		if attempt > 0 && got <= prev {
			t.Fatalf("attempt=%d: got %v not strictly greater than prev %v", attempt, got, prev)
		}
		prev = got
	}
}

func TestExponentialBackoff_BoundedWithJitter(t *testing.T) {
	t.Parallel()
	base := 100 * time.Millisecond
	b := ExponentialBackoff(base, 2.0, true)

	const iterations = 1000
	for attempt := 0; attempt <= 8; attempt++ {
		upper := time.Duration(float64(base) * math.Pow(2.0, float64(attempt)))
		for i := 0; i < iterations; i++ {
			got := b(attempt)
			if got < 0 {
				t.Fatalf("attempt=%d iter=%d: got %v, want >= 0", attempt, i, got)
			}
			if got >= upper {
				t.Fatalf("attempt=%d iter=%d: got %v, want < %v", attempt, i, got, upper)
			}
		}
	}
}

func TestExponentialBackoff_FactorOne(t *testing.T) {
	t.Parallel()
	base := 100 * time.Millisecond
	b := ExponentialBackoff(base, 1.0, false)
	for attempt := 0; attempt <= 20; attempt++ {
		if got := b(attempt); got != base {
			t.Fatalf("attempt=%d: got %v, want %v", attempt, got, base)
		}
	}
}

func TestExponentialBackoff_BaseZero(t *testing.T) {
	t.Parallel()
	for _, jitter := range []bool{false, true} {
		b := ExponentialBackoff(0, 2.0, jitter)
		for attempt := 0; attempt <= 10; attempt++ {
			if got := b(attempt); got != 0 {
				t.Fatalf("jitter=%v attempt=%d: got %v, want 0", jitter, attempt, got)
			}
		}
	}
}

func TestExponentialBackoff_LargeAttemptNoPanic(t *testing.T) {
	t.Parallel()
	// attempt=1000 with factor=2.0 overflows float64 (2^1000 is well
	// past +Inf). attempt=400 with factor=10.0 likewise overflows.
	// The guard in ExponentialBackoff must cap or zero, never panic
	// or return a negative duration.
	cases := []struct {
		base    time.Duration
		factor  float64
		attempt int
		jitter  bool
	}{
		{100 * time.Millisecond, 2.0, 1000, false},
		{100 * time.Millisecond, 2.0, 1000, true},
		{100 * time.Millisecond, 10.0, 400, false},
		{time.Second, 2.0, 63, false},  // borderline: 2^63 ns is right at int64 max
		{time.Second, 2.0, 100, false}, // well past int64 max
	}
	for _, c := range cases {
		b := ExponentialBackoff(c.base, c.factor, c.jitter)
		got := b(c.attempt)
		if got < 0 {
			t.Fatalf("base=%v factor=%v attempt=%d jitter=%v: got %v, want non-negative",
				c.base, c.factor, c.attempt, c.jitter, got)
		}
		// time.Duration is finite by definition (int64), but make
		// sure we did not sneak through a wraparound.
		if got > time.Duration(math.MaxInt64) {
			t.Fatalf("got %v exceeds MaxInt64 ns", got)
		}
	}
}

func TestExponentialBackoff_NegativeBase(t *testing.T) {
	t.Parallel()
	for _, jitter := range []bool{false, true} {
		b := ExponentialBackoff(-time.Second, 2.0, jitter)
		for attempt := 0; attempt <= 5; attempt++ {
			if got := b(attempt); got != 0 {
				t.Fatalf("jitter=%v attempt=%d: got %v, want 0 (defensive on negative base)",
					jitter, attempt, got)
			}
		}
	}
}

// TestBackoffFunc_NoContextHandling is a compile-time-style
// assertion that ExponentialBackoff satisfies the BackoffFunc shape
// (int -> time.Duration). The point is to document — and lock in —
// the absence of a context parameter. Cancellation lives in the
// caller (RetrySink), per docs/specs/notifications.md §7.
func TestBackoffFunc_NoContextHandling(t *testing.T) {
	t.Parallel()
	//nolint:staticcheck // QF1011 — explicit type asserts ExponentialBackoff implements BackoffFunc
	var _ BackoffFunc = ExponentialBackoff(100*time.Millisecond, 2.0, false)
	//nolint:staticcheck // QF1011 — explicit type asserts ExponentialBackoff implements BackoffFunc
	var _ BackoffFunc = ExponentialBackoff(0, 1.0, true)

	// Sanity-check the runtime signature, too.
	b := ExponentialBackoff(time.Millisecond, 2.0, false)
	if got := b(3); got != 8*time.Millisecond {
		t.Fatalf("attempt=3 base=1ms factor=2: got %v, want 8ms", got)
	}
}
