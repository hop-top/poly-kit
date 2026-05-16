package sync

import (
	"sync"
	"time"
)

// WallClock abstracts the source of physical wall-clock time so callers can
// substitute deterministic implementations in tests.
//
// WallClock is intentionally distinct from the hybrid logical clock exposed
// by [Clock]. [Clock] returns a [Timestamp] (Physical+Logical+NodeID) suited
// for causal ordering across nodes; WallClock returns a plain [time.Time]
// representing only the wall instant. Production code typically uses the
// [System] singleton (which wraps [time.Now]); tests typically use
// [FixedClock] or [MockWallClock] to pin or advance time deterministically.
type WallClock interface {
	// WallTime returns the current wall-clock time.
	WallTime() time.Time
}

// SystemWallClock is the production [WallClock] implementation backed by
// [time.Now]. Its zero value is ready to use.
type SystemWallClock struct{}

// WallTime returns time.Now().
func (SystemWallClock) WallTime() time.Time { return time.Now() }

// System is a process-wide [WallClock] backed by [time.Now]. It is the
// default clock used by constructors that do not accept a WallClock.
var System WallClock = SystemWallClock{}

// FixedClock returns a [WallClock] that always reports t. Useful for tests
// that need a stable, non-advancing clock.
func FixedClock(t time.Time) WallClock {
	return fixedClock{t: t}
}

type fixedClock struct {
	t time.Time
}

func (f fixedClock) WallTime() time.Time { return f.t }

// MockWallClock is a thread-safe [WallClock] whose reported time can be
// advanced explicitly via [MockWallClock.Advance]. It is intended for tests
// that need to simulate the passage of time deterministically.
type MockWallClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewMockWallClock returns a [MockWallClock] initialized to start.
func NewMockWallClock(start time.Time) *MockWallClock {
	return &MockWallClock{now: start}
}

// WallTime returns the current mock time.
func (m *MockWallClock) WallTime() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.now
}

// Advance shifts the mock clock forward by d.
func (m *MockWallClock) Advance(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.now = m.now.Add(d)
}
