// Package llm exposes a pluggable accessor for an [aim.Registry], the single
// source of truth for model metadata (capabilities, modalities, cost, context
// windows). Downstream consumers — most importantly the picker — call
// [Default] to obtain the active registry rather than constructing one
// themselves, so tests and embedders can swap in custom sources via
// [SetDefaultRegistry].
//
// The default lazily constructs a single [aim.NewRegistry] (XDG cache,
// models.dev backing) on first use and reuses the same instance until the
// provider is swapped. Swapping the provider invalidates the cached default,
// so the next [Default] call honors the new provider.
package llm

import (
	"context"
	"sync"

	"hop.top/aim"
)

// RegistryProvider returns the [aim.Registry] the llm package should use.
// Tests and embedders supply a custom provider via [SetDefaultRegistry] to
// inject in-memory sources, mocks, or pre-configured registries.
type RegistryProvider func(ctx context.Context) (*aim.Registry, error)

var (
	registryMu       sync.Mutex
	registryProvider RegistryProvider // nil means "use lazy default"
	registryCache    *aim.Registry    // memoised result of the lazy default
)

// Default returns the active [aim.Registry]. With no override it lazily
// constructs a single [aim.NewRegistry] and reuses it across calls. After
// [SetDefaultRegistry] the supplied provider takes over and its result is
// returned on every call (no caching at this layer — the provider owns
// instance lifetime).
func Default(ctx context.Context) (*aim.Registry, error) {
	registryMu.Lock()
	provider := registryProvider
	registryMu.Unlock()

	if provider != nil {
		return provider(ctx)
	}

	registryMu.Lock()
	defer registryMu.Unlock()
	if registryCache == nil {
		registryCache = aim.NewRegistry()
	}
	return registryCache, nil
}

// SetDefaultRegistry installs fn as the active provider. Passing nil restores
// the lazy default and clears any cached registry so the next [Default] call
// constructs a fresh one.
func SetDefaultRegistry(fn RegistryProvider) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registryProvider = fn
	registryCache = nil
}

// ResetDefaultRegistry is shorthand for SetDefaultRegistry(nil).
func ResetDefaultRegistry() {
	SetDefaultRegistry(nil)
}
