package llm

import (
	"context"
	"slices"
	"strings"
)

// localPriceSchemes run on the caller's hardware: no per-token charge.
var localPriceSchemes = []string{"ollama", "lmstudio", "routellm", "triton"}

// PriceOf is model's rate per million tokens under scheme, for
// [Usage.Cost]. It reads only the aim catalog already cached on disk
// (the registry [Default] returns, so [SetDefaultRegistry] redirects
// it); it never fetches, and a missing cache answers (Price{}, false).
//
// A local scheme (ollama, lmstudio, routellm, triton, or a catalog
// provider on a loopback host) is free: Price{}, true, whatever the
// model. Otherwise ok is false when the cache, the provider, the model
// or the model's cost is absent. scheme may be a curated alias of a
// catalog provider id ("fireworks-ai" for "fireworks").
func PriceOf(ctx context.Context, scheme, model string) (Price, bool) {
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	if slices.Contains(localPriceSchemes, scheme) {
		return Price{}, true
	}
	p, ok := catalogProvider(ctx, scheme)
	if !ok {
		return Price{}, false
	}
	if p.IsLocal() {
		return Price{}, true
	}
	m := p.Models[model]
	if m == nil || m.Cost == nil {
		return Price{}, false
	}
	return Price{
		Input:      m.Cost.Input,
		Output:     m.Cost.Output,
		CacheRead:  m.Cost.CacheRead,
		CacheWrite: m.Cost.CacheWrite,
	}, true
}
