// Package registry provides an init()-based plugin registry for extensions
// that declare [ext.CapRegistry].
//
// A package-level default registry is provided for convenience. Extensions
// register themselves during init() and consumers look them up at runtime.
package registry

import (
	"fmt"
	"sync"

	"hop.top/kit/go/ai/ext"
)

// Registry holds registered extensions keyed by name.
type Registry struct {
	mu    sync.RWMutex
	exts  map[string]ext.Extension
	order []string
}

// New returns an empty Registry.
func New() *Registry {
	return &Registry{exts: make(map[string]ext.Extension)}
}

// Register adds an extension to the registry.
// It panics if the extension does not declare CapRegistry or if an extension
// with the same name is already registered.
func (r *Registry) Register(e ext.Extension) {
	if !e.Capabilities().Has(ext.CapRegistry) {
		panic(fmt.Sprintf("registry: extension %q does not declare CapRegistry", e.Meta().Name))
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	name := e.Meta().Name
	if _, exists := r.exts[name]; exists {
		panic(fmt.Sprintf("registry: duplicate extension %q", name))
	}
	r.exts[name] = e
	r.order = append(r.order, name)
}

// Get returns the extension with the given name and true, or zero value and
// false if not found.
func (r *Registry) Get(name string) (ext.Extension, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	e, ok := r.exts[name]
	return e, ok
}

// MustGet returns the extension with the given name or panics.
func (r *Registry) MustGet(name string) ext.Extension {
	e, ok := r.Get(name)
	if !ok {
		panic(fmt.Sprintf("registry: extension %q not found", name))
	}
	return e
}

// List returns all registered extensions in registration order.
func (r *Registry) List() []ext.Extension {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]ext.Extension, len(r.order))
	for i, name := range r.order {
		out[i] = r.exts[name]
	}
	return out
}

// defaultRegistry is the package-level registry used by the top-level
// functions.
var defaultRegistry = New()

// Register adds an extension to the default registry.
// See [Registry.Register] for details.
func Register(e ext.Extension) { defaultRegistry.Register(e) }

// Get looks up an extension by name in the default registry.
func Get(name string) (ext.Extension, bool) { return defaultRegistry.Get(name) }

// MustGet looks up an extension by name in the default registry or panics.
func MustGet(name string) ext.Extension { return defaultRegistry.MustGet(name) }

// List returns all extensions from the default registry.
func List() []ext.Extension { return defaultRegistry.List() }
