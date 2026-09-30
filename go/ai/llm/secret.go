package llm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"hop.top/kit/go/storage/secret"
)

// ProviderKey describes the credential a provider scheme takes.
type ProviderKey struct {
	// EnvVars names the variables holding the key, highest precedence
	// first. Each name is also the key looked up in a [secret.Store].
	// Empty means the scheme takes no key.
	EnvVars []string

	// Optional marks a local runtime (ollama, lmstudio, routellm,
	// triton, or a catalog provider on a loopback host): a key found
	// under EnvVars is used, a missing one is not an error, and the
	// universal [FallbackEnvKey] is never lent to it.
	Optional bool
}

// envKeyCompat pins the single name [EnvKeyFor] returned before the
// google entry listed more than one; GEMINI_API_KEY still resolves.
var envKeyCompat = map[string]string{
	"google": "GEMINI_API_KEY",
	"gemini": "GEMINI_API_KEY",
}

// keyPlan is a scheme's resolved credential source.
type keyPlan struct {
	// known is false for a scheme no adapter serves: kit lends no
	// credential to a host it cannot reach.
	known bool
	// literal is llm.yaml providers.<scheme>.api_key.
	literal string
	// envVar is llm.yaml providers.<scheme>.api_key_env.
	envVar string
	// key lists envVar first, then the layered provider key.
	key ProviderKey
}

// keyPlan resolves scheme's credential, highest precedence first:
//
//  1. llm.yaml providers.<scheme>.api_key, then api_key_env
//  2. the adapter's [Declaration].Key
//  3. aim catalog facts: key vars in catalog order, optional when local
//  4. the <SCHEME>_API_KEY convention
//
// The universal [FallbackEnvKey] (5) is the lookup's, for required keys
// only. Layer 3 reads only the cached catalog; nothing is fetched.
func (r *Registry) keyPlan(ctx context.Context, scheme string) keyPlan {
	rt, ok := r.route(ctx, scheme)
	if !ok {
		return keyPlan{}
	}
	plan := keyPlan{known: true, key: layeredKey(ctx, scheme, rt)}
	plan.literal, plan.envVar = configuredKey(scheme, rt.scheme)
	if plan.envVar != "" {
		vars := []string{plan.envVar}
		for _, v := range plan.key.EnvVars {
			if v != plan.envVar {
				vars = append(vars, v)
			}
		}
		plan.key.EnvVars = vars
	}
	return plan
}

// layeredKey is layers 2-4 of [Registry.keyPlan] for a routed scheme.
func layeredKey(ctx context.Context, scheme string, rt route) ProviderKey {
	if k := rt.decl.Key; k != nil {
		return ProviderKey{EnvVars: slices.Clone(k.EnvVars), Optional: k.Optional}
	}
	facts := rt.catalog
	if facts == nil {
		if p, ok := catalogProvider(ctx, scheme); ok {
			facts = &p
		}
	}
	if facts != nil {
		if k, ok := factsKey(*facts); ok {
			return k
		}
	}
	// A registered scheme (or an alias of one) takes its registered
	// name's convention; a catalog provider takes its own.
	name := rt.scheme
	if rt.catalog != nil {
		name = scheme
	}
	return ProviderKey{EnvVars: []string{conventionKey(name)}}
}

// configuredKey reads llm.yaml's api_key and api_key_env for scheme,
// or for the registered scheme it is an alias of when scheme has no
// block of its own. A block belongs to its scheme: it is never lent to
// another provider.
func configuredKey(scheme, registered string) (literal, envVar string) {
	cf := loadConfigFile()
	fp, ok := cf.Providers[scheme]
	if !ok && registered != scheme {
		fp = cf.Providers[registered]
	}
	return fp.APIKey, fp.APIKeyEnv
}

// FallbackEnvKey is the universal env var consulted when a
// provider-specific key is empty. It mirrors the LLM_API_KEY
// convention already implemented across the per-driver packages
// (see anthropic.go, google.go, openai.go).
const FallbackEnvKey = "LLM_API_KEY"

