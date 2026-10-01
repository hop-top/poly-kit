package llm

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"sync"

	"hop.top/aim"
	llmerrors "hop.top/kit/go/ai/llm/errors"
)

// ---------------------------------------------------------------------------
// Registry
// ---------------------------------------------------------------------------

// Factory creates a Provider from a resolved configuration.
type Factory func(cfg ResolvedConfig) (Provider, error)

// Declaration is what an adapter states about the provider behind a
// scheme when it registers it. It outranks the aim catalog's facts for
// that scheme; a zero field defers to the catalog and, for the key, to
// the <SCHEME>_API_KEY convention. See [ProviderKeyFor] for the full
// key precedence.
type Declaration struct {
	// Key, when non-nil, is the scheme's credential, used as given:
	// declare it when the provider reads several variables in a set
	// order (google), runs locally (ollama), or takes no key at all
	// (lmstudio: Optional with no EnvVars).
	Key *ProviderKey

	// BaseURL is the scheme's default endpoint. [Resolve] passes it to
	// the factory as ProviderConfig.BaseURL when the URI names neither a
	// host nor a base_url param, including for a scheme reached through
	// an alias ("fireworks-ai" for "fireworks"). Leave it empty when the
	// adapter picks its own default, for example from its own
	// environment variables.
	BaseURL string

	// Protocols lists the wire protocols, as [aim.Provider.Protocol]
	// names them ("openai-compatible", ...), that this scheme's factory
	// speaks. A catalog provider no adapter registers by name or alias
	// resolves through the factory claiming its protocol, with the
	// catalog's base URL. A protocol has at most one claimant.
	Protocols []string
}

// Registry maps URI schemes to adapter factories.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
	decls     map[string]Declaration
	protocols map[string]string // protocol -> scheme whose factory speaks it
}

// DefaultRegistry is the process-wide registry used by adapter init
// functions and the package-level [Register] / [Resolve] helpers.
var DefaultRegistry = NewRegistry()

// Register adds a factory for the given scheme to [DefaultRegistry],
// with at most one [Declaration].
func Register(scheme string, f Factory, decl ...Declaration) {
	DefaultRegistry.Register(scheme, f, decl...)
}

// Resolve looks up and creates a provider via [DefaultRegistry].
func Resolve(uri string) (Provider, error) { return DefaultRegistry.Resolve(uri) }

// Schemes returns the list of registered schemes in [DefaultRegistry].
func Schemes() []string { return DefaultRegistry.Schemes() }

// NewRegistry creates an empty adapter registry.
func NewRegistry() *Registry {
	return &Registry{
		factories: make(map[string]Factory),
		decls:     make(map[string]Declaration),
		protocols: make(map[string]string),
	}
}

// Register adds a factory for the given scheme with at most one
// [Declaration]. Panics on a duplicate scheme, on more than one
// declaration, and on a protocol another scheme already claims.
func (r *Registry) Register(scheme string, f Factory, decl ...Declaration) {
	if len(decl) > 1 {
		panic(fmt.Sprintf("llm: scheme %q registered with %d declarations; want at most one", scheme, len(decl)))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.factories[scheme]; ok {
		panic(fmt.Sprintf(
			"llm: adapter already registered for scheme %q", scheme,
		))
	}
	var d Declaration
	if len(decl) == 1 {
		d = decl[0]
		if d.Key != nil {
			d.Key = &ProviderKey{EnvVars: slices.Clone(d.Key.EnvVars), Optional: d.Key.Optional}
		}
		d.Protocols = slices.Clone(d.Protocols)
	}
	for _, p := range d.Protocols {
		if owner, ok := r.protocols[p]; ok {
			panic(fmt.Sprintf("llm: protocol %q already claimed by scheme %q", p, owner))
		}
	}
	for _, p := range d.Protocols {
		r.protocols[p] = scheme
	}
	r.factories[scheme] = f
	r.decls[scheme] = d
}

// Schemes returns a sorted list of registered URI schemes.
func (r *Registry) Schemes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	schemes := make([]string, 0, len(r.factories))
	for s := range r.factories {
		schemes = append(schemes, s)
	}
	sort.Strings(schemes)
	return schemes
}

