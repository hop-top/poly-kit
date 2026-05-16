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

func writeConfig(t *testing.T, dir, filename string, v any) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o750))
	data, err := yaml.Marshal(v)
	require.NoError(t, err)
	path := filepath.Join(dir, filename)
	require.NoError(t, os.WriteFile(path, data, 0o644))
	return path
}

func TestGet_Scalar(t *testing.T) {
	dir := t.TempDir()
	p := writeConfig(t, dir, "c.yaml", map[string]any{"name": "myapp"})

	v, err := config.Get("name", config.Options{ProjectConfigPath: p})
	require.NoError(t, err)
	assert.Equal(t, "myapp", v)
}

func TestGet_Nested(t *testing.T) {
	dir := t.TempDir()
	p := writeConfig(t, dir, "c.yaml", map[string]any{
		"core": map[string]any{"timeout": "30s"},
	})

	v, err := config.Get("core.timeout", config.Options{ProjectConfigPath: p})
	require.NoError(t, err)
	assert.Equal(t, "30s", v)
}

func TestGet_LayerPrecedence(t *testing.T) {
	sys := writeConfig(t, t.TempDir(), "c.yaml", map[string]any{"name": "sys"})
	usr := writeConfig(t, t.TempDir(), "c.yaml", map[string]any{"name": "usr"})
	proj := writeConfig(t, t.TempDir(), "c.yaml", map[string]any{"name": "proj"})

	v, err := config.Get("name", config.Options{
		SystemConfigPath:  sys,
		UserConfigPath:    usr,
		ProjectConfigPath: proj,
	})
	require.NoError(t, err)
	assert.Equal(t, "proj", v)
}

func TestGet_UserShadowsSystem(t *testing.T) {
	sys := writeConfig(t, t.TempDir(), "c.yaml", map[string]any{"name": "sys"})
	usr := writeConfig(t, t.TempDir(), "c.yaml", map[string]any{"name": "usr"})

	v, err := config.Get("name", config.Options{
		SystemConfigPath: sys,
		UserConfigPath:   usr,
	})
	require.NoError(t, err)
	assert.Equal(t, "usr", v)
}

func TestGet_NotFound(t *testing.T) {
	dir := t.TempDir()
	p := writeConfig(t, dir, "c.yaml", map[string]any{"name": "x"})

	_, err := config.Get("nonexistent", config.Options{ProjectConfigPath: p})
	assert.ErrorIs(t, err, config.ErrKeyNotFound)
}

func TestGet_Sequence(t *testing.T) {
	dir := t.TempDir()
	p := writeConfig(t, dir, "c.yaml", map[string]any{
		"tags": []string{"a", "b", "c"},
	})

	v, err := config.Get("tags", config.Options{ProjectConfigPath: p})
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "c"}, v)
}

func TestGet_MissingFile(t *testing.T) {
	_, err := config.Get("name", config.Options{
		ProjectConfigPath: "/nonexistent/path/c.yaml",
	})
	assert.ErrorIs(t, err, config.ErrKeyNotFound)
}

func TestGet_EmptyOptions(t *testing.T) {
	_, err := config.Get("name", config.Options{})
	assert.ErrorIs(t, err, config.ErrKeyNotFound)
}

// --- Backward compatibility (T-0413) ---

func TestLoad_StillWorks(t *testing.T) {
	dir := t.TempDir()
	p := writeConfig(t, dir, "c.yaml", map[string]any{
		"name": "loaded", "port": 9090,
	})

	type cfg struct {
		Name string `yaml:"name"`
		Port int    `yaml:"port"`
	}
	var c cfg
	err := config.Load(&c, config.Options{UserConfigPath: p})
	require.NoError(t, err)
	assert.Equal(t, "loaded", c.Name)
	assert.Equal(t, 9090, c.Port)
}

func TestLoad_ThenGet(t *testing.T) {
	dir := t.TempDir()
	p := writeConfig(t, dir, "c.yaml", map[string]any{
		"name": "consistent", "debug": true,
	})

	type cfg struct {
		Name  string `yaml:"name"`
		Debug bool   `yaml:"debug"`
	}
	var c cfg
	require.NoError(t, config.Load(&c, config.Options{UserConfigPath: p}))

	v, err := config.Get("name", config.Options{UserConfigPath: p})
	require.NoError(t, err)
	assert.Equal(t, c.Name, v)
}
