package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/config"
)

func TestList_SingleLayer(t *testing.T) {
	dir := t.TempDir()
	p := writeConfig(t, dir, "c.yaml", map[string]any{"a": 1, "b": 2})

	entries, err := config.List(config.Options{ProjectConfigPath: p})
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, "a", entries[0].Key)
	assert.Equal(t, "b", entries[1].Key)
}

func TestList_MultiLayer(t *testing.T) {
	sys := writeConfig(t, t.TempDir(), "c.yaml", map[string]any{"a": "sys"})
	usr := writeConfig(t, t.TempDir(), "c.yaml", map[string]any{
		"a": "usr", "b": "bval",
	})

	entries, err := config.List(config.Options{
		SystemConfigPath: sys,
		UserConfigPath:   usr,
	})
	require.NoError(t, err)
	require.Len(t, entries, 2)

	m := make(map[string]config.Entry, len(entries))
	for _, e := range entries {
		m[e.Key] = e
	}
	assert.Equal(t, "usr", m["a"].Value, "user shadows system")
	assert.Equal(t, "bval", m["b"].Value)
}

func TestList_NestedKeys(t *testing.T) {
	dir := t.TempDir()
	p := writeConfig(t, dir, "c.yaml", map[string]any{
		"core": map[string]any{"x": 1, "y": 2},
	})

	entries, err := config.List(config.Options{ProjectConfigPath: p})
	require.NoError(t, err)

	keys := make([]string, len(entries))
	for i, e := range entries {
		keys[i] = e.Key
	}
	assert.ElementsMatch(t, []string{"core.x", "core.y"}, keys)
}

func TestList_EmptyOptions(t *testing.T) {
	entries, err := config.List(config.Options{})
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestList_ScopeTagged(t *testing.T) {
	sys := writeConfig(t, t.TempDir(), "c.yaml", map[string]any{"a": "s"})
	usr := writeConfig(t, t.TempDir(), "c.yaml", map[string]any{"b": "u"})
	proj := writeConfig(t, t.TempDir(), "c.yaml", map[string]any{"c": "p"})

	entries, err := config.List(config.Options{
		SystemConfigPath:  sys,
		UserConfigPath:    usr,
		ProjectConfigPath: proj,
	})
	require.NoError(t, err)

	m := make(map[string]config.Entry, len(entries))
	for _, e := range entries {
		m[e.Key] = e
	}
	assert.Equal(t, config.ScopeSystem, m["a"].Scope)
	assert.Equal(t, config.ScopeUser, m["b"].Scope)
	assert.Equal(t, config.ScopeProject, m["c"].Scope)
}

func TestList_MissingFiles(t *testing.T) {
	entries, err := config.List(config.Options{
		SystemConfigPath: "/nonexistent/sys.yaml",
		UserConfigPath:   "/nonexistent/usr.yaml",
	})
	require.NoError(t, err)
	assert.Empty(t, entries)
}
