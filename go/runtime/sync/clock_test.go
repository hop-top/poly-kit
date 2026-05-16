package sync

import (
	"sync"
	"testing"
	"time"
)

func TestClock_Now_Monotonic(t *testing.T) {
	c := NewClock("node1")
	prev := c.Now()
	for i := 0; i < 100; i++ {
		cur := c.Now()
		if !prev.Before(cur) {
			t.Fatalf("iteration %d: %+v not before %+v", i, prev, cur)
		}
		prev = cur
	}
}

func TestClock_Now_SetsNodeID(t *testing.T) {
	c := NewClock("abc")
	ts := c.Now()
	if ts.NodeID != "abc" {
		t.Fatalf("expected NodeID abc, got %s", ts.NodeID)
	}
}

func TestClock_Update_TakesMax(t *testing.T) {
	c := NewClock("local")
	local := c.Now()

	remote := Timestamp{
		Physical: local.Physical + 1_000_000_000,
		Logical:  5,
		NodeID:   "remote",
	}

	merged := c.Update(remote)
	if merged.Physical < remote.Physical {
		t.Fatalf("merged physical %d < remote %d", merged.Physical, remote.Physical)
	}
	if !local.Before(merged) {
		t.Fatalf("local %+v not before merged %+v", local, merged)
	}
}

func TestTimestamp_Before_Ordering(t *testing.T) {
	a := Timestamp{Physical: 1, Logical: 0, NodeID: "a"}
	b := Timestamp{Physical: 2, Logical: 0, NodeID: "a"}
	if !a.Before(b) {
		t.Fatal("expected a before b by physical")
	}

	c := Timestamp{Physical: 1, Logical: 1, NodeID: "a"}
	if !a.Before(c) {
		t.Fatal("expected a before c by logical")
	}

	d := Timestamp{Physical: 1, Logical: 0, NodeID: "b"}
	if !a.Before(d) {
		t.Fatal("expected a before d by NodeID")
	}
}

func TestTimestamp_Equal(t *testing.T) {
	a := Timestamp{Physical: 10, Logical: 3, NodeID: "x"}
	b := Timestamp{Physical: 10, Logical: 3, NodeID: "x"}
	if !a.Equal(b) {
		t.Fatal("expected equal")
	}
	b.Logical = 4
	if a.Equal(b) {
		t.Fatal("expected not equal")
	}
}

func TestNewClockWithWallClock_FixedPinsPhysical(t *testing.T) {
	pinned := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	c := NewClockWithWallClock("node", FixedClock(pinned))

	first := c.Now()
	if first.Physical != pinned.UnixNano() {
		t.Fatalf("first.Physical = %d, want %d", first.Physical, pinned.UnixNano())
	}
	if first.Logical != 0 {
		t.Fatalf("first.Logical = %d, want 0", first.Logical)
	}

	// Subsequent calls keep Physical pinned and bump Logical because the
	// wall clock never advances past last.Physical.
	for i := 1; i <= 5; i++ {
		ts := c.Now()
		if ts.Physical != pinned.UnixNano() {
			t.Fatalf("call %d: Physical = %d, want %d", i, ts.Physical, pinned.UnixNano())
		}
		if ts.Logical != uint32(i) {
			t.Fatalf("call %d: Logical = %d, want %d", i, ts.Logical, i)
		}
	}
}

func TestNewClockWithWallClock_MockAdvancesPhysical(t *testing.T) {
	start := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	m := NewMockWallClock(start)
	c := NewClockWithWallClock("node", m)

	first := c.Now()
	if first.Physical != start.UnixNano() {
		t.Fatalf("first.Physical = %d, want %d", first.Physical, start.UnixNano())
	}

	m.Advance(time.Second)
	second := c.Now()
	want := start.Add(time.Second).UnixNano()
	if second.Physical != want {
		t.Fatalf("second.Physical = %d, want %d", second.Physical, want)
	}
	if second.Logical != 0 {
		t.Fatalf("second.Logical = %d, want 0 after wall advance", second.Logical)
	}
}

func TestNewClockWithWallClock_NilFallsBackToSystem(t *testing.T) {
	c := NewClockWithWallClock("node", nil)
	if c.wall == nil {
		t.Fatal("expected non-nil wall after nil fallback")
	}
	// Should produce a timestamp without panicking.
	_ = c.Now()
}

func TestClock_ConcurrentSafety(t *testing.T) {
	c := NewClock("node")
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				c.Now()
			}
		}()
	}
	wg.Wait()
}
