package xdg_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/xdg"
)

// TestConcurrentResolve_NoRace hammers every exported resolver from many
// goroutines at once: the six directory functions and their Raw*
// variants, all five *File helpers, all five Search*File helpers, and
// the non-path accessors (Home, UserDir, UserDirs, FontDirs,
// ApplicationDirs). Every one of them is a serialized entry into the
// library, so every one belongs here.
//
// Each of them refreshes github.com/adrg/xdg from the environment, and
// that library rewrites its package-level globals on every refresh with
// no lock of its own. Without the package's own serialization, two
// goroutines resolving simultaneously trip the race detector — one
// writing the globals, the other reading them to build a path. Run this
// under -race; it passes trivially without it.
//
// The assertions also pin the outcome the race threatens: a path built
// from a half-updated resolve would not carry the pinned XDG_* roots.
func TestConcurrentResolve_NoRace(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, "cfg")
	data := filepath.Join(root, "data")
	cache := filepath.Join(root, "cache")
	state := filepath.Join(root, "state")
	runtime := filepath.Join(root, "runtime")
	bin := filepath.Join(root, "bin")

	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	t.Setenv("XDG_BIN_HOME", bin)

	// RuntimeFile filters its candidate bases through an existence check
	// and falls back to os.TempDir() when the runtime base is missing,
	// so the base has to exist for the pinned root to be the one used.
	require.NoError(t, os.MkdirAll(runtime, 0o750))

	// Directory resolvers: name -> (resolver, expected root).
	dirFns := map[string]struct {
		fn   func(string) (string, error)
		root string
	}{
		"ConfigDir":    {xdg.ConfigDir, cfg},
		"RawConfigDir": {xdg.RawConfigDir, cfg},
		"DataDir":      {xdg.DataDir, data},
		"RawDataDir":   {xdg.RawDataDir, data},
		"CacheDir":     {xdg.CacheDir, cache},
		"RawCacheDir":  {xdg.RawCacheDir, cache},
		"StateDir":     {xdg.StateDir, state},
		"RawStateDir":  {xdg.RawStateDir, state},
		"RuntimeDir":   {xdg.RuntimeDir, runtime},
		"BinHome":      {xdg.BinHome, bin},
	}

	// File resolvers: name -> (resolver, expected root). These create
	// the parent directory of the path they return, so they hold the
	// resolve lock across filesystem I/O.
	fileFns := map[string]struct {
		fn   func(string, string) (string, error)
		root string
	}{
		"ConfigFile":  {xdg.ConfigFile, cfg},
		"DataFile":    {xdg.DataFile, data},
		"CacheFile":   {xdg.CacheFile, cache},
		"StateFile":   {xdg.StateFile, state},
		"RuntimeFile": {xdg.RuntimeFile, runtime},
	}

	// Search resolvers. Each stats its search paths under the same
	// lock. Nothing is planted, so the lookup failure is the expected
	// result — what is under test is the concurrent entry, not the hit.
	searchFns := map[string]func(string, string) (string, error){
		"SearchConfigFile":  xdg.SearchConfigFile,
		"SearchDataFile":    xdg.SearchDataFile,
		"SearchCacheFile":   xdg.SearchCacheFile,
		"SearchStateFile":   xdg.SearchStateFile,
		"SearchRuntimeFile": xdg.SearchRuntimeFile,
	}

	const goroutines = 24
	const iterations = 20

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				for name, c := range dirFns {
					got, err := c.fn("mytool")
					assert.NoError(t, err, "%s", name)
					assert.Equal(t, filepath.Join(c.root, "mytool"), got, "%s", name)
				}
				for name, c := range fileFns {
					got, err := c.fn("mytool", "app.yaml")
					assert.NoError(t, err, "%s", name)
					assert.Equal(t, filepath.Join(c.root, "mytool", "app.yaml"), got, "%s", name)
				}
				for name, fn := range searchFns {
					_, err := fn("mytool", "absent.yaml")
					assert.Error(t, err, "%s", name)
				}

				// Non-path accessors touch the same globals.
				assert.NotEmpty(t, xdg.Home(), "Home")
				assert.NotEmpty(t, xdg.UserDirs().Documents, "UserDirs")
				assert.NotEmpty(t, xdg.FontDirs(), "FontDirs")
				assert.NotEmpty(t, xdg.ApplicationDirs(), "ApplicationDirs")

				doc, err := xdg.UserDir("documents")
				assert.NoError(t, err, "UserDir")
				assert.NotEmpty(t, doc, "UserDir")
			}
		}()
	}
	wg.Wait()

	// Sanity: the pinned environment is still what the resolvers report
	// once the storm is over.
	got, err := xdg.ConfigDir("mytool")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(cfg, "mytool"), got)
}

// TestReloadObservesEnvChange pins the behavior that rules out caching
// the resolved directories: a caller that changes an XDG_* variable must
// see the new value on the very next resolve, with no explicit refresh.
func TestReloadObservesEnvChange(t *testing.T) {
	first := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", first)
	got, err := xdg.ConfigDir("mytool")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(first, "mytool"), got)

	second := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", second)
	got, err = xdg.ConfigDir("mytool")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(second, "mytool"), got,
		"resolvers must observe an XDG_* change without an explicit refresh")
}
