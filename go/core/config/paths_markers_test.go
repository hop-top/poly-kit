package config_test

// Tests for PathsForToolWithMarkers and OptionsForToolWithMarkers — the
// variants that let adopters override the hard-coded .kit/ project marker
// chain with their own (e.g. ".ctxt/", ".rsx/", ".c12n/"). The default
// PathsForTool/OptionsForTool keep their existing behavior.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/config"
)

func TestPathsForToolWithMarkers_CustomMarkerFound(t *testing.T) {
	home := pathsFixture(t)
	project := filepath.Join(home, "work", "myproj")
	cfg := filepath.Join(project, ".ctxt", "config.yaml")
	touch(t, cfg)

	// Use a deeper cwd so the marker is found via the walk-up branch
	// rather than the cwd-itself probe.
	deepCwd := filepath.Join(project, "internal", "events")
	require.NoError(t, os.MkdirAll(deepCwd, 0o755))

	chain := config.PathsForToolWithMarkers(deepCwd, "ctxt", []string{
		filepath.Join(".ctxt", "config.yaml"),
		".ctxt.yaml",
		"ctxt.yaml",
	})

	// At least one entry from cwd or project Source should reference our
	// custom marker and be Exists=true.
	var found bool
	for _, p := range chain {
		if (p.Source == "cwd" || p.Source == "project") && p.Path == cfg {
			assert.True(t, p.Exists,
				"custom-marker path %s should report Exists=true", cfg)
			found = true
		}
	}
	assert.True(t, found,
		"chain should contain the custom .ctxt/config.yaml entry; got %+v", chain)
}

func TestPathsForToolWithMarkers_NilFallsBackToDefault(t *testing.T) {
	home := pathsFixture(t)
	project := filepath.Join(home, "work", "myproj")
	kitCfg := filepath.Join(project, ".kit", "config.yaml")
	touch(t, kitCfg)

	// Nil markers -> use the package default markers (.kit/...).
	chain := config.PathsForToolWithMarkers(project, "ctxt", nil)

	var sawKit bool
	for _, p := range chain {
		if p.Path == kitCfg {
			sawKit = true
			break
		}
	}
	assert.True(t, sawKit,
		"nil markers should fall back to default .kit/ chain; got %+v", chain)
}

func TestPathsForToolWithMarkers_RewritesUserAndSystem(t *testing.T) {
	home := pathsFixture(t)
	chain := config.PathsForToolWithMarkers(home, "ctxt", []string{".ctxt.yaml"})

	user := findBySource(chain, "user")
	require.NotEmpty(t, user, "chain should include a user entry")
	assert.Contains(t, user[0].Path, filepath.Join(".config", "ctxt", "config.yaml"),
		"user layer should be parameterized by tool name")

	sys := findBySource(chain, "system")
	require.NotEmpty(t, sys, "chain should include a system entry")
	assert.Equal(t, filepath.Join("/etc", "ctxt", "config.yaml"), sys[0].Path,
		"system layer should be parameterized by tool name")
}

func TestOptionsForToolWithMarkers_PicksCustomProjectFile(t *testing.T) {
	home := pathsFixture(t)
	project := filepath.Join(home, "work", "myproj")
	cfg := filepath.Join(project, ".ctxt.yaml")
	touch(t, cfg)

	opts := config.OptionsForToolWithMarkersFrom(project, "ctxt", []string{".ctxt.yaml"})

	assert.Equal(t, cfg, opts.ProjectConfigPath,
		"ProjectConfigPath should be set to the .ctxt.yaml file in cwd")
	assert.Contains(t, opts.UserConfigPath, filepath.Join(".config", "ctxt", "config.yaml"),
		"UserConfigPath should be parameterized by tool")
	assert.Equal(t, filepath.Join("/etc", "ctxt", "config.yaml"), opts.SystemConfigPath,
		"SystemConfigPath should be parameterized by tool")
}

func TestOptionsForToolWithMarkers_NoCustomFile_ProjectEmpty(t *testing.T) {
	home := pathsFixture(t)
	cwd := filepath.Join(home, "work", "myproj")
	require.NoError(t, os.MkdirAll(cwd, 0o755))

	opts := config.OptionsForToolWithMarkersFrom(cwd, "ctxt", []string{".ctxt.yaml"})

	assert.Empty(t, opts.ProjectConfigPath,
		"ProjectConfigPath should be empty when no custom marker file exists")
}
