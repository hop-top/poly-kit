package sync

import (
	"context"
	gosync "sync"
)

// MemoryTransport is an in-memory Transport for testing.
type MemoryTransport struct {
	mu    gosync.Mutex
	diffs []Diff
	alive bool
}

// NewMemoryTransport creates a MemoryTransport ready for use.
func NewMemoryTransport() *MemoryTransport {
	return &MemoryTransport{alive: true}
}

// Push appends diffs to the in-memory buffer.
func (m *MemoryTransport) Push(_ context.Context, diffs []Diff) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.diffs = append(m.diffs, diffs...)
	return nil
}

// Pull returns all diffs whose timestamp is after since.
func (m *MemoryTransport) Pull(_ context.Context, since Timestamp) ([]Diff, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var result []Diff
	for _, d := range m.diffs {
		if since.Before(d.Timestamp) {
			result = append(result, d)
		}
	}
	return result, nil
}

// Ping returns nil if alive.
func (m *MemoryTransport) Ping(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.alive {
		return errTransportDead
	}
	return nil
}

// SetAlive controls Ping behavior for testing.
func (m *MemoryTransport) SetAlive(alive bool) {
	m.mu.Lock()
	m.alive = alive
	m.mu.Unlock()
}

// Diffs returns a copy of all stored diffs.
func (m *MemoryTransport) Diffs() []Diff {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Diff, len(m.diffs))
	copy(out, m.diffs)
	return out
}

type transportError struct{ msg string }

func (e *transportError) Error() string { return e.msg }

var errTransportDead = &transportError{"transport: not reachable"}