// ErrMissingKey matches (via errors.Is) every [*MissingKeyError].
var ErrMissingKey = errors.New("llm: missing provider API key")

// MissingKeyError reports that no key was found for a scheme that
// requires one. It carries names only, never a value, so callers can
// word their own message and pick their own exit code.
type MissingKeyError struct {
	Scheme string
	Model  string
	// EnvVars lists every name consulted, highest precedence first,
	// ending with [FallbackEnvKey].
	EnvVars []string
}

func (e *MissingKeyError) Error() string {
	return fmt.Sprintf("llm: no API key for provider %q (model %q): set %s",
		e.Scheme, e.Model, strings.Join(e.EnvVars, " or "))
}

// Is reports whether target is [ErrMissingKey].
func (e *MissingKeyError) Is(target error) bool { return target == ErrMissingKey }

// Unwrap returns [secret.ErrNotFound], the sentinel [SecretFor] uses.
func (e *MissingKeyError) Unwrap() error { return secret.ErrNotFound }

// ProviderKeyFor returns the credential description for the scheme of
// providerURI ("openrouter://vendor/model" or just "openrouter").
//
// EnvVars resolve, highest precedence first: llm.yaml
// providers.<scheme>.api_key_env; the adapter's [Declaration].Key,
// used as given; the cached aim catalog's key vars for the provider
// (optional when its base URL is on loopback); the <SCHEME>_API_KEY
// convention. A scheme reached through an alias ("fireworks-ai") or
// through its catalog protocol resolves like the provider it names.
// The catalog is read from the on-disk cache only, never fetched: with
// no cache, declarations and the convention answer.
//
// ok is false for a scheme no adapter serves. The returned EnvVars
// slice is a copy.
func ProviderKeyFor(providerURI string) (key ProviderKey, ok bool) {
	plan := DefaultRegistry.keyPlan(context.Background(), schemeOf(providerURI))
	return plan.key, plan.known
}

// EnvKeyFor returns the canonical env var name for the provider
// identified by providerURI. The URI may be a full URI
// ("openai://gpt-4") or just a scheme ("openai"); only the scheme
// portion drives the lookup.
//
// EnvKeyFor answers with one name: llm.yaml's api_key_env when set,
// else the first of [ProviderKeyFor]'s EnvVars. For google and gemini
// it stays GEMINI_API_KEY for compatibility, although GOOGLE_API_KEY
// outranks it; [ProviderKeyFor] lists every name in precedence order.
//
// When the scheme is unknown or takes no key of its own (lmstudio),
// EnvKeyFor returns [FallbackEnvKey] so callers can still resolve a
// value from the universal LLM_API_KEY variable.
func EnvKeyFor(providerURI string) string {
	scheme := schemeOf(providerURI)
	plan := DefaultRegistry.keyPlan(context.Background(), scheme)
	if plan.envVar != "" {
		return plan.envVar
	}
	if name, ok := envKeyCompat[scheme]; ok {
		return name
	}
	if plan.known && len(plan.key.EnvVars) > 0 {
		return plan.key.EnvVars[0]
	}
	return FallbackEnvKey
}

// SecretFor resolves the API key for providerURI through the
// canonical fallback chain, where names are [ProviderKeyFor]'s EnvVars
// (or [EnvKeyFor] for a scheme without any):
//
//  0. llm.yaml providers.<scheme>.api_key, when set
//  1. store.Get(ctx, name) for each name — keyring / vault / etc.
//  2. os.Getenv(name) for each name — provider-specific env var
//  3. os.Getenv(FallbackEnvKey) — universal LLM_API_KEY
//
// When all are empty, SecretFor returns secret.ErrNotFound so callers
// can branch on a single sentinel.
//
// Passing a nil store is allowed; it short-circuits step 1. This
// lets adopters call SecretFor unconditionally even when no secret
// store is configured.
func SecretFor(ctx context.Context, store secret.Store, providerURI string) (string, error) {
	plan := DefaultRegistry.keyPlan(ctx, schemeOf(providerURI))
	if plan.literal != "" {
		return plan.literal, nil
	}
	names := plan.key.EnvVars
	if len(names) == 0 {
		names = []string{EnvKeyFor(providerURI)}
	}
	return lookupKey(ctx, store, names, true)
}

