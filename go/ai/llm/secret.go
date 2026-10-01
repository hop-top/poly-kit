package llm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	// literal is api_key of the llm.yaml block configuring the scheme
	// ([ProviderSettingsFor] picks it).
	literal string
	// envVar is that block's api_key_env.
	envVar string
	// block is the providers key the block sits under.
	block string
	// key lists envVar first, then the layered provider key.
	key ProviderKey
}

// keyPlan resolves scheme's credential, highest precedence first:
//
//  1. llm.yaml api_key, then api_key_env, from the block
//     [ProviderSettingsFor] picks (the scheme's own, else an alias's)
//  2. the adapter's [Declaration].Key
//  3. aim catalog facts: key vars in catalog order, optional when local
//  4. the <SCHEME>_API_KEY convention
//
// The universal [FallbackEnvKey] (5) is the lookup's, for required keys
// only. Layer 3 reads only the cached catalog; nothing is fetched. A
// scheme no adapter serves has layer 1 alone.
func (r *Registry) keyPlan(ctx context.Context, scheme string) keyPlan {
	rt, ok := r.route(ctx, scheme)
	var plan keyPlan
	if ok {
		plan = keyPlan{known: true, key: layeredKey(ctx, scheme, rt)}
	}
	// A scheme no adapter serves still takes what its own block names:
	// that key is its own, not lent.
	fp, block, _ := findBlock(loadConfigFile(), blockNames(scheme, rt, ok))
	plan.literal, plan.block = fp.APIKey, block
	if !blankKey(fp.APIKeyEnv) {
		plan.envVar = fp.APIKeyEnv
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
	// StoreErr, when non-nil, reports the secret-store backend failures
	// met on the way: the key may sit in a store that could not be read.
	// It names keys, never values.
	StoreErr error
}

func (e *MissingKeyError) Error() string {
	model := ""
	if e.Model != "" {
		model = fmt.Sprintf(" (model %q)", e.Model)
	}
	msg := fmt.Sprintf("llm: no API key for provider %q%s: set %s",
		e.Scheme, model, strings.Join(e.EnvVars, " or "))
	if e.StoreErr != nil {
		msg += " (" + e.StoreErr.Error() + ")"
	}
	return msg
}

// Is reports whether target is [ErrMissingKey] or, through StoreErr,
// one of the store's errors.
func (e *MissingKeyError) Is(target error) bool {
	return target == ErrMissingKey || (e.StoreErr != nil && errors.Is(e.StoreErr, target))
}

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
// ok is false for a scheme no adapter serves; EnvVars then holds only
// its own llm.yaml api_key_env, if set. The returned EnvVars slice is a
// copy.
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

// SecretFor returns the API key for the scheme of providerURI
// ("openai://gpt-4" or just "openai"), resolved as [ResolveAPIKey]
// resolves it apart from the URI's own api_key param, which SecretFor
// ignores:
//
//  0. llm.yaml api_key of the block [ProviderSettingsFor] picks
//  1. store.Get(ctx, name) for each [ProviderKeyFor] name — keyring /
//     vault / etc.; the block's api_key_env comes first
//  2. os.Getenv(name) for each name — provider-specific env var
//  3. os.Getenv(FallbackEnvKey) — universal LLM_API_KEY, for a required
//     key only: never for a local runtime, a scheme taking no key, or
//     one no adapter serves
//
// A blank value (empty or only whitespace) counts as unset at every
// step. When all are blank, SecretFor returns an error matching
// secret.ErrNotFound (a [*MissingKeyError] for a required key) so
// callers can branch on a single sentinel.
//
// A store backend failure for a name counts as that name being absent
// from the store: the search goes on. When a later source supplies the
// key the failure is logged as a warning on [slog.Default]; when none
// does, the returned error carries it.
//
// Passing a nil store is allowed; it short-circuits step 1. This
// lets adopters call SecretFor unconditionally even when no secret
// store is configured.
func SecretFor(ctx context.Context, store secret.Store, providerURI string) (string, error) {
	scheme := schemeOf(providerURI)
	res, err := DefaultRegistry.resolveKey(ctx, store, URI{Scheme: scheme})
	if err != nil {
		return "", err // a *MissingKeyError: errors.Is secret.ErrNotFound
	}
	if res.Value == "" {
		if res.StoreErr != nil {
			return "", fmt.Errorf("%w (%w)", secret.ErrNotFound, res.StoreErr)
		}
		return "", secret.ErrNotFound
	}
	warnStoreError(ctx, scheme, res.StoreErr)
	return res.Value, nil
}

// ApplyAPIKey returns uri with the provider's API key set as its
// api_key param, which is where [Resolve] reads it from. A URI-form
// model ("openrouter://openai/gpt-4.1-nano") otherwise reaches its
// provider unauthenticated.
//
// The key resolves as in [ResolveAPIKey], which also reports where it
// came from; [LoadConfig] resolves its APIKey the same way. store may
// be nil. uri comes back unchanged when it already carries a non-blank
// api_key (the caller's choice outranks everything else). A blank one
// (empty or only whitespace) is no key: it is removed and the key
// resolves as if uri named none. uri also comes back unchanged, a blank
// api_key aside, when its scheme takes no key, when a local runtime's
// own variable is unset, and when no adapter serves the scheme and its
// own llm.yaml block names no key (kit lends no other credential to an
// unknown host).
//
// A required key that resolves nowhere yields a [*MissingKeyError]
// (errors.Is ErrMissingKey). A store backend failure for a name counts
// as that name being absent from the store and the search goes on; it
// is surfaced, never dropped: in the MissingKeyError's StoreErr when no
// source has the key, else as a warning on [slog.Default]. uri must
// name its scheme: deriving one from a bare model id is the caller's
// policy.
//
// The returned URI holds the key: never log or print it. Errors never
// carry key values.
func ApplyAPIKey(ctx context.Context, store secret.Store, uri string) (string, error) {
	parsed, err := ParseURI(uri)
	if err != nil {
		// ParseURI quotes its input, which may carry a key.
		return "", errors.New("llm: apply API key: provider URI must be scheme://model")
	}
	// A blank api_key is no key: it leaves the URI, and the key resolves
	// as if the URI never named one.
	if v, named := parsed.Params["api_key"]; named && blankKey(v) {
		uri = stripURIParam(uri, "api_key")
	}
	res, err := DefaultRegistry.resolveKey(ctx, store, parsed)
	if err != nil {
		return "", err // a *MissingKeyError, carrying any store failure
	}
	warnStoreError(ctx, parsed.Scheme, res.StoreErr)
	if res.Source.Kind == KeySourceURI || res.Value == "" {
		return uri, nil
	}
	value := res.Value

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
// universal variable when universal is set, and says which answered. A
// blank value counts as unset. It returns "" and a zero source when
// nothing is found.
//
// A store backend failure (any error but secret.ErrNotFound) counts as
// the name being absent from the store, so an unreadable keyring never
// hides a key set in the environment. The failures come back in
// storeErr, found or not, for the caller to surface.
func lookupKey(ctx context.Context, store secret.Store, names []string, universal bool) (value string, src KeySource, storeErr error) {
	var failed storeLookupError
	defer func() {
		if len(failed) > 0 {
			storeErr = failed
		}
	}()
	if store != nil {
		for _, name := range names {
			s, err := store.Get(ctx, name)
			if err == nil && s != nil && !blankKey(string(s.Value)) {
				return string(s.Value), KeySource{Kind: KeySourceStore, Name: name}, nil
			}
			if err != nil && !errors.Is(err, secret.ErrNotFound) {
				failed = append(failed, fmt.Errorf("%s: %w", name, err))
			}
		}
	}
	for _, name := range names {
		if v := os.Getenv(name); !blankKey(v) {
			return v, KeySource{Kind: KeySourceEnv, Name: name}, nil
		}
	}
	if universal {
		if v := os.Getenv(FallbackEnvKey); !blankKey(v) {
			return v, KeySource{Kind: KeySourceFallback, Name: FallbackEnvKey}, nil
		}
	}
	return "", KeySource{}, nil
}

// storeLookupError lists the secret-store backend failures one key
// lookup met, each naming the key asked for, never a value.
type storeLookupError []error

func (e storeLookupError) Error() string {
	parts := make([]string, len(e))
	for i, err := range e {
		parts[i] = err.Error()
	}
	return "secret store: " + strings.Join(parts, "; ")
}

func (e storeLookupError) Unwrap() []error { return e }

// warnStoreError logs err, a lookup's store failures, when a key was
// resolved (or found optional) despite them: the call's result cannot
// carry it, and an unreadable store should not pass unnoticed.
func warnStoreError(ctx context.Context, scheme string, err error) {
	if err == nil {
		return
	}
	slog.Default().WarnContext(ctx, "llm: secret store lookup failed; key resolution went on without it",
		"scheme", scheme, "error", err)
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
