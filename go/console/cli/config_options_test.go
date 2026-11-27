package cli_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	coreconfig "hop.top/kit/go/core/config"
)

func optionsFixture(t *testing.T) (cwd string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	return t.TempDir()
}

func TestConfigOptionsFromCarriesLegacyPaths(t *testing.T) {
	cwd := optionsFixture(t)
	legacy := []string{"~/.old/a.json", "~/.old/b.yaml"}
	cfg := cli.Config{Name: "tool", LegacyUserConfigPaths: legacy}

	got := cfg.ConfigOptionsFrom(cwd)

	assert.Equal(t, legacy, got.LegacyUserConfigPaths)
	want := coreconfig.OptionsForToolWithMarkersFrom(cwd, "tool", nil)
	assert.Equal(t, want.UserConfigPath, got.UserConfigPath)
	assert.Equal(t, want.SystemConfigPath, got.SystemConfigPath)
	assert.Equal(t, want.ProjectConfigPath, got.ProjectConfigPath)
}

func TestConfigOptionsFromHonoursProjectMarker(t *testing.T) {
	cwd := optionsFixture(t)
	marker := filepath.Join(".rlz", "config.yaml")
	p := filepath.Join(cwd, marker)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte("a: 1\n"), 0o644))

	got := cli.Config{Name: "tool", ProjectMarker: marker}.ConfigOptionsFrom(cwd)

	assert.Equal(t, p, got.ProjectConfigPath)
}

func TestConfigOptionsFromDefaultMarkersWithoutProjectMarker(t *testing.T) {
	cwd := optionsFixture(t)
	want := coreconfig.OptionsForToolWithMarkersFrom(cwd, "tool", nil)
	got := cli.Config{Name: "tool"}.ConfigOptionsFrom(cwd)
	assert.Equal(t, want, got)
	assert.Empty(t, got.LegacyUserConfigPaths)
}

func TestConfigOptionsFromEmptyNameIsZero(t *testing.T) {
	cwd := optionsFixture(t)
	got := cli.Config{LegacyUserConfigPaths: []string{"~/.old/a.json"}}.ConfigOptionsFrom(cwd)
	assert.Equal(t, coreconfig.Options{}, got)
	assert.Nil(t, cli.Config{}.ConfigPathsFrom(cwd))
}

func TestConfigOptionsUsesProcessCwd(t *testing.T) {
	cwd := optionsFixture(t)
	t.Chdir(cwd)
	cfg := cli.Config{Name: "tool", LegacyUserConfigPaths: []string{"~/.old/a.json"}}
	assert.Equal(t, cfg.ConfigOptionsFrom(cwd), cfg.ConfigOptions())
}

func TestConfigPathsFromListsLegacyAfterUser(t *testing.T) {
	cwd := optionsFixture(t)
	cfg := cli.Config{Name: "tool", LegacyUserConfigPaths: []string{"~/.old/a.json"}}
	chain := cfg.ConfigPathsFrom(cwd)
	var sources []string
	for _, p := range chain {
		sources = append(sources, p.Source)
	}
	assert.Contains(t, sources, coreconfig.SourceUserLegacy)
	for i, s := range sources {
		if s == coreconfig.SourceUserLegacy {
			assert.Equal(t, "user", sources[i-1])
		}
	}
}
