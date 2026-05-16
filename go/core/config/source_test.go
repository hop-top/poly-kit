package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/config"
)

func TestRegistry_AddGet(t *testing.T) {
	r := &config.Registry{}
	r.Add(config.Source{Name: "local", Type: "filesystem", Dirs: []string{"/tmp/a"}})
	r.Add(config.Source{Name: "remote", Type: "ctxt", Endpoint: "https://x"})

	got, ok := r.Get("local")
	require.True(t, ok)
	assert.Equal(t, "filesystem", got.Type)
	assert.Equal(t, []string{"/tmp/a"}, got.Dirs)

	_, ok = r.Get("missing")
	assert.False(t, ok)
}

func TestRegistry_AddDuplicateUpserts(t *testing.T) {
	r := &config.Registry{}
	r.Add(config.Source{Name: "x", Type: "filesystem"})
	r.Add(config.Source{Name: "x", Type: "ctxt", Endpoint: "https://y"})

	require.Len(t, r.Sources, 1)
	got, _ := r.Get("x")
	assert.Equal(t, "ctxt", got.Type)
	assert.Equal(t, "https://y", got.Endpoint)
}

func TestRegistry_Remove(t *testing.T) {
	r := &config.Registry{}
	r.Add(config.Source{Name: "a"})
	r.Add(config.Source{Name: "b"})

	assert.True(t, r.Remove("a"))
	assert.False(t, r.Remove("a"))
	assert.Equal(t, []string{"b"}, r.Names())
}

func TestRegistry_NamesPreservesInsertionOrder(t *testing.T) {
	r := &config.Registry{}
	for _, n := range []string{"c", "a", "b"} {
		r.Add(config.Source{Name: n})
	}
	assert.Equal(t, []string{"c", "a", "b"}, r.Names())
}

func TestRegistry_Merge(t *testing.T) {
	base := &config.Registry{}
	base.Add(config.Source{Name: "a", Type: "filesystem"})
	base.Add(config.Source{Name: "b", Type: "filesystem"})

	override := config.Registry{}
	override.Add(config.Source{Name: "b", Type: "ctxt"}) // overrides
	override.Add(config.Source{Name: "c", Type: "ctxt"}) // appends

	base.Merge(override)
	require.Len(t, base.Sources, 3)
	got, _ := base.Get("b")
	assert.Equal(t, "ctxt", got.Type)
	assert.Equal(t, []string{"a", "b", "c"}, base.Names())
}

func TestRegistry_RoundTripYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.yaml")

	r := &config.Registry{}
	r.Add(config.Source{
		Name: "local", Type: "filesystem", Dirs: []string{"/x", "/y"},
	})
	r.Add(config.Source{
		Name: "remote", Type: "ctxt",
		Endpoint: "https://api.example.com", Token: "secret",
	})

	require.NoError(t, config.SaveRegistry(path, r))

	loaded, err := config.LoadRegistry(path)
	require.NoError(t, err)
	require.Len(t, loaded.Sources, 2)
	assert.Equal(t, "local", loaded.Sources[0].Name)
	assert.Equal(t, []string{"/x", "/y"}, loaded.Sources[0].Dirs)
	assert.Equal(t, "remote", loaded.Sources[1].Name)
	assert.Equal(t, "secret", loaded.Sources[1].Token)
}

func TestRegistry_LoadMissingFileReturnsEmpty(t *testing.T) {
	r, err := config.LoadRegistry(filepath.Join(t.TempDir(), "absent.yaml"))
	require.NoError(t, err)
	assert.Empty(t, r.Sources)
}

func TestRegistry_LoadEmptyPathReturnsEmpty(t *testing.T) {
	r, err := config.LoadRegistry("")
	require.NoError(t, err)
	assert.Empty(t, r.Sources)
}

func TestRegistry_LoadInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	require.NoError(t, os.WriteFile(path, []byte(":\n  - oops"), 0o644))

	_, err := config.LoadRegistry(path)
	require.Error(t, err)
}
