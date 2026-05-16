package xdg_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/xdg"
)

func TestConfigDir_XDGOverride(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-cfg")
	dir, err := xdg.ConfigDir("mytool")
	require.NoError(t, err)
	assert.Equal(t, "/tmp/xdg-cfg/mytool", dir)
}

func TestDataDir_XDGOverride(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg-data")
	dir, err := xdg.DataDir("mytool")
	require.NoError(t, err)
	assert.Equal(t, "/tmp/xdg-data/mytool", dir)
}

func TestCacheDir_XDGOverride(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/tmp/xdg-cache")
	dir, err := xdg.CacheDir("mytool")
	require.NoError(t, err)
	assert.Equal(t, "/tmp/xdg-cache/mytool", dir)
}

func TestStateDir_XDGOverride(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/tmp/xdg-state")
	dir, err := xdg.StateDir("mytool")
	require.NoError(t, err)
	assert.Equal(t, "/tmp/xdg-state/mytool", dir)
}

func TestConfigDir_FallbackContainsTool(t *testing.T) {
	os.Unsetenv("XDG_CONFIG_HOME")
	dir, err := xdg.ConfigDir("mytool")
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(dir, filepath.Join("mytool")),
		"expected path to end with tool name, got: %s", dir)
}

func TestRuntimeDir_XDGOverride(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/tmp/xdg-runtime")
	dir, err := xdg.RuntimeDir("mytool")
	require.NoError(t, err)
	assert.Equal(t, "/tmp/xdg-runtime/mytool", dir)
}

func TestBinHome_XDGOverride(t *testing.T) {
	t.Setenv("XDG_BIN_HOME", "/tmp/xdg-bin")
	dir, err := xdg.BinHome("mytool")
	require.NoError(t, err)
	assert.Equal(t, "/tmp/xdg-bin/mytool", dir)
}

func TestConfigFile_CreatesParents(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	path, err := xdg.ConfigFile("mytool", "app.yaml")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "mytool", "app.yaml"), path)
	// Parent dir must exist after the call.
	info, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestStateFile_CreatesParents(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", root)
	path, err := xdg.StateFile("mytool", "sub/app.state")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "mytool", "sub", "app.state"), path)
	info, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestSearchConfigFile_FindsExisting(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	target := filepath.Join(root, "mytool", "app.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o750))
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o600))

	found, err := xdg.SearchConfigFile("mytool", "app.yaml")
	require.NoError(t, err)
	assert.Equal(t, target, found)
}

func TestSearchConfigFile_MissingReturnsError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_DIRS", t.TempDir())
	_, err := xdg.SearchConfigFile("mytool", "missing.yaml")
	assert.Error(t, err)
}

func TestHome_NotEmpty(t *testing.T) {
	assert.NotEmpty(t, xdg.Home())
}

func TestUserDir_KnownNames(t *testing.T) {
	for _, name := range []string{
		"desktop", "download", "downloads", "documents",
		"music", "pictures", "videos", "templates",
		"publicshare", "public",
	} {
		dir, err := xdg.UserDir(name)
		require.NoError(t, err, "name=%s", name)
		assert.NotEmpty(t, dir, "name=%s", name)
	}
}

func TestUserDir_CaseInsensitive(t *testing.T) {
	a, err := xdg.UserDir("Documents")
	require.NoError(t, err)
	b, err := xdg.UserDir("DOCUMENTS")
	require.NoError(t, err)
	assert.Equal(t, a, b)
}

func TestUserDir_UnknownReturnsError(t *testing.T) {
	_, err := xdg.UserDir("photos")
	assert.Error(t, err)
}

func TestUserDirs_ReturnsAll(t *testing.T) {
	dirs := xdg.UserDirs()
	assert.NotEmpty(t, dirs.Desktop)
	assert.NotEmpty(t, dirs.Documents)
	assert.NotEmpty(t, dirs.Download)
}
