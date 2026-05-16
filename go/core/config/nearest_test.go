package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/config"
)

func TestNearest_FindsMarkerInStartDir(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".rlz"), 0o755))
	want := filepath.Join(root, ".rlz", "config.yaml")
	require.NoError(t, os.WriteFile(want, []byte("registry: {}\n"), 0o644))

	got, err := config.Nearest(root, ".rlz/config.yaml")
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestNearest_WalksUp(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b", "c")
	require.NoError(t, os.MkdirAll(deep, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".rlz"), 0o755))
	want := filepath.Join(root, ".rlz", "config.yaml")
	require.NoError(t, os.WriteFile(want, []byte("x: 1\n"), 0o644))

	got, err := config.Nearest(deep, ".rlz/config.yaml")
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestNearest_NotFound(t *testing.T) {
	root := t.TempDir()
	_, err := config.Nearest(root, ".does-not-exist/c.yaml")
	assert.ErrorIs(t, err, config.ErrNotFound)
}

func TestNearest_RespectsMaxDepth(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b", "c", "d", "e")
	require.NoError(t, os.MkdirAll(deep, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".rlz"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".rlz", "config.yaml"), []byte("x: 1\n"), 0o644))

	// Cap depth at 2 — marker is 5 dirs up, must not be found.
	_, err := config.NearestWithDepth(deep, ".rlz/config.yaml", 2)
	assert.ErrorIs(t, err, config.ErrNotFound)
}

func TestNearest_DirectoryMarker(t *testing.T) {
	// marker is just a dir name (no file inside).
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sub", ".rlz"), 0o755))

	got, err := config.Nearest(filepath.Join(root, "sub"), ".rlz")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "sub", ".rlz"), got)
}

func TestNearest_EmptyMarker(t *testing.T) {
	_, err := config.Nearest(t.TempDir(), "")
	require.Error(t, err)
}

func TestNearest_EmptyStartDirUsesCwd(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".rlz"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".rlz", "config.yaml"), []byte("x: 1\n"), 0o644))

	old, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(old) })
	require.NoError(t, os.Chdir(root))

	got, err := config.Nearest("", ".rlz/config.yaml")
	require.NoError(t, err)
	// macOS's TempDir resolves through /private; compare via filepath.EvalSymlinks-style suffix.
	assert.Contains(t, got, filepath.Join(".rlz", "config.yaml"))
}
