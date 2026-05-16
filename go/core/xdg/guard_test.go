package xdg_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/xdg"
)

// withGuard installs g for the duration of the test and restores the prior
// guard on cleanup.
func withGuard(t *testing.T, g xdg.Guard) {
	t.Helper()
	prev := xdg.SetGuard(g)
	t.Cleanup(func() { xdg.SetGuard(prev) })
}

func TestSetGuard_DefaultIsPermissive(t *testing.T) {
	withGuard(t, nil)
	t.Setenv("XDG_CONFIG_HOME", "/tmp/x")
	dir, err := xdg.ConfigDir("mytool")
	require.NoError(t, err)
	assert.Equal(t, "/tmp/x/mytool", dir)
}

func TestSetGuard_RejectsConfigDir(t *testing.T) {
	rejected := errors.New("denied")
	withGuard(t, func(string, xdg.Op) error { return rejected })

	t.Setenv("XDG_CONFIG_HOME", "/tmp/x")
	_, err := xdg.ConfigDir("mytool")
	require.Error(t, err)
	assert.True(t, errors.Is(err, rejected), "guard error should propagate")
}

func TestSetGuard_RejectsDataDir(t *testing.T) {
	rejected := errors.New("nope")
	withGuard(t, func(string, xdg.Op) error { return rejected })

	t.Setenv("XDG_DATA_HOME", "/tmp/y")
	_, err := xdg.DataDir("mytool")
	require.Error(t, err)
	assert.True(t, errors.Is(err, rejected))
}

func TestSetGuard_RejectsCacheDir(t *testing.T) {
	rejected := errors.New("nope")
	withGuard(t, func(string, xdg.Op) error { return rejected })

	t.Setenv("XDG_CACHE_HOME", "/tmp/c")
	_, err := xdg.CacheDir("mytool")
	require.Error(t, err)
	assert.True(t, errors.Is(err, rejected))
}

func TestSetGuard_RejectsStateDir(t *testing.T) {
	rejected := errors.New("nope")
	withGuard(t, func(string, xdg.Op) error { return rejected })

	t.Setenv("XDG_STATE_HOME", "/tmp/s")
	_, err := xdg.StateDir("mytool")
	require.Error(t, err)
	assert.True(t, errors.Is(err, rejected))
}

func TestSetGuard_OpHintIsWriteForDirs(t *testing.T) {
	var seen xdg.Op
	withGuard(t, func(_ string, op xdg.Op) error { seen = op; return nil })
	t.Setenv("XDG_CONFIG_HOME", "/tmp/x")

	_, err := xdg.ConfigDir("mytool")
	require.NoError(t, err)
	assert.Equal(t, xdg.OpWrite, seen, "Dir functions should signal write intent")
}

func TestRawDirs_BypassGuard(t *testing.T) {
	withGuard(t, func(string, xdg.Op) error { return errors.New("would block") })

	t.Setenv("XDG_CONFIG_HOME", "/tmp/x")
	t.Setenv("XDG_DATA_HOME", "/tmp/y")
	t.Setenv("XDG_CACHE_HOME", "/tmp/c")
	t.Setenv("XDG_STATE_HOME", "/tmp/s")

	c, err := xdg.RawConfigDir("mytool")
	require.NoError(t, err)
	assert.Equal(t, "/tmp/x/mytool", c)

	d, err := xdg.RawDataDir("mytool")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/tmp/y", "mytool"), d)

	ca, err := xdg.RawCacheDir("mytool")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/tmp/c", "mytool"), ca)

	s, err := xdg.RawStateDir("mytool")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/tmp/s", "mytool"), s)
}

func TestSetGuard_RestoresPrevious(t *testing.T) {
	called := 0
	g1 := xdg.Guard(func(string, xdg.Op) error { called++; return nil })
	prev := xdg.SetGuard(g1)
	t.Cleanup(func() { xdg.SetGuard(prev) })

	t.Setenv("XDG_CONFIG_HOME", "/tmp/x")
	_, _ = xdg.ConfigDir("a")
	require.Equal(t, 1, called)

	g2Called := 0
	prev2 := xdg.SetGuard(func(string, xdg.Op) error { g2Called++; return nil })
	_, _ = xdg.ConfigDir("a")
	assert.Equal(t, 1, g2Called)
	assert.Equal(t, 1, called, "g1 should not be called after replacement")

	xdg.SetGuard(prev2)
	_, _ = xdg.ConfigDir("a")
	assert.Equal(t, 2, called, "restoring prev2 should re-activate g1")
}
