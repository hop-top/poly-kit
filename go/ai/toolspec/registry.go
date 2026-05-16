package toolspec

import (
	"context"
)

// Cache is an optional key-value cache for resolved ToolSpecs.
// Implementations control TTL and eviction independently.
type Cache interface {
	Get(ctx context.Context, key string, dst any) (bool, error)
	Put(ctx context.Context, key string, v any) error
}

// Registry resolves tool specs from ordered sources with optional caching.
type Registry struct {
	sources []Source
	cache   Cache // nil when caching is disabled
}

// RegistryOption configures a Registry.
type RegistryOption func(*Registry)

// WithSource appends a source to the resolution chain.
// Sources are queried in the order they are added; earlier sources
// take precedence when merging.
func WithSource(s Source) RegistryOption {
	return func(r *Registry) { r.sources = append(r.sources, s) }
}

// WithCache enables caching via the provided Cache implementation.
// Use sqlstore.Store (which satisfies Cache) for SQLite-backed caching.
func WithCache(c Cache) RegistryOption {
	return func(r *Registry) { r.cache = c }
}

// NewRegistry creates a Registry with the supplied options.
func NewRegistry(opts ...RegistryOption) *Registry {
	r := &Registry{}
	for _, o := range opts {
		o(r)
	}
	return r
}

// cacheKey returns the SQLite key for a tool name.
func cacheKey(tool string) string { return "toolspec:" + tool }

// Resolve returns the merged ToolSpec for tool by checking the cache
// first (if configured) and then querying each source in order.
// The merged result is stored in the cache before returning.
func (r *Registry) Resolve(tool string) (*ToolSpec, error) {
	ctx := context.Background()

	// 1. Check cache.
	if r.cache != nil {
		var cached ToolSpec
		ok, err := r.cache.Get(ctx, cacheKey(tool), &cached)
		if err != nil {
			return nil, err
		}
		if ok {
			return &cached, nil
		}
	}

	// 2. Query sources and merge.
	var acc *ToolSpec
	for _, src := range r.sources {
		spec, err := src.Resolve(tool)
		if err != nil {
			return nil, err
		}
		if spec == nil {
			continue
		}
		if acc == nil {
			acc = spec
			continue
		}
		acc = Merge(acc, spec)
	}
	if acc == nil {
		acc = &ToolSpec{Name: tool}
	}

	// 3. Store in cache.
	if r.cache != nil {
		if err := r.cache.Put(ctx, cacheKey(tool), acc); err != nil {
			return nil, err
		}
	}

	return acc, nil
}
