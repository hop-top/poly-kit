package sync

import (
	"sync"
	"testing"
	"time"
)

func TestSystemWallClock_MonotonicNonDecreasing(t *testing.T) {
	c := SystemWallClock{}
	prev := c.WallTime()
	for i := 0; i < 1000; i++ {
		cur := c.WallTime()
		if cur.Before(prev) {
			t.Fatalf("iteration %d: WallTime went backward: prev=%s cur=%s", i, prev, cur)
		}
		prev = cur
	}
}

func TestSystem_DefaultIsSystemWallClock(t *testing.T) {
	if _, ok := System.(SystemWallClock); !ok {
		t.Fatalf("expected System to be SystemWallClock, got %T", System)
	}
	// Sanity: System.WallTime should be close to time.Now.
	delta := time.Since(System.WallTime())
	if delta < -time.Second || delta > time.Second {
		t.Fatalf("System.WallTime drifted from time.Now by %s", delta)
	}
}

func TestFixedClock_AlwaysReturnsSameTime(t *testing.T) {
	want := time.Date(2024, 1, 2, 3, 4, 5, 6, time.UTC)
	c := FixedClock(want)
	for i := 0; i < 100; i++ {
		got := c.WallTime()
		if !got.Equal(want) {
			t.Fatalf("iteration %d: got %s want %s", i, got, want)
		}
	}
}

func TestMockWallClock_AdvanceShiftsByExactDuration(t *testing.T) {
	start := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	m := NewMockWallClock(start)
	if got := m.WallTime(); !got.Equal(start) {
		t.Fatalf("initial WallTime: got %s want %s", got, start)
	}

	cases := []time.Duration{
		time.Nanosecond,
		time.Microsecond,
		time.Millisecond,
		time.Second,
		time.Minute,
		time.Hour,
		24 * time.Hour,
	}
	cum := start
	for _, d := range cases {
		m.Advance(d)
		cum = cum.Add(d)
		if got := m.WallTime(); !got.Equal(cum) {
			t.Fatalf("after Advance(%s): got %s want %s", d, got, cum)
		}
	}
}

func TestMockWallClock_AdvanceZeroIsNoOp(t *testing.T) {
	start := time.Unix(0, 0)
	m := NewMockWallClock(start)
	m.Advance(0)
	if got := m.WallTime(); !got.Equal(start) {
		t.Fatalf("Advance(0) shifted time: got %s want %s", got, start)
	}
}

func TestMockWallClock_ConcurrentAdvanceAndRead(t *testing.T) {
	start := time.Unix(0, 0)
	m := NewMockWallClock(start)

	const writers = 16
	const reads = 1000
	const writes = 100

	var wg sync.WaitGroup
	wg.Add(writers * 2)

	for i := 0; i < writers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < writes; j++ {
				m.Advance(time.Nanosecond)
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < reads; j++ {
				_ = m.WallTime()
			}
		}()
	}
	wg.Wait()

	wantNanos := int64(writers * writes)
	got := m.WallTime().Sub(start)
	if got != time.Duration(wantNanos) {
		t.Fatalf("expected total advance %dns, got %s", wantNanos, got)
	}
}
