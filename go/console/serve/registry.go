package serve

import (
	"fmt"
	"sync"
)

// Registry is the seam kit-owned and adopter-owned services register
// into (contract §"Service registration"). A tool builds one in main
// before the root command executes; the supervisor reads it.
//
// Register panics on a duplicate name. A collision is a wiring bug in
// main, and panicking surfaces it on the first run rather than at the
// first serve; there is no last-writer-wins path. An adopter
// deliberately replacing a kit-shipped service calls [Registry.Override].
//
// This mirrors the panic-on-duplicate contract of
// hop.top/kit/go/console/output.Registry so the two seams behave the
// same way under the same mistake.
type Registry struct {
	mu     sync.RWMutex
	byName map[string]Service
	order  []string
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{byName: make(map[string]Service)}
}

// Register adds svc under svc.Name().
//
// It panics when the name fails [ValidateName] and when a service is
// already registered under it. Both are construction-time wiring
// errors, not runtime conditions.
func (r *Registry) Register(svc Service) {
	if svc == nil {
		panic("serve: Register called with nil Service")
	}
	name := svc.Name()
	if err := ValidateName(name); err != nil {
		panic(err.Error())
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byName[name]; exists {
		panic(fmt.Sprintf("serve: service %q already registered (use Override to replace)", name))
	}
	r.byName[name] = svc
	r.order = append(r.order, name)
}

// Override registers svc, replacing any service already under its
// name and keeping that name's original position in [Registry.List].
// This is the documented way to swap a kit-shipped service for an
// adopter's own, and the only path that accepts a duplicate name.
//
// It still panics on an invalid name: Override lifts the collision
// rule, not the grammar.
func (r *Registry) Override(svc Service) {
	if svc == nil {
		panic("serve: Override called with nil Service")
	}
	name := svc.Name()
	if err := ValidateName(name); err != nil {
		panic(err.Error())
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byName[name]; !exists {
		r.order = append(r.order, name)
	}
	r.byName[name] = svc
}

// Lookup returns the service registered under name, if any.
func (r *Registry) Lookup(name string) (Service, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	svc, ok := r.byName[name]
	return svc, ok
}

// Names returns every registered identifier in registration order, so
// `serve --list` and the startup log mirror the adopter's wiring
// (contract §"Ordering").
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// List returns every registered service in registration order.
func (r *Registry) List() []Service {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Service, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.byName[name])
	}
	return out
}

// Len returns the number of registered services.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byName)
}
