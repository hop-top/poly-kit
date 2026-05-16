package output

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Registry holds Formatter implementations keyed by Formatter.Key().
//
// Use Default for the package-level registry shared by all callers, or
// NewRegistry for an isolated set (tests, multi-CLI binaries). Built-in
// formatters (json, yaml, table) register against Default at init time.
//
// Register panics on duplicate keys. Adopters intentionally replacing a
// built-in must call Override.
type Registry struct {
	mu    sync.RWMutex
	byKey map[string]Formatter
}

// NewRegistry returns an empty Registry with no built-in formatters.
func NewRegistry() *Registry {
	return &Registry{byKey: make(map[string]Formatter)}
}

// Default is the package-level Registry. Built-ins register here at init
// time; most adopters use this directly.
var Default = NewRegistry()

// Register adds f to the registry. Panics if a formatter with the same
// Key() is already registered. Use Override to intentionally replace.
func (r *Registry) Register(f Formatter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := f.Key()
	if key == "" {
		panic("output: formatter Key() is empty")
	}
	if _, exists := r.byKey[key]; exists {
		panic(fmt.Sprintf("output: formatter %q already registered (use Override to replace)", key))
	}
	r.byKey[key] = f
}

// Override replaces (or registers) the formatter for f.Key().
// Use this when intentionally swapping a built-in implementation.
func (r *Registry) Override(f Formatter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if f.Key() == "" {
		panic("output: formatter Key() is empty")
	}
	r.byKey[f.Key()] = f
}

// Lookup returns the formatter registered under key, if any.
func (r *Registry) Lookup(key string) (Formatter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.byKey[key]
	return f, ok
}

// Keys returns all registered format keys, sorted for stable output.
func (r *Registry) Keys() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.byKey))
	for k := range r.byKey {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Formatters returns all registered formatters in Key() order.
func (r *Registry) Formatters() []Formatter {
	keys := r.Keys()
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Formatter, len(keys))
	for i, k := range keys {
		out[i] = r.byKey[k]
	}
	return out
}

// ExtensionMap returns extension→key mappings (e.g. ".csv" → "csv") across
// all registered formatters. Later registrations win on collision; callers
// who care should validate ahead of time.
func (r *Registry) ExtensionMap() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]string)
	// stable order: iterate keys sorted so collision resolution is deterministic
	keys := make([]string, 0, len(r.byKey))
	for k := range r.byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, ext := range r.byKey[k].Extensions() {
			out[strings.ToLower(ext)] = k
		}
	}
	return out
}
