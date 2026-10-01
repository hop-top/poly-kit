package llm

import (
	"context"
	"slices"

	"hop.top/aim"
)

// ProviderSettings is a provider block of llm.yaml
// ({xdg.ConfigDir("hop")}/llm.yaml, providers.<name>) as
// [ProviderSettingsFor] finds it. It never carries the api_key value:
// [ResolveAPIKey] resolves the key and says where it came from.
type ProviderSettings struct {
	// Block is the providers key the settings were read from: the
	// scheme itself or another name for the same provider
	// ("fireworks" for "fireworks-ai").
	Block string
	// BaseURL is base_url. [LoadConfig] layers LLM_BASE_URL and the
	// URI over it; this is the file's value alone.
	BaseURL string
	// Model is model.
	Model string
	// APIKeyEnv is api_key_env: the variable holding the key.
	APIKeyEnv string
	// Extras holds the block's other keys.
	Extras map[string]any
}

// ProviderSettingsFor returns the llm.yaml provider block that
// configures the scheme of providerURI ("fireworks-ai://model" or just
// "fireworks-ai"). ok is false when no block does.
//
// Blocks are tried in order, and the first found is used whole (blocks
// are never merged):
//
//  1. providers.<scheme>
//  2. for a scheme reaching an adapter by name or alias, the registered
//     scheme it reaches ("fireworks-ai" reads providers.fireworks)
//  3. the provider's other curated names: aim's catalog id and its
//     aliases (providers.fireworks-ai for "fireworks", providers.gemini
//     for "google")
//
// A catalog provider routed by protocol takes 1 and 3 only: the block of
// the adapter claiming its protocol (providers.openai for
// "digitalocean") configures another host and is never lent to it. A
// scheme no adapter serves takes its own block only.
//
// [LoadConfig] and the key resolution ([ResolveAPIKey], [ApplyAPIKey],
// [ProviderKeyFor]) read the same block. The catalog is read from the
// on-disk cache only, never fetched.
func ProviderSettingsFor(providerURI string) (ProviderSettings, bool) {
	fp, block, ok := DefaultRegistry.providerBlock(context.Background(), schemeOf(providerURI))
	if !ok {
		return ProviderSettings{}, false
	}
	s := ProviderSettings{
		Block:     block,
		BaseURL:   fp.BaseURL,
		Model:     fp.Model,
		APIKeyEnv: fp.APIKeyEnv,
	}
	if len(fp.Extra) > 0 {
		s.Extras = fp.Extra
	}
	return s, true
}

// providerBlock returns the llm.yaml block configuring scheme and the
// providers key it sits under; see [ProviderSettingsFor].
func (r *Registry) providerBlock(ctx context.Context, scheme string) (configFileProvider, string, bool) {
	return findBlock(loadConfigFile(), r.blockNamesFor(ctx, scheme))
}

// blockNamesFor is [blockNames] for scheme as r routes it.
func (r *Registry) blockNamesFor(ctx context.Context, scheme string) []string {
	rt, routed := r.route(ctx, scheme)
	return blockNames(scheme, rt, routed)
}

// blockNames lists, highest precedence first, the providers keys that
// may configure scheme reached through rt; routed is false when no
// adapter serves it.
func blockNames(scheme string, rt route, routed bool) []string {
	names := []string{scheme}
	if !routed {
		return names
	}
	if rt.catalog == nil {
		names = append(names, rt.scheme)
	}
	names = append(names, aim.CanonicalProviderID(scheme))
	names = append(names, aim.ProviderAliases(scheme)...)
	out := names[:0:0]
	for _, n := range names {
		if n != "" && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// findBlock returns the first of names with a block in cf.
func findBlock(cf configFile, names []string) (configFileProvider, string, bool) {
	for _, n := range names {
		if fp, ok := cf.Providers[n]; ok {
			return fp, n, true
		}
	}
	return configFileProvider{}, "", false
}
