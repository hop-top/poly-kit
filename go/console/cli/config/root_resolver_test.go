package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	kitcliconfig "hop.top/kit/go/console/cli/config"
)

func legacyFixture(t *testing.T) (cwd, legacy string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	legacy = filepath.Join(home, "old", "settings.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(legacy), 0o755))
	require.NoError(t, os.WriteFile(legacy, []byte(`{"a":1}`), 0o644))
	return t.TempDir(), legacy
}

func TestRootResolverListsLegacyAfterUser(t *testing.T) {
	cwd, legacy := legacyFixture(t)
	cfg := cli.Config{Name: "tool", LegacyUserConfigPaths: []string{legacy}}
	cmd := kitcliconfig.Command("tool", kitcliconfig.WithRootConfig(cfg))

	out, _, err := runCmd(t, cmd, "paths", "--from", cwd, "--format", "json")
	require.NoError(t, err)
	assert.Contains(t, out, `"source": "user-legacy"`)
	assert.Contains(t, out, legacy)

	cmd = kitcliconfig.Command("tool", kitcliconfig.WithRootConfig(cfg))
	got, _, err := runCmd(t, cmd, "path", "--from", cwd)
	require.NoError(t, err)
	assert.Equal(t, legacy, strings.TrimSpace(got), "path reports the legacy file in use")
}

func TestRootResolverCanonicalWinsOverLegacy(t *testing.T) {
	cwd, legacy := legacyFixture(t)
	canon := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "tool", "config.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(canon), 0o755))
	require.NoError(t, os.WriteFile(canon, []byte("a: 2\n"), 0o644))
	cfg := cli.Config{Name: "tool", LegacyUserConfigPaths: []string{legacy}}
	cmd := kitcliconfig.Command("tool", kitcliconfig.WithRootConfig(cfg))

	got, _, err := runCmd(t, cmd, "path", "--from", cwd)
	require.NoError(t, err)
	assert.Equal(t, canon, strings.TrimSpace(got))
}

func TestRootResolverEmptyFieldAddsNothing(t *testing.T) {
	cwd, _ := legacyFixture(t)
	cmd := kitcliconfig.Command("tool", kitcliconfig.WithRootConfig(cli.Config{Name: "tool"}))
	out, _, err := runCmd(t, cmd, "paths", "--from", cwd)
	require.NoError(t, err)
	assert.NotContains(t, out, "old/settings.json")
	assert.NotEmpty(t, out, "chain still resolved from the root name")
}

func TestRootResolverHonoursProjectMarker(t *testing.T) {
	cwd, _ := legacyFixture(t)
	cfg := cli.Config{Name: "tool", ProjectMarker: ".rlz/config.yaml"}
	cmd := kitcliconfig.Command("tool", kitcliconfig.WithRootConfig(cfg))
	out, _, err := runCmd(t, cmd, "paths", "--from", cwd)
	require.NoError(t, err)
	assert.Contains(t, out, filepath.Join(".rlz", "config.yaml"))
}

func TestRootResolverNoNameIsEmpty(t *testing.T) {
	cwd, legacy := legacyFixture(t)
	cmd := kitcliconfig.Command("tool", kitcliconfig.WithRootConfig(cli.Config{LegacyUserConfigPaths: []string{legacy}}))
	out, _, err := runCmd(t, cmd, "paths", "--from", cwd)
	require.NoError(t, err)
	assert.Empty(t, out)
}
