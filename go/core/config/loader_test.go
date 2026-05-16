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

type testCfg struct {
	Name  string `yaml:"name"`
	Debug bool   `yaml:"debug"`
	Port  int    `yaml:"port"`
}

func writeYAML(t *testing.T, dir, filename string, v any) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o750))
	data, err := yaml.Marshal(v)
	require.NoError(t, err)
	path := filepath.Join(dir, filename)
	require.NoError(t, os.WriteFile(path, data, 0o644))
	return path
}

func TestLoader_UserOverridesDefault(t *testing.T) {
	userDir := t.TempDir()
	writeYAML(t, userDir, "config.yaml", map[string]any{"name": "user-name"})
	var cfg testCfg
	cfg.Name = "default"
	cfg.Port = 8080
	err := config.Load(&cfg, config.Options{
		UserConfigPath: filepath.Join(userDir, "config.yaml"),
	})
	require.NoError(t, err)
	assert.Equal(t, "user-name", cfg.Name)
	assert.Equal(t, 8080, cfg.Port)
}

func TestLoader_ProjectOverridesUser(t *testing.T) {
	userDir, projDir := t.TempDir(), t.TempDir()
	writeYAML(t, userDir, "config.yaml", map[string]any{"name": "user"})
	writeYAML(t, projDir, "config.yaml", map[string]any{"name": "project"})
	var cfg testCfg
	err := config.Load(&cfg, config.Options{
		UserConfigPath:    filepath.Join(userDir, "config.yaml"),
		ProjectConfigPath: filepath.Join(projDir, "config.yaml"),
	})
	require.NoError(t, err)
	assert.Equal(t, "project", cfg.Name)
}

func TestLoader_EnvOverride(t *testing.T) {
	t.Setenv("MY_DEBUG", "true")
	var cfg testCfg
	err := config.Load(&cfg, config.Options{
		EnvOverride: func(c any) {
			if os.Getenv("MY_DEBUG") == "true" {
				c.(*testCfg).Debug = true
			}
		},
	})
	require.NoError(t, err)
	assert.True(t, cfg.Debug)
}

func TestLoader_MissingFilesAreSkipped(t *testing.T) {
	var cfg testCfg
	cfg.Name = "default"
	err := config.Load(&cfg, config.Options{
		UserConfigPath:    "/nonexistent/config.yaml",
		ProjectConfigPath: "/also/nonexistent.yaml",
	})
	require.NoError(t, err)
	assert.Equal(t, "default", cfg.Name)
}
