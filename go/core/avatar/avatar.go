package avatar

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Options carries the inputs to a Provider. All fields are optional except
// Seed; provider-specific defaults apply when fields are zero-valued.
type Options struct {
	// Provider selects which registered provider handles the request.
	// Empty string falls back to the package default (see DefaultProvider).
	Provider string

	// Seed is the identity input. Free-form: providers document what
	// they expect (dicebear: any string; gravatar: email address;
	// boring: any string). Required.
	Seed string

	// Style is provider-specific (e.g. dicebear "shapes" or "bottts").
	// Empty string uses the provider's default style.
	Style string

	// Size in pixels. Zero uses the provider's default. Providers
	// that don't support sizing (vector-only, etc.) ignore this.
	Size int

	// Format is the requested output format ("svg", "png", "webp", ...).
	// Empty uses the provider's default. Providers that only emit one
	// format ignore this.
	Format string

	// Extra carries provider-specific knobs that aren't worth a typed
	// field. Keys and meanings are provider-defined.
	Extra map[string]string
}

// Provider renders an avatar from Options to a URL or data URI.
//
// Generate must be safe for concurrent use; built-in providers are
// pure functions of Options and the ctx is reserved for future async
// providers (e.g. portrait generators that round-trip an API).
type Provider interface {
	Name() string
	Generate(ctx context.Context, opts Options) (string, error)
	// Styles returns the provider's known style identifiers, or
	// nil/empty if the concept doesn't apply.
	Styles() []string
}

var (
	registryMu      sync.RWMutex
	registry        = map[string]Provider{}
	defaultProvider = "dicebear"
)

// RegisterProvider adds p to the registry under p.Name(). If a provider
// with the same name is already registered it is replaced. Built-in
// providers register themselves via init(); callers add their own at
// program start.
func RegisterProvider(p Provider) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[p.Name()] = p
}

// LookupProvider returns the named provider, or false if not registered.
func LookupProvider(name string) (Provider, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	p, ok := registry[name]
	return p, ok
}

// Providers returns the names of all registered providers, sorted.
func Providers() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// DefaultProvider returns the name used when Options.Provider is empty.
func DefaultProvider() string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return defaultProvider
}

// SetDefaultProvider changes the fallback used when Options.Provider is
// empty. Returns an error if the named provider is not registered.
func SetDefaultProvider(name string) error {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, ok := registry[name]; !ok {
		return fmt.Errorf("avatar: provider %q not registered", name)
	}
	defaultProvider = name
	return nil
}

// Generate produces an avatar URL or data URI for opts.
// Provider selection: opts.Provider, else DefaultProvider().
func Generate(ctx context.Context, opts Options) (string, error) {
	if opts.Seed == "" {
		return "", fmt.Errorf("avatar: Seed is required")
	}
	name := opts.Provider
	if name == "" {
		name = DefaultProvider()
	}
	p, ok := LookupProvider(name)
	if !ok {
		return "", fmt.Errorf("avatar: provider %q not registered", name)
	}
	return p.Generate(ctx, opts)
}
