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
	assert.Equal(t, []any{"a", "b", "c"}, v)
}

func TestGet_PreservesScalarTypes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.yaml")
	src := "" +
		"port: 8080\n" +
		"ratio: 0.9\n" +
		"debug: true\n" +
		"disabled: false\n" +
		"unset: null\n" +
		"name: myapp\n" +
		"quoted: \"8080\"\n" +
		"yes_key: yes\n" +
		"on_key: on\n" +
		"off_key: off\n" +
		"no_key: no\n" +
		"released: 2024-01-01\n"
	require.NoError(t, os.WriteFile(p, []byte(src), 0o644))

	tests := []struct {
		key  string
		want any
	}{
		{"port", 8080},
		{"ratio", 0.9},
		{"debug", true},
		{"disabled", false},
		{"unset", nil},
		{"name", "myapp"},
		// Quoted in the file, so genuinely a string: the inverse case.
		{"quoted", "8080"},
		// YAML 1.1 boolean lookalikes stay strings under YAML 1.2.
		{"yes_key", "yes"},
		{"on_key", "on"},
		{"off_key", "off"},
		{"no_key", "no"},
		// !!timestamp is deliberately surfaced as a string.
		{"released", "2024-01-01"},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			v, err := config.Get(tt.key, config.Options{ProjectConfigPath: p})
			require.NoError(t, err)
			assert.Equal(t, tt.want, v)
		})
	}
}

func TestGet_SequencePreservesElementTypes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.yaml")
	require.NoError(t, os.WriteFile(p,
		[]byte("mixed: [1, 2.5, three, true, null]\n"), 0o644))

	v, err := config.Get("mixed", config.Options{ProjectConfigPath: p})
	require.NoError(t, err)
	assert.Equal(t, []any{1, 2.5, "three", true, nil}, v)
}

func TestGet_NestedMappingPreservesTypes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.yaml")
	require.NoError(t, os.WriteFile(p,
		[]byte("core:\n  server:\n    port: 8080\n    tls: true\n"), 0o644))

	v, err := config.Get("core", config.Options{ProjectConfigPath: p})
	require.NoError(t, err)

	top, ok := v.(map[string]any)
	require.True(t, ok)
	server, ok := top["server"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, 8080, server["port"])
	assert.Equal(t, true, server["tls"])

	// Reaching the same leaf directly agrees with the recursive path.
	leaf, err := config.Get("core.server.port", config.Options{ProjectConfigPath: p})
	require.NoError(t, err)
	assert.Equal(t, 8080, leaf)
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
