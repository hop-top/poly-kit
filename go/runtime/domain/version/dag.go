package version

import (
	"fmt"
	"sync"
)

// Version represents a single point in the version DAG.
type Version struct {
	ID        string   `json:"id"`
	ParentIDs []string `json:"parent_ids"`
	Timestamp int64    `json:"timestamp"`
	Hash      string   `json:"hash"` // SHA-256 of payload
}

// DAG is a directed acyclic graph of versions, providing ancestry
// queries and branch detection.
type DAG struct {
	mu       sync.RWMutex
	versions map[string]*Version
	children map[string][]string
}

// NewDAG returns an empty version DAG.
func NewDAG() *DAG {
	return &DAG{
		versions: make(map[string]*Version),
		children: make(map[string][]string),
	}
}

// Append adds a version to the DAG. Returns an error if the version
// ID already exists or if any parent ID is unknown.
func (d *DAG) Append(v Version) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, exists := d.versions[v.ID]; exists {
		return fmt.Errorf("version %s already exists", v.ID)
	}
	for _, pid := range v.ParentIDs {
		if _, ok := d.versions[pid]; !ok {
			return fmt.Errorf("unknown parent %s", pid)
		}
	}

	d.versions[v.ID] = &v
	for _, pid := range v.ParentIDs {
		d.children[pid] = append(d.children[pid], v.ID)
	}
	return nil
}

// Get retrieves a version by ID.
func (d *DAG) Get(id string) (*Version, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	v, ok := d.versions[id]
	return v, ok
}

// Ancestors returns all ancestor IDs of the given version (exclusive),
// in no guaranteed order. Uses iterative BFS to avoid stack overflow.
func (d *DAG) Ancestors(id string) []string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	visited := make(map[string]bool)
	queue := []string{id}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if visited[cur] {
			continue
		}
		visited[cur] = true
		if v, ok := d.versions[cur]; ok {
			queue = append(queue, v.ParentIDs...)
		}
	}

	delete(visited, id)
	result := make([]string, 0, len(visited))
	for k := range visited {
		result = append(result, k)
	}
	return result
}

// CommonAncestor finds the most recent common ancestor of a and b.
// Returns ("", false) if no common ancestor exists.
func (d *DAG) CommonAncestor(a, b string) (string, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	// BFS to collect all ancestors of a (inclusive)
	ancestorsA := make(map[string]bool)
	q := []string{a}
	for len(q) > 0 {
		cur := q[0]
		q = q[1:]
		if ancestorsA[cur] {
			continue
		}
		ancestorsA[cur] = true
		if v, ok := d.versions[cur]; ok {
			q = append(q, v.ParentIDs...)
		}
	}

	// BFS from b looking for first hit in ancestorsA
	queue := []string{b}
	visited := make(map[string]bool)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if visited[cur] {
			continue
		}
		visited[cur] = true
		if ancestorsA[cur] {
			return cur, true
		}
		if v, ok := d.versions[cur]; ok {
			queue = append(queue, v.ParentIDs...)
		}
	}
	return "", false
}

// Children returns the IDs of versions that list id in their
// ParentIDs. Order matches insertion order (the DAG records each
// child as it is appended). Returns an empty slice when id has no
// children or is unknown to the DAG.
//
// Useful for bottom-up DAG walks (e.g. version pruning, where a
// candidate is prunable iff all of its descendants are also
// candidates — see engine-version-pruning spec §3 #3).
func (d *DAG) Children(id string) []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	src := d.children[id]
	if len(src) == 0 {
		return nil
	}
	out := make([]string, len(src))
	copy(out, src)
	return out
}

// Heads returns all version IDs that have no children (tips of branches).
func (d *DAG) Heads() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	heads := make([]string, 0)
	for id := range d.versions {
		if len(d.children[id]) == 0 {
			heads = append(heads, id)
		}
	}
	return heads
}

// IsBranched reports whether the DAG has more than one head.
func (d *DAG) IsBranched() bool {
	return len(d.Heads()) > 1
}