// ApplyAPIKey returns uri with the provider's API key set as its
// api_key param, which is where [Resolve] reads it from. A URI-form
// model ("openrouter://openai/gpt-4.1-nano") otherwise reaches its
// provider unauthenticated.
//
// The key resolves as in [SecretFor], names per [ProviderKeyFor];
// store may be nil. uri comes back unchanged when it already carries
// api_key (the caller's choice outranks everything else), when its
// scheme takes no key or is one no adapter serves (kit lends no
// credential to an unknown host), and when a local runtime's own
// variable is unset.
//
// A required key that resolves nowhere yields a [*MissingKeyError]
// (errors.Is ErrMissingKey); store backend failures come back wrapped
// and are not ErrMissingKey. uri must name its scheme: deriving one
// from a bare model id is the caller's policy.
//
// The returned URI holds the key: never log or print it. Errors never
// carry key values.
func ApplyAPIKey(ctx context.Context, store secret.Store, uri string) (string, error) {
	parsed, err := ParseURI(uri)
	if err != nil {
		// ParseURI quotes its input, which may carry a key.
		return "", errors.New("llm: apply API key: provider URI must be scheme://model")
	}
	if _, explicit := parsed.Params["api_key"]; explicit {
		return uri, nil
	}
	// A scheme no adapter serves has a zero plan: no literal, no names.
	plan := DefaultRegistry.keyPlan(ctx, parsed.Scheme)
	value := plan.literal
	if value == "" {
		key := plan.key
		if len(key.EnvVars) == 0 {
			return uri, nil
		}
		value, err = lookupKey(ctx, store, key.EnvVars, !key.Optional)
		switch {
		case errors.Is(err, secret.ErrNotFound):
			if key.Optional {
				return uri, nil
			}
			return "", &MissingKeyError{
				Scheme:  parsed.Scheme,
				Model:   parsed.Model,
				EnvVars: append(slices.Clone(key.EnvVars), FallbackEnvKey),
			}
		case err != nil:
			return "", fmt.Errorf("llm: resolve %s API key: %w", parsed.Scheme, err)
		}
	}

	// ParseURI splits the query on & and does not unescape, so a key
	// holding either separator cannot travel as a param intact.
	if strings.ContainsAny(value, "&?#") {
		return "", fmt.Errorf("llm: %s API key contains a character a provider URI cannot carry (& ? #)", parsed.Scheme)
	}
	sep := "?"
	if strings.Contains(uri, "?") {
		sep = "&"
	}
	return uri + sep + "api_key=" + value, nil
}

// lookupKey consults store then environment for each name, then the
// universal variable when universal is set. secret.ErrNotFound means
// nothing was found; any other store error surfaces unchanged.
func lookupKey(ctx context.Context, store secret.Store, names []string, universal bool) (string, error) {
	if store != nil {
		for _, name := range names {
			s, err := store.Get(ctx, name)
			if err == nil && s != nil && len(s.Value) > 0 {
				return string(s.Value), nil
			}
			// secret.ErrNotFound is the expected "no entry"; everything
			// else surfaces unchanged so callers can see backend issues.
			if err != nil && !errors.Is(err, secret.ErrNotFound) {
				return "", err
			}
		}
	}
	for _, name := range names {
		if v := os.Getenv(name); v != "" {
			return v, nil
		}
	}
	if universal {
		if v := os.Getenv(FallbackEnvKey); v != "" {
			return v, nil
		}
	}
	return "", secret.ErrNotFound
}

// schemeOf extracts the scheme from a provider URI. When the input
// has no "://" separator the entire string is treated as the
// scheme — adopters sometimes pass "openai" alone when they only
// need the env-var mapping.
func schemeOf(providerURI string) string {
	if providerURI == "" {
		return ""
	}
	if idx := strings.Index(providerURI, "://"); idx > 0 {
		return providerURI[:idx]
	}
	return providerURI
}
