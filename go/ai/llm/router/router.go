// Package router provides a native routing engine for LLM model selection.
//
// A Router scores prompts to decide whether to use a strong or weak model.
// The Controller manages router registration, threshold parsing, middleware
// chains, and delegates completions to the appropriate llm.Provider.
package router

import (
	"context"
	"fmt"
	"sync"
)

// Router scores a prompt and returns a [0,1] win rate for the strong model.
// If the returned score >= threshold, the controller routes to the strong
// model; otherwise, it routes to the weak model.
type Router interface {
	// Score returns the strong-model win rate for the given prompt.
	Score(ctx context.Context, prompt string) (float64, error)
}

// ModelPair holds the strong and weak model identifiers used for routing.
type ModelPair struct {
	Strong string
	Weak   string
}

// Middleware can override the model pair on a per-request basis,
// e.g. based on detected intent.
type Middleware interface {
	GetModelPair(ctx context.Context, prompt string) (*ModelPair, error)
}

// RoutingError indicates a routing-specific failure.
type RoutingError struct {
	Message string
}

func (e *RoutingError) Error() string {
	return fmt.Sprintf("routing: %s", e.Message)
}

// NewRoutingError creates a RoutingError with the given message.
func NewRoutingError(msg string) *RoutingError {
	return &RoutingError{Message: msg}
}

// Registry maps router names to Router implementations.
type Registry struct {
	mu      sync.RWMutex
	routers map[string]Router
}

// NewRegistry creates an empty router registry.
func NewRegistry() *Registry {
	return &Registry{routers: make(map[string]Router)}
}

// Register adds a named router. Returns an error if already registered.
func (r *Registry) Register(name string, router Router) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.routers[name]; ok {
		return fmt.Errorf("router %q already registered", name)
	}
	r.routers[name] = router
	return nil
}

// Get returns the router for the given name, or an error if not found.
func (r *Registry) Get(name string) (Router, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	router, ok := r.routers[name]
	if !ok {
		return nil, NewRoutingError(
			fmt.Sprintf("unknown router %q; available: %v",
				name, r.names()),
		)
	}
	return router, nil
}

// Names returns registered router names.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.names()
}

// names returns registered names (caller must hold lock).
func (r *Registry) names() []string {
	names := make([]string, 0, len(r.routers))
	for k := range r.routers {
		names = append(names, k)
	}
	return names
}
