package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"hop.top/kit/go/console/cli/svcconfig"
	"hop.top/kit/go/storage/kv"
	"hop.top/kit/go/storage/kv/memory"
	"hop.top/kit/go/transport/cmdsurface"
)

// The cache block is the api service's read-tier result cache:
// services.api.cache, with shared defaults under services.all.cache.
// It is on by default and inert until a read command declares
// kit/cache-ttl.
//
//	services:
//	  api:
//	    cache:
//	      enabled: true      # default
//	      backend: memory    # default; or a registered kv backend with TTL support
//	      max_bytes: 67108864 # memory only; default 64 MiB
//	      path: cache.db     # file or directory, for sqlite or badger
//
// A backend other than memory must be registered by importing its
// kv driver (hop.top/kit/go/storage/kv/sqlite, .../badger) and must
// store with a TTL. Its keys are registered in svcconfig.
const (
	cacheBlock         = "cache"
	cacheKeyEnabled    = "enabled"
	cacheKeyBackend    = "backend"
	cacheKeyPath       = "path"
	cacheKeyMaxBytes   = "max_bytes"
	cacheBackendMemory = "memory"
)

// cacheNoTTLBackends are kit's kv backends that store without a TTL,
// and so cannot hold a result cache.
var cacheNoTTLBackends = []string{"etcd", "tidb"}

// resultCacheConfig is the resolved cache block.
type resultCacheConfig struct {
	enabled  bool
	backend  string
	path     string
	maxBytes int64
}

// resultCacheSettings resolves and checks the api service's cache
// block. An unknown key, a value of the wrong type, a backend that is
// not registered or has no TTL, a file backend without a path, or
// max_bytes on a backend it does not apply to is a configuration
// error, reported at validation.
func (a *apiService) resultCacheSettings() (resultCacheConfig, error) {
	out := resultCacheConfig{enabled: true, backend: cacheBackendMemory}
	if a.root == nil || a.root.Viper == nil {
		return out, nil
	}
	v := a.root.Viper
	cfg := svcconfig.New(v)
	if err := cfg.ValidateBlock(cacheBlock, APIServiceName, svcconfig.Shared); err != nil {
		return out, err
	}
	if raw, k, ok := cfg.Lookup(APIServiceName, cacheBlock, cacheKeyEnabled); ok {
		on, err := boolValue(raw)
		if err != nil {
			return out, fmt.Errorf("%s: %w", k, err)
		}
		out.enabled = on
	}
	backendKey := ""
	if _, k, ok := cfg.Lookup(APIServiceName, cacheBlock, cacheKeyBackend); ok {
		out.backend = strings.TrimSpace(v.GetString(k))
		backendKey = k
	}
	if _, k, ok := cfg.Lookup(APIServiceName, cacheBlock, cacheKeyPath); ok {
		out.path = strings.TrimSpace(v.GetString(k))
	}
	if raw, k, ok := cfg.Lookup(APIServiceName, cacheBlock, cacheKeyMaxBytes); ok {
		n, err := wholeBytes(raw)
		if err == nil && n <= 0 {
			err = fmt.Errorf("%d is not positive; want a byte count (default %d)", n, memory.DefaultMaxBytes)
		}
		if err == nil && out.backend != cacheBackendMemory {
			err = fmt.Errorf("applies to the memory backend only, not %q", out.backend)
		}
		if err != nil {
			return out, fmt.Errorf("%s: %w", k, err)
		}
		out.maxBytes = n
	}
	if !out.enabled || out.backend == cacheBackendMemory {
		return out, nil
	}
	switch {
	case out.backend == "":
		return out, fmt.Errorf("%s: empty; use %q or a registered kv backend with TTL support", backendKey, cacheBackendMemory)
	case slices.Contains(cacheNoTTLBackends, out.backend):
		return out, fmt.Errorf("%s: %q stores without a TTL; use %q, \"sqlite\" or \"badger\"",
			backendKey, out.backend, cacheBackendMemory)
	case !slices.Contains(kv.Backends(), out.backend):
		return out, fmt.Errorf("%s: kv backend %q is not registered; import its driver (registered: %s)",
			backendKey, out.backend, strings.Join(append([]string{cacheBackendMemory}, kv.Backends()...), ", "))
	case out.path == "":
		return out, fmt.Errorf("%s: backend %q needs %s",
			svcconfig.Key(APIServiceName, cacheBlock, cacheKeyPath), out.backend, cacheKeyPath)
	}
	return out, nil
}

// validateResultCache refuses a cache block resultCacheSettings would.
func (a *apiService) validateResultCache() error {
	_, err := a.resultCacheSettings()
	return err
}

// openResultCache opens the store the cache block names and returns
// the bridge option that turns the cache on, with the store to close
// when the service stops. A disabled cache returns neither.
func (a *apiService) openResultCache(ctx context.Context) (cmdsurface.Option, kv.Store, error) {
	c, err := a.resultCacheSettings()
	if err != nil || !c.enabled {
		return nil, nil, err
	}
	if c.backend == cacheBackendMemory {
		store := memory.New(memory.WithMaxBytes(c.maxBytes))
		return cmdsurface.WithResultCache(store), store, nil
	}
	store, err := kv.OpenContext(ctx, kv.Config{Backend: c.backend, Path: c.path})
	if err != nil {
		return nil, nil, fmt.Errorf("services.%s.%s: %w", APIServiceName, cacheBlock, err)
	}
	ttl, ok := store.(kv.TTLStore)
	if !ok {
		_ = store.Close()
		return nil, nil, fmt.Errorf("services.%s.%s.%s: kv backend %q stores without a TTL",
			APIServiceName, cacheBlock, cacheKeyBackend, c.backend)
	}
	return cmdsurface.WithResultCache(ttl), store, nil
}
