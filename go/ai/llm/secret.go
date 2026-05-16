package llm

import (
	"context"
	"errors"
	"os"
	"strings"

	"hop.top/kit/go/storage/secret"
)

// envKeyByScheme maps a provider URI scheme to its canonical
// environment variable name. The mapping mirrors what the per-driver
// packages document (anthropic.go, openai.go, ollama.go, …) so a
// single helper can replace the repeated os.Getenv("XXX_API_KEY")
// dance adopters keep reinventing.
//
// New schemes should be added here in lockstep with their driver.
var envKeyByScheme = map[string]string{
	"anthropic": "ANTHROPIC_API_KEY",
	"google":    "GEMINI_API_KEY",
	"gemini":    "GEMINI_API_KEY",
	"openai":    "OPENAI_API_KEY",
	"ollama":    "OLLAMA_API_KEY",
	"routellm":  "ROUTELLM_API_KEY",
	"triton":    "TRITON_API_KEY",
}

// FallbackEnvKey is the universal env var consulted when a
// provider-specific key is empty. It mirrors the LLM_API_KEY
// convention already implemented across the per-driver packages
// (see anthropic.go, google.go, openai.go).
const FallbackEnvKey = "LLM_API_KEY"

// EnvKeyFor returns the canonical env var name for the provider
// identified by providerURI. The URI may be a full URI
// ("openai://gpt-4") or just a scheme ("openai"); only the scheme
// portion drives the lookup.
//
// When the scheme is unknown, EnvKeyFor returns [FallbackEnvKey] so
// callers can still resolve a value from the universal LLM_API_KEY
// variable without having to special-case unknown providers.
func EnvKeyFor(providerURI string) string {
	scheme := schemeOf(providerURI)
	if scheme == "" {
		return FallbackEnvKey
	}
	if name, ok := envKeyByScheme[scheme]; ok {
		return name
	}
	return FallbackEnvKey
}

// SecretFor resolves the API key for providerURI through the
// canonical fallback chain:
//
//  1. store.Get(ctx, EnvKeyFor(uri)) — keyring / vault / etc.
//  2. os.Getenv(EnvKeyFor(uri)) — provider-specific env var
//  3. os.Getenv(FallbackEnvKey) — universal LLM_API_KEY
//
// When all three are empty, SecretFor returns secret.ErrNotFound so
// callers can branch on a single sentinel.
//
// Passing a nil store is allowed; it short-circuits step 1. This
// lets adopters call SecretFor unconditionally even when no secret
// store is configured.
func SecretFor(ctx context.Context, store secret.Store, providerURI string) (string, error) {
	envName := EnvKeyFor(providerURI)

	if store != nil {
		s, err := store.Get(ctx, envName)
		if err == nil && s != nil && len(s.Value) > 0 {
			return string(s.Value), nil
		}
		// secret.ErrNotFound is the expected "no entry"; everything
		// else surfaces unchanged so callers can see backend issues.
		if err != nil && !errors.Is(err, secret.ErrNotFound) {
			return "", err
		}
	}

	if v := os.Getenv(envName); v != "" {
		return v, nil
	}
	if v := os.Getenv(FallbackEnvKey); v != "" {
		return v, nil
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
