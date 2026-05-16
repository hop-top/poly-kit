package breaker

import (
	"sort"
	"sync"
)

// registry is the process-wide breaker store. Keep it private so the
// surface stays narrow (Lookup / List / ResetAll / Snapshot /
// Unregister); we can swap to a versioned snapshot store later
// without breaking callers.
var registry sync.Map // map[string]*breakerImpl

// registerOrPanic inserts b under name. Double-registration panics:
// duplicate names are almost always a bug (two packages claiming the
// same fuse). Tests recover via Unregister + t.Cleanup.
func registerOrPanic(name string, b *breakerImpl) {
	if _, loaded := registry.LoadOrStore(name, b); loaded {
		panic("breaker: duplicate registration for name " + name)
	}
}

// Lookup returns the breaker registered under name. Useful for ops
// commands and tools that don't hold the original handle.
func Lookup(name string) (Breaker, bool) {
	v, ok := registry.Load(name)
	if !ok {
		return nil, false
	}
	return v.(*breakerImpl), true
}

// List returns every registered breaker, sorted by name.
func List() []Breaker {
	var out []Breaker
	registry.Range(func(_, v any) bool {
		out = append(out, v.(*breakerImpl))
		return true
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// ResetAll calls Reset on every registered breaker. Big-hammer ops
// escape hatch.
func ResetAll() {
	registry.Range(func(_, v any) bool {
		v.(*breakerImpl).Reset()
		return true
	})
}

// Snapshot returns a name → Stats map. Best-effort consistency:
// each Stats is atomic per-breaker but the overall map is not a
// snapshot of the whole process at one instant.
func Snapshot() map[string]Stats {
	out := map[string]Stats{}
	registry.Range(func(k, v any) bool {
		out[k.(string)] = v.(*breakerImpl).Stats()
		return true
	})
	return out
}

// Unregister removes name from the registry. For tests + dynamic
// tool teardown.
func Unregister(name string) {
	registry.Delete(name)
}
