package llm_test

// Gap tests for `hop.top/kit/go/ai/llm`. Surfaced by every adopter
// reimplementing the secret.Open(...).Get(ctx, "<PROVIDER>_API_KEY")
// dance keyed off URI scheme.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/storage/secret/memory"
)

// Gap: no LLM-provider → keyring helper.
//
// Every provider driver (anthropic, google, openai, ollama, triton,
// routellm) currently calls os.Getenv("XXX_API_KEY") with a fallback
// to LLM_API_KEY. Adopters that store keys in a keyring (kit ships
// go/storage/secret/keyring) must reimplement:
//
//   - parse provider URI (e.g. "anthropic://...", "openai://...")
//   - map scheme → env var name (anthropic→ANTHROPIC_API_KEY, etc.)
//   - secret.Open(...).Get(ctx, envName)
//   - fallback chain: keyring → env → LLM_API_KEY
//
// Desired API:
//
//	key, err := llm.SecretFor(ctx, store, providerURI)
//	// store is a secret.Store; providerURI is "openai://...";
//	// returns the secret resolved through keyring then env.
//
// Or a sibling that reads the scheme→env map kit already encodes
// across the per-provider drivers:
//
//	envName := llm.EnvKeyFor(providerURI)  // "OPENAI_API_KEY"
//
// Either form would let adopters drop the per-tool reimplementation.
func TestGap_LLMSecretFor_Missing(t *testing.T) {
	// Make sure no leftover keys from CI poison the result.
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("LLM_API_KEY", "")

	require.Equal(t, "OPENAI_API_KEY", llm.EnvKeyFor("openai://gpt-4"))
	require.Equal(t, "ANTHROPIC_API_KEY", llm.EnvKeyFor("anthropic://claude-3"))
	require.Equal(t, "GEMINI_API_KEY", llm.EnvKeyFor("google://gemini-pro"))
	require.Equal(t, "LLM_API_KEY", llm.EnvKeyFor("unknown://x"))

	store := memory.New()
	ctx := context.Background()

	t.Run("env fallback", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "sk-test")
		got, err := llm.SecretFor(ctx, store, "openai://gpt-4")
		require.NoError(t, err)
		require.Equal(t, "sk-test", got)
	})

	t.Run("store wins over env", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "sk-env")
		require.NoError(t, store.Set(ctx, "OPENAI_API_KEY", []byte("sk-store")))
		got, err := llm.SecretFor(ctx, store, "openai://gpt-4")
		require.NoError(t, err)
		require.Equal(t, "sk-store", got)
		require.NoError(t, store.Delete(ctx, "OPENAI_API_KEY"))
	})

	t.Run("universal fallback", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "")
		t.Setenv("LLM_API_KEY", "sk-universal")
		got, err := llm.SecretFor(ctx, store, "openai://gpt-4")
		require.NoError(t, err)
		require.Equal(t, "sk-universal", got)
	})

	t.Run("not found", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "")
		t.Setenv("LLM_API_KEY", "")
		_, err := llm.SecretFor(ctx, store, "openai://gpt-4")
		require.Error(t, err)
	})
}
