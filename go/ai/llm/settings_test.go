package llm_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
)

// One provider block configures a scheme under any of its names, for
// every reader: ProviderSettingsFor, LoadConfig and key resolution.
func TestProviderSettingsFor_FollowsAliases(t *testing.T) {
	cases := []struct {
		name, yamlBlock, scheme string
	}{
		{"alias reads the registered scheme's block", "fireworks", "fireworks-ai"},
		{"registered scheme reads the catalog id's block", "fireworks-ai", "fireworks"},
		{"alias reads the registered scheme's block (together)", "together", "togetherai"},
		{"catalog id reads its alias's block", "gemini", "google"},
		{"alias reads the catalog id's block", "google", "gemini"},
	}
	for _, catalog := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				isolateKeys(t)
				if catalog {
					useCatalog(t, fixtureProviders()...)
				}
				writeLLMConfig(t, "providers:\n  "+tc.yamlBlock+":\n"+
					"    base_url: http://127.0.0.1:9/v1\n"+
					"    model: m-file\n"+
					"    api_key: fake-file\n"+
					"    api_key_env: MY_KEY\n"+
					"    organization: org-1\n")

				got, ok := llm.ProviderSettingsFor(tc.scheme + "://m")
				require.True(t, ok)
				assert.Equal(t, llm.ProviderSettings{
					Block:     tc.yamlBlock,
					BaseURL:   "http://127.0.0.1:9/v1",
					Model:     "m-file",
					APIKeyEnv: "MY_KEY",
					Extras:    map[string]any{"organization": "org-1"},
				}, got)

				cfg, err := llm.LoadConfig(tc.scheme + "://m")
				require.NoError(t, err)
				assert.Equal(t, "http://127.0.0.1:9/v1", cfg.Provider.BaseURL)
				assert.Equal(t, "org-1", cfg.Provider.Extras["organization"])
				assert.Equal(t, "fake-file", cfg.Provider.APIKey)

				uri, err := llm.ApplyAPIKey(context.Background(), nil, tc.scheme+"://m")
				require.NoError(t, err)
				assert.Equal(t, tc.scheme+"://m?api_key=fake-file", uri)
			})
		}
	}
}

// The scheme's own block outranks an alias's; blocks are never merged.
func TestProviderSettingsFor_OwnBlockWins(t *testing.T) {
	isolateKeys(t)
	writeLLMConfig(t, `providers:
  fireworks:
    base_url: http://127.0.0.1:9/registered
    api_key: fake-registered
  fireworks-ai:
    base_url: http://127.0.0.1:9/own
`)
	got, ok := llm.ProviderSettingsFor("fireworks-ai")
	require.True(t, ok)
	assert.Equal(t, "fireworks-ai", got.Block)
	assert.Equal(t, "http://127.0.0.1:9/own", got.BaseURL)

	// The own block sets no key: the alias block's is not merged in.
	_, err := llm.ApplyAPIKey(context.Background(), nil, "fireworks-ai://m")
	require.ErrorIs(t, err, llm.ErrMissingKey)
	cfg, err := llm.LoadConfig("fireworks-ai://m")
	require.NoError(t, err)
	assert.Empty(t, cfg.Provider.APIKey)
}

// A catalog provider routed by protocol never reads the block of the
// adapter that speaks its protocol: that block configures another host,
// and its key would go to this one.
func TestProviderSettingsFor_ProtocolRouteNeverBorrowsClaimantBlock(t *testing.T) {
	isolateKeys(t)
	useCatalog(t, fixtureProviders()...)
	writeLLMConfig(t, `providers:
  openai:
    api_key: fake-openai-file
    base_url: http://127.0.0.1:9/openai
`)
	t.Setenv("DIGITALOCEAN_ACCESS_TOKEN", "fake-do")

	_, ok := llm.ProviderSettingsFor("digitalocean")
	assert.False(t, ok)

	uri, err := llm.ApplyAPIKey(context.Background(), nil, "digitalocean://m")
	require.NoError(t, err)
	assert.Equal(t, "digitalocean://m?api_key=fake-do", uri)

	sec, err := llm.SecretFor(context.Background(), nil, "digitalocean")
	require.NoError(t, err)
	assert.Equal(t, "fake-do", sec)

	cfg, err := llm.LoadConfig("digitalocean://m")
	require.NoError(t, err)
	assert.Empty(t, cfg.Provider.BaseURL)
	assert.NotEqual(t, "fake-openai-file", cfg.Provider.APIKey)
}

// A protocol route still reads its own block.
func TestProviderSettingsFor_ProtocolRouteOwnBlock(t *testing.T) {
	isolateKeys(t)
	useCatalog(t, fixtureProviders()...)
	writeLLMConfig(t, "providers:\n  digitalocean:\n    base_url: http://127.0.0.1:9/do\n")
	got, ok := llm.ProviderSettingsFor("digitalocean://m")
	require.True(t, ok)
	assert.Equal(t, "digitalocean", got.Block)
	assert.Equal(t, "http://127.0.0.1:9/do", got.BaseURL)
}

func TestProviderSettingsFor_NoBlock(t *testing.T) {
	isolateKeys(t)
	writeLLMConfig(t, "providers:\n  openrouter:\n    base_url: http://127.0.0.1:9/v1\n")
	_, ok := llm.ProviderSettingsFor("groq")
	assert.False(t, ok)
	_, ok = llm.ProviderSettingsFor("no-such-scheme")
	assert.False(t, ok)
}

// A scheme no adapter serves reads its own block and nothing else.
func TestProviderSettingsFor_UnknownSchemeOwnBlockOnly(t *testing.T) {
	isolateKeys(t)
	writeLLMConfig(t, "providers:\n  no-such-scheme:\n    base_url: http://127.0.0.1:9/v1\n")
	got, ok := llm.ProviderSettingsFor("no-such-scheme://m")
	require.True(t, ok)
	assert.Equal(t, "http://127.0.0.1:9/v1", got.BaseURL)
}
