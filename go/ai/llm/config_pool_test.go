package llm_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
)

// writePoolConfig drops llm.yaml into XDG_CONFIG_HOME/hop and points the
// runtime at it. Matches the layout loadConfigFile expects: kit's xdg helper
// resolves "hop" to "$XDG_CONFIG_HOME/hop".
func writePoolConfig(t *testing.T, body string) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	hopDir := filepath.Join(tmp, "hop")
	require.NoError(t, os.MkdirAll(hopDir, 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(hopDir, "llm.yaml"), []byte(body), 0o644,
	))
}

func TestLoadPool_BasicFixture(t *testing.T) {
	writePoolConfig(t, `pool:
  - alias: fast
    scheme: openai
    model: gpt-4o-mini
  - alias: smart
    scheme: anthropic
    model: claude-sonnet-4-5
    weight: 2.5
  - alias: off
    scheme: openai
    model: gpt-3.5-turbo
    enabled: false
`)

	got, err := llm.LoadPool()
	require.NoError(t, err)
	require.Len(t, got, 3)

	// Declaration order preserved.
	assert.Equal(t, "fast", got[0].Alias)
	assert.Equal(t, "openai", got[0].Scheme)
	assert.Equal(t, "gpt-4o-mini", got[0].Model)
	assert.True(t, got[0].Enabled, "default Enabled must be true when omitted")
	assert.Equal(t, 1.0, got[0].Weight, "default Weight must be 1.0 when omitted")

	assert.Equal(t, "smart", got[1].Alias)
	assert.True(t, got[1].Enabled)
	assert.Equal(t, 2.5, got[1].Weight)

	assert.Equal(t, "off", got[2].Alias)
	assert.False(t, got[2].Enabled, "explicit enabled:false must round-trip")
	assert.Equal(t, 1.0, got[2].Weight)
}

func TestLoadPool_NoPoolBlock(t *testing.T) {
	writePoolConfig(t, `default: openai://gpt-4o
providers:
  openai:
    api_key: sk-test
`)
	got, err := llm.LoadPool()
	require.NoError(t, err)
	assert.Nil(t, got, "missing pool block must return nil slice, nil error")
}

func TestLoadPool_EnvDisable(t *testing.T) {
	writePoolConfig(t, `pool:
  - alias: fast
    scheme: openai
    model: gpt-4o-mini
  - alias: smart
    scheme: anthropic
    model: claude-sonnet-4-5
  - alias: cheap
    scheme: openai
    model: gpt-3.5-turbo
`)
	// disable one by alias, one by scheme:model.
	t.Setenv("LLM_POOL_DISABLE", "fast, anthropic:claude-sonnet-4-5")

	got, err := llm.LoadPool()
	require.NoError(t, err)
	require.Len(t, got, 3)

	assert.False(t, got[0].Enabled, "fast disabled by alias match")
	assert.False(t, got[1].Enabled, "smart disabled by scheme:model match")
	assert.True(t, got[2].Enabled, "cheap untouched")
}

func TestResolvePool_CliOverride(t *testing.T) {
	entries := []llm.PoolEntry{
		{Alias: "fast", Scheme: "openai", Model: "gpt-4o-mini", Enabled: true, Weight: 1.0},
		{Alias: "smart", Scheme: "anthropic", Model: "claude-sonnet-4-5", Enabled: true, Weight: 1.0},
		{Alias: "", Scheme: "openai", Model: "gpt-3.5-turbo", Enabled: true, Weight: 1.0},
	}

	got := llm.ResolvePool(entries, []string{"fast", "openai:gpt-3.5-turbo"})
	require.Len(t, got, 3)
	assert.False(t, got[0].Enabled)
	assert.True(t, got[1].Enabled)
	assert.False(t, got[2].Enabled)

	// Source slice must not be mutated.
	assert.True(t, entries[0].Enabled, "ResolvePool must return a copy, not mutate input")
	assert.True(t, entries[2].Enabled)
}

func TestResolvePool_EmptyDisableList(t *testing.T) {
	entries := []llm.PoolEntry{
		{Alias: "a", Scheme: "openai", Model: "gpt-4o", Enabled: true, Weight: 1.0},
	}
	got := llm.ResolvePool(entries, nil)
	require.Len(t, got, 1)
	assert.True(t, got[0].Enabled)
}
