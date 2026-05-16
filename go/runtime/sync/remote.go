package sync

import (
	"context"
	"errors"
	gosync "sync"
)

// SyncMode controls the direction of replication for a remote.
type SyncMode int

const (
	Bidirectional SyncMode = iota
	PushOnly
	PullOnly
)

// Transport is the push/pull interface (forward declaration for phase 2).
type Transport interface {
	Push(ctx context.Context, diffs []Diff) error
	Pull(ctx context.Context, since Timestamp) ([]Diff, error)
	Ping(ctx context.Context) error
}

// Remote represents a named replication endpoint with mode and filter.
type Remote struct {
	Name      string
	Transport Transport
	Mode      SyncMode
	Filter    func(Diff) bool
}

// RemoteSet manages a concurrent-safe collection of named remotes.
type RemoteSet struct {
	mu      gosync.RWMutex
	remotes map[string]*Remote
}

// NewRemoteSet returns an empty RemoteSet.
func NewRemoteSet() *RemoteSet {
	return &RemoteSet{remotes: make(map[string]*Remote)}
}

// Add registers a remote. Returns an error if the name already exists.
func (rs *RemoteSet) Add(r Remote) error {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if _, exists := rs.remotes[r.Name]; exists {
		return errors.New("sync: remote already exists: " + r.Name)
	}
	rs.remotes[r.Name] = &r
	return nil
}

// Remove deletes a remote by name. Returns an error if not found.
func (rs *RemoteSet) Remove(name string) error {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if _, exists := rs.remotes[name]; !exists {
		return errors.New("sync: remote not found: " + name)
	}
	delete(rs.remotes, name)
	return nil
}

// Get retrieves a remote by name.
func (rs *RemoteSet) Get(name string) (*Remote, bool) {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	r, ok := rs.remotes[name]
	return r, ok
}

// List returns all remotes in no particular order.
func (rs *RemoteSet) List() []Remote {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	out := make([]Remote, 0, len(rs.remotes))
	for _, r := range rs.remotes {
		out = append(out, *r)
	}
	return out
}

// Len returns the number of registered remotes.
func (rs *RemoteSet) Len() int {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return len(rs.remotes)
}
