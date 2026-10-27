package llm_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	_ "hop.top/kit/go/ai/llm/anthropic"
	llmerrors "hop.top/kit/go/ai/llm/errors"
)

type nopProvider struct{}

func (nopProvider) Close() error { return nil }

// hermeticRegistry registers "fake", which also claims the
// openai-compatible protocol, recording each config it is built from.
func hermeticRegistry(got *[]llm.ResolvedConfig) *llm.Registry {
	r := llm.NewRegistry()
	r.Register("fake", func(cfg llm.ResolvedConfig) (llm.Provider, error) {
		*got = append(*got, cfg)
		return nopProvider{}, nil
	}, llm.Declaration{Protocols: []string{"openai-compatible"}})
	return r
}

func TestResolveHermeticReachesTheAdapter(t *testing.T) {
	var got []llm.ResolvedConfig
	r := hermeticRegistry(&got)

	_, err := r.Resolve("fake://m?api_key=k")
	require.NoError(t, err)
	_, err = r.Resolve("fake://m?api_key=k", llm.Hermetic())
	require.NoError(t, err)

	require.Len(t, got, 2)
	assert.False(t, got[0].Hermetic, "ambient by default")
	assert.True(t, got[1].Hermetic, "Hermetic tells the adapter")
	assert.Equal(t, "k", got[1].Provider.APIKey, "the URI still counts")
}

func TestResolveHermeticReadsNoCatalog(t *testing.T) {
	useCatalog(t, fixtureProviders()...)
	var got []llm.ResolvedConfig
	r := hermeticRegistry(&got)

	_, err := r.Resolve("deepseek://deepseek-chat")
	require.NoError(t, err, "a catalog provider resolves by protocol")
	require.Len(t, got, 1)

	_, err = r.Resolve("deepseek://deepseek-chat", llm.Hermetic())
	var nf *llmerrors.ErrProviderNotFound
	require.True(t, errors.As(err, &nf), "hermetic reads no catalog cache: %v", err)
	assert.Len(t, got, 1)
}

func TestPackageResolveTakesHermetic(t *testing.T) {
	// The package-level helper forwards options to DefaultRegistry.
	_, err := llm.Resolve("no-such-scheme://m", llm.Hermetic())
	var nf *llmerrors.ErrProviderNotFound
	assert.True(t, errors.As(err, &nf))
}

func TestHermeticAnthropicReadsNoConfigFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "hop"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hop", "llm.yaml"),
		[]byte("providers:\n  anthropic:\n    base_url: https://gw.corp.test\n"), 0o600))
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "env-token")
	t.Setenv("ANTHROPIC_WORKSPACE_ID", "ws-1")
	uri := "anthropic://m?api_key=k&base_url=https://gw.corp.test"

	reads := llm.CountConfigFileReads(t)
	_, err := llm.Resolve(uri)
	require.NoError(t, err)
	require.Positive(t, *reads, "control: an ambient adapter consults llm.yaml to trust the env token")

	*reads = 0
	_, err = llm.Resolve(uri, llm.Hermetic())
	require.NoError(t, err)
	assert.Zero(t, *reads, "hermetic reads no llm.yaml")
}
