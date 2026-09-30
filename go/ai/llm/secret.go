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
	// triton): a key found under EnvVars is used, a missing one is not
	// an error, and the universal [FallbackEnvKey] is never lent to it.
	Optional bool
}

// providerKeys is kit's single scheme → credential table. Every scheme
// an adapter registers has an entry; a test fails when one is missing.
//
// google and gemini are one adapter under two names. GOOGLE_API_KEY
// outranks GEMINI_API_KEY because Google's genai SDK
// (google.golang.org/genai, getAPIKeyFromEnv) reads them in that order.
//
// The OpenAI-compatible gateways carry their own variables: lending
// them OPENAI_API_KEY would send an OpenAI key to a host that is not
// OpenAI.
var providerKeys = map[string]ProviderKey{
	"anthropic":  {EnvVars: []string{"ANTHROPIC_API_KEY"}},
	"openai":     {EnvVars: []string{"OPENAI_API_KEY"}},
	"google":     {EnvVars: []string{"GOOGLE_API_KEY", "GEMINI_API_KEY"}},
	"gemini":     {EnvVars: []string{"GOOGLE_API_KEY", "GEMINI_API_KEY"}},
	"openrouter": {EnvVars: []string{"OPENROUTER_API_KEY"}},
	"groq":       {EnvVars: []string{"GROQ_API_KEY"}},
	"xai":        {EnvVars: []string{"XAI_API_KEY"}},
	"together":   {EnvVars: []string{"TOGETHER_API_KEY"}},
	"fireworks":  {EnvVars: []string{"FIREWORKS_API_KEY"}},
	"deepseek":   {EnvVars: []string{"DEEPSEEK_API_KEY"}},
	"mistral":    {EnvVars: []string{"MISTRAL_API_KEY"}},
	"ollama":     {EnvVars: []string{"OLLAMA_API_KEY"}, Optional: true},
	"lmstudio":   {Optional: true},
	"routellm":   {EnvVars: []string{"ROUTELLM_API_KEY"}, Optional: true},
	"triton":     {EnvVars: []string{"TRITON_API_KEY"}, Optional: true},
}

// envKeyCompat pins the single name [EnvKeyFor] returned before
// providerKeys listed more than one; GEMINI_API_KEY still resolves.
var envKeyCompat = map[string]string{
	"google": "GEMINI_API_KEY",
	"gemini": "GEMINI_API_KEY",
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
// ok is false for a scheme kit has no entry for. The returned EnvVars
// slice is a copy.
func ProviderKeyFor(providerURI string) (key ProviderKey, ok bool) {
	key, ok = providerKeys[schemeOf(providerURI)]
	key.EnvVars = slices.Clone(key.EnvVars)
	return key, ok
}

// EnvKeyFor returns the canonical env var name for the provider
// identified by providerURI. The URI may be a full URI
// ("openai://gpt-4") or just a scheme ("openai"); only the scheme
// portion drives the lookup.
//
// EnvKeyFor answers with one name. For google and gemini it stays
// GEMINI_API_KEY for compatibility, although GOOGLE_API_KEY outranks
// it; [ProviderKeyFor] lists every name in precedence order.
//
// When the scheme is unknown or takes no key of its own (lmstudio),
// EnvKeyFor returns [FallbackEnvKey] so callers can still resolve a
// value from the universal LLM_API_KEY variable.
func EnvKeyFor(providerURI string) string {
	scheme := schemeOf(providerURI)
	if name, ok := envKeyCompat[scheme]; ok {
		return name
	}
	if key, ok := providerKeys[scheme]; ok && len(key.EnvVars) > 0 {
		return key.EnvVars[0]
	}
	return FallbackEnvKey
}

// SecretFor resolves the API key for providerURI through the
// canonical fallback chain, where names are [ProviderKeyFor]'s EnvVars
// (or [EnvKeyFor] for a scheme without any):
//
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
	names := providerKeys[schemeOf(providerURI)].EnvVars
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
// The key resolves as in [SecretFor]; store may be nil. uri comes back
// unchanged when it already carries api_key (the caller's choice
// outranks the environment), when its scheme takes no key or is one
// kit has no entry for (kit lends no credential to an unknown host),
// and when a local runtime's own variable is unset.
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
	key, known := providerKeys[parsed.Scheme]
	if !known || len(key.EnvVars) == 0 {
		return uri, nil
	}

	value, err := lookupKey(ctx, store, key.EnvVars, !key.Optional)
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
