package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
	"hop.top/kit/go/core/config"
)

func TestUnset_ExistingKey(t *testing.T) {
	dir := t.TempDir()
	p := writeConfig(t, dir, "c.yaml", map[string]any{
		"name": "x", "port": 8080,
	})
	opts := config.Options{ProjectConfigPath: p}

	require.NoError(t, config.Unset("name", config.ScopeProject, opts))

	// name gone, port remains.
	_, err := config.Get("name", opts)
	assert.ErrorIs(t, err, config.ErrKeyNotFound)
	v, err := config.Get("port", opts)
	require.NoError(t, err)
	assert.Equal(t, "8080", v)
}

func TestUnset_NestedKey(t *testing.T) {
	dir := t.TempDir()
	p := writeConfig(t, dir, "c.yaml", map[string]any{
		"core": map[string]any{"a": 1, "b": 2},
	})
	opts := config.Options{ProjectConfigPath: p}

	require.NoError(t, config.Unset("core.a", config.ScopeProject, opts))

	_, err := config.Get("core.a", opts)
	assert.ErrorIs(t, err, config.ErrKeyNotFound)

	v, err := config.Get("core.b", opts)
	require.NoError(t, err)
	assert.Equal(t, "2", v)
}

func TestUnset_CleanupEmptyParent(t *testing.T) {
	dir := t.TempDir()
	p := writeConfig(t, dir, "c.yaml", map[string]any{
		"core": map[string]any{"only": "val"},
	})
	opts := config.Options{ProjectConfigPath: p}

	require.NoError(t, config.Unset("core.only", config.ScopeProject, opts))

	// "core" mapping should be pruned too.
	_, err := config.Get("core", opts)
	assert.ErrorIs(t, err, config.ErrKeyNotFound)

	// File should still be valid YAML.
	data, err := os.ReadFile(p)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, yaml.Unmarshal(data, &out))
	assert.NotContains(t, out, "core")
}

func TestUnset_NotFound(t *testing.T) {
	dir := t.TempDir()
	p := writeConfig(t, dir, "c.yaml", map[string]any{"a": 1})
	opts := config.Options{ProjectConfigPath: p}

	err := config.Unset("nonexistent", config.ScopeProject, opts)
	assert.ErrorIs(t, err, config.ErrKeyNotFound)
}

func TestUnset_EmptyScope(t *testing.T) {
	err := config.Unset("key", config.ScopeProject, config.Options{})
	assert.ErrorIs(t, err, config.ErrEmptyScope)
}

func TestUnset_NonExistentFile(t *testing.T) {
	opts := config.Options{
		ProjectConfigPath: filepath.Join(t.TempDir(), "missing.yaml"),
	}

	err := config.Unset("key", config.ScopeProject, opts)
	require.Error(t, err)
	// parseOrCreateDoc returns empty doc for missing file, so we get
	// ErrKeyNotFound (key doesn't exist in the empty doc).
	assert.ErrorIs(t, err, config.ErrKeyNotFound)
}