// route is how a URI scheme reaches an adapter.
type route struct {
	// scheme is the registered scheme whose factory serves the URI: the
	// URI's own, the one it is an alias of, or the one claiming the
	// catalog provider's protocol.
	scheme  string
	factory Factory
	decl    Declaration // zero for a protocol route: no scheme's defaults apply

	// catalog is set for a protocol route: the provider the URI names.
	catalog *aim.Provider
}

// registered returns the scheme registered under name or, failing that,
// under a curated alias of the same provider ("fireworks-ai" reaches
// "fireworks"). It reads no catalog. Callers hold r.mu.
func (r *Registry) registered(name string) (string, bool) {
	if _, ok := r.factories[name]; ok {
		return name, true
	}
	id := aim.CanonicalProviderID(name)
	var match []string
	for s := range r.factories {
		if aim.CanonicalProviderID(s) == id {
			match = append(match, s)
		}
	}
	if len(match) == 0 {
		return "", false
	}
	// Several names for one provider (google, gemini): prefer the
	// catalog id, else the first by name, so the pick is stable.
	if slices.Contains(match, id) {
		return id, true
	}
	sort.Strings(match)
	return match[0], true
}

// route finds the adapter for scheme: registered by name, then by
// alias, then (from the cached catalog only) by the catalog provider's
// protocol.
func (r *Registry) route(ctx context.Context, scheme string) (route, bool) {
	r.mu.RLock()
	if s, ok := r.registered(scheme); ok {
		rt := route{scheme: s, factory: r.factories[s], decl: r.decls[s]}
		r.mu.RUnlock()
		return rt, true
	}
	r.mu.RUnlock()

	p, ok := catalogProvider(ctx, scheme)
	if !ok {
		return route{}, false
	}
	proto := p.Protocol()
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.protocols[proto]
	if proto == "" || !ok {
		return route{}, false
	}
	return route{scheme: s, factory: r.factories[s], catalog: &p}, true
}

// baseURL is the default base URL a route supplies when the URI names
// none: the declaration's for a registered scheme (empty leaves the
// adapter's own default), the catalog's for a protocol route.
func (rt route) baseURL(uriScheme string) (string, error) {
	if rt.catalog != nil {
		return catalogBaseURL(uriScheme, *rt.catalog)
	}
	return rt.decl.BaseURL, nil
}

// Resolve parses a provider URI, finds its adapter and creates a
// Provider. It uses [ParseURI] to build a minimal [ResolvedConfig]
// directly from the URI components and parameters.
//
// The scheme reaches an adapter registered under it, under a curated
// alias of the same provider (aim's list: fireworks-ai / fireworks,
// togetherai / together, google / gemini), or, for a provider in the
// cached aim catalog that no adapter registers, the adapter claiming
// its protocol ([Declaration.Protocols]). Resolve never fetches the
// catalog.
//
// The key is the URI's api_key param alone. A blank one (empty or only
// whitespace) is no key: it is dropped, and the adapter gets neither a
// key nor the param.
//
// Base URL, highest first: the URI's base_url param or host, the
// declared [Declaration.BaseURL], and for a catalog provider the
// catalog's base URL with its ${VAR} placeholders expanded. A catalog
// provider without a usable base URL is an error, never a fallback to
// another host.
func (r *Registry) Resolve(uri string) (Provider, error) {
	parsed, err := ParseURI(uri)
	if err != nil {
		return nil, fmt.Errorf("llm: invalid URI %q: %w", RedactURI(uri), err)
	}

	rt, ok := r.route(context.Background(), parsed.Scheme)
	if !ok {
		return nil, llmerrors.NewProviderNotFound(parsed.Scheme)
	}
	// A blank api_key is no key: the adapter gets none, and no blank
	// param among its params.
	dropBlankURIKey(&parsed)

	cfg := ResolvedConfig{
		URI: parsed,
		Provider: ProviderConfig{
			Model: parsed.Model,
		},
	}
	if parsed.Host != "" {
		cfg.Provider.BaseURL = "http://" + parsed.Host
	}
	if parsed.Params != nil {
		cfg.Provider.Params = parsed.Params
		if v, ok := parsed.Params["api_key"]; ok {
			cfg.Provider.APIKey = v
		}
		if v, ok := parsed.Params["base_url"]; ok {
			cfg.Provider.BaseURL = v
		}
	}
	if cfg.Provider.BaseURL == "" {
		base, err := rt.baseURL(parsed.Scheme)
		if err != nil {
			return nil, err
		}
		cfg.Provider.BaseURL = base
	}

	return rt.factory(cfg)
}
