package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/config"
)

// pathsFixture builds a hermetic environment for Paths tests:
// - HOME (and XDG_CONFIG_HOME) point inside t.TempDir()
// - returns the home dir for path assertions
func pathsFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	require.NoError(t, os.MkdirAll(home, 0o755))
	t.Setenv("HOME", home)
	// adrg/xdg honors XDG_CONFIG_HOME directly; pin it to avoid reading
	// the developer's real config dir on macOS where ConfigHome defaults
	// to ~/Library/Application Support.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CONFIG_DIRS", "")
	return home
}

func touch(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("x: 1\n"), 0o644))
}

// findBySource returns entries in chain whose Source matches.
func findBySource(chain []config.ResolvedPath, source string) []config.ResolvedPath {
	var out []config.ResolvedPath
	for _, p := range chain {
		if p.Source == source {
			out = append(out, p)
		}
	}
	return out
}

func TestPaths_CwdInsideProjectWithConfig(t *testing.T) {
	home := pathsFixture(t)
	project := filepath.Join(home, "work", "myproj")
	cfg := filepath.Join(project, ".kit", "config.yaml")
	touch(t, cfg)

	chain := config.Paths(project)

	// First entry should be the cwd-scoped marker that exists.
	require.NotEmpty(t, chain)
	cwdEntries := findBySource(chain, "cwd")
	require.NotEmpty(t, cwdEntries)
	// The .kit/config.yaml entry must exist and be present in the chain.
	var hit *config.ResolvedPath
	for i, e := range cwdEntries {
		if e.Path == cfg {
			hit = &cwdEntries[i]
		}
	}
	require.NotNil(t, hit, "expected .kit/config.yaml under cwd source")
	assert.True(t, hit.Exists)
}

func TestPaths_ProjectWithNoProjectConfig(t *testing.T) {
	home := pathsFixture(t)
	// User config exists; project does not.
	userCfg := filepath.Join(home, ".config", "kit", "config.yaml")
	touch(t, userCfg)

	project := filepath.Join(home, "work", "empty")
	require.NoError(t, os.MkdirAll(project, 0o755))

	chain := config.Paths(project)

	// All cwd entries should be Exists=false (no marker present).
	for _, e := range findBySource(chain, "cwd") {
		assert.False(t, e.Exists, "cwd entry %q should not exist", e.Path)
	}

	// User entry exists.
	user := findBySource(chain, "user")
	require.Len(t, user, 1)
	assert.Equal(t, userCfg, user[0].Path)
	assert.True(t, user[0].Exists)

	// Defaults entry is always present and Exists=true.
	def := findBySource(chain, "default")
	require.Len(t, def, 1)
	assert.True(t, def[0].Exists)
}

func TestPaths_RandomCwdNoProjectNoUser(t *testing.T) {
	pathsFixture(t)

	random := t.TempDir() // outside HOME entirely
	chain := config.Paths(random)

	// User entry should be Exists=false.
	user := findBySource(chain, "user")
	require.Len(t, user, 1)
	assert.False(t, user[0].Exists)

	// System entry should always be present (and almost certainly not exist
	// on the dev machine for the kit tool).
	sys := findBySource(chain, "system")
	require.Len(t, sys, 1)
	assert.Equal(t, filepath.Join("/etc", "kit", "config.yaml"), sys[0].Path)

	// Defaults entry always present.
	def := findBySource(chain, "default")
	require.Len(t, def, 1)
	assert.True(t, def[0].Exists)
}

func TestPaths_UserExistsSystemDoesNot(t *testing.T) {
	home := pathsFixture(t)
	userCfg := filepath.Join(home, ".config", "kit", "config.yaml")
	touch(t, userCfg)

	chain := config.Paths(filepath.Join(home, "anywhere"))

	user := findBySource(chain, "user")
	require.Len(t, user, 1)
	assert.True(t, user[0].Exists)

	sys := findBySource(chain, "system")
	require.Len(t, sys, 1)
	// /etc/kit/config.yaml should not exist in test env.
	assert.False(t, sys[0].Exists)
}

func TestPaths_RelativeCwdResolvesToAbs(t *testing.T) {
	pathsFixture(t)

	// chdir into a tempdir so a relative cwd is meaningful.
	old, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(old) })

	base := t.TempDir()
	require.NoError(t, os.Chdir(base))

	chain := config.Paths(".")
	require.NotEmpty(t, chain)

	// Every entry except the synthetic default and the system entry must
	// be absolute.
	for _, e := range chain {
		if e.Source == "default" {
			continue
		}
		assert.True(t, filepath.IsAbs(e.Path), "expected absolute path for source=%s, got %q", e.Source, e.Path)
	}
}

func TestPaths_DeepWalkUp(t *testing.T) {
	home := pathsFixture(t)
	// Project root sits at home/work/proj; cwd is 4 levels deeper.
	projectRoot := filepath.Join(home, "work", "proj")
	deep := filepath.Join(projectRoot, "a", "b", "c", "d")
	require.NoError(t, os.MkdirAll(deep, 0o755))

	cfg := filepath.Join(projectRoot, ".kit", "config.yaml")
	touch(t, cfg)

	chain := config.Paths(deep)

	// The project-root config must appear in the chain under "project"
	// source and be marked Exists.
	var hit *config.ResolvedPath
	for i := range chain {
		if chain[i].Path == cfg {
			hit = &chain[i]
			break
		}
	}
	require.NotNil(t, hit, "expected project-root .kit/config.yaml in the chain (deep walk)")
	assert.Equal(t, "project", hit.Source)
	assert.True(t, hit.Exists)
}

func TestPaths_DoesNotWalkAboveHome(t *testing.T) {
	home := pathsFixture(t)
	// Place a config above home — the walk must NOT discover it.
	above := filepath.Dir(home) // root tempdir, parent of HOME
	stray := filepath.Join(above, ".kit", "config.yaml")
	touch(t, stray)

	// cwd inside HOME, a few levels deep.
	deep := filepath.Join(home, "x", "y", "z")
	require.NoError(t, os.MkdirAll(deep, 0o755))

	chain := config.Paths(deep)
	for _, e := range chain {
		assert.NotEqual(t, stray, e.Path, "must not walk above $HOME and discover %q", stray)
	}
}

func TestPaths_DefaultEntryIsLast(t *testing.T) {
	pathsFixture(t)
	chain := config.Paths(t.TempDir())
	require.NotEmpty(t, chain)
	last := chain[len(chain)-1]
	assert.Equal(t, "default", last.Source)
	assert.True(t, last.Exists)
}

func TestPaths_Determinism(t *testing.T) {
	pathsFixture(t)
	cwd := t.TempDir()
	a := config.Paths(cwd)
	b := config.Paths(cwd)
	assert.Equal(t, a, b)
}
