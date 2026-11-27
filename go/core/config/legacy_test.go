package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/config"
)

type legacyCfg struct {
	Name   string         `yaml:"name"`
	Port   int            `yaml:"port"`
	Ratio  float64        `yaml:"ratio"`
	Flag   bool           `yaml:"flag"`
	Nested map[string]any `yaml:"nested"`
	Dir    string         `yaml:"dir"`
	Ptr    *string        `yaml:"ptr"`
}

func writeFile(t *testing.T, path, content string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}

// legacyEnv returns canonical and legacy paths under one temp dir.
func legacyEnv(t *testing.T) (canonical, legacyJSON, legacyYAML string) {
	t.Helper()
	root := t.TempDir()
	return filepath.Join(root, "new", "config.yaml"),
		filepath.Join(root, "old", "config.json"),
		filepath.Join(root, "old", "config.yaml")
}

func TestLegacy_JSONFileFillsUserLayer(t *testing.T) {
	canonical, legacyJSON, _ := legacyEnv(t)
	writeFile(t, legacyJSON, "{\n\t\"name\": \"app\",\n\t\"port\": 8080,\n"+
		"\t\"ratio\": 0.5,\n\t\"flag\": true,\n\t\"ptr\": null,\n"+
		"\t\"nested\": {\"a\": {\"b\": [1, 2]}}\n}\n")

	var cfg legacyCfg
	err := config.Load(&cfg, config.Options{
		UserConfigPath:        canonical,
		LegacyUserConfigPaths: []string{legacyJSON},
	})
	require.NoError(t, err)
	assert.Equal(t, "app", cfg.Name)
	assert.Equal(t, 8080, cfg.Port)
	assert.Equal(t, 0.5, cfg.Ratio)
	assert.True(t, cfg.Flag)
	assert.Nil(t, cfg.Ptr)
	assert.Equal(t, map[string]any{"a": map[string]any{"b": []any{1, 2}}}, cfg.Nested)
}

func TestLegacy_IgnoredWhenCanonicalExists(t *testing.T) {
	canonical, legacyJSON, _ := legacyEnv(t)
	writeFile(t, canonical, "name: new\n")
	// A broken legacy file must not even be read.
	writeFile(t, legacyJSON, "{not: [valid")

	called := false
	var cfg legacyCfg
	err := config.Load(&cfg, config.Options{
		UserConfigPath:        canonical,
		LegacyUserConfigPaths: []string{legacyJSON},
		OnLegacyConfig:        func(string, string) { called = true },
	})
	require.NoError(t, err)
	assert.Equal(t, "new", cfg.Name)
	assert.Zero(t, cfg.Port)
	assert.False(t, called)
}

func TestLegacy_FirstExistingWinsInOrder(t *testing.T) {
	canonical, legacyJSON, legacyYAML := legacyEnv(t)
	missing := filepath.Join(filepath.Dir(legacyJSON), "missing.json")
	writeFile(t, legacyJSON, `{"name": "first"}`)
	writeFile(t, legacyYAML, "name: second\nport: 9\n")

	var cfg legacyCfg
	require.NoError(t, config.Load(&cfg, config.Options{
		UserConfigPath:        canonical,
		LegacyUserConfigPaths: []string{"", missing, legacyJSON, legacyYAML},
	}))
	// First existing file only; no merge with later candidates.
	assert.Equal(t, "first", cfg.Name)
	assert.Zero(t, cfg.Port)
}

func TestLegacy_NoLegacyPathsKeepsBehaviour(t *testing.T) {
	canonical, legacyJSON, _ := legacyEnv(t)
	writeFile(t, legacyJSON, `{"name": "old"}`)
	var cfg legacyCfg
	require.NoError(t, config.Load(&cfg, config.Options{UserConfigPath: canonical}))
	assert.Empty(t, cfg.Name)
}

func TestLegacy_EmptyUserPathDisablesLegacy(t *testing.T) {
	_, legacyJSON, _ := legacyEnv(t)
	writeFile(t, legacyJSON, `{"name": "old"}`)
	var cfg legacyCfg
	require.NoError(t, config.Load(&cfg, config.Options{
		LegacyUserConfigPaths: []string{legacyJSON},
	}))
	assert.Empty(t, cfg.Name)
}

func TestLegacy_HookFiresOncePerLoad(t *testing.T) {
	canonical, legacyJSON, _ := legacyEnv(t)
	writeFile(t, legacyJSON, `{"name": "old"}`)
	type call struct{ used, canonical string }
	var calls []call
	opts := config.Options{
		UserConfigPath:        canonical,
		LegacyUserConfigPaths: []string{legacyJSON},
		OnLegacyConfig: func(used, canon string) {
			calls = append(calls, call{used, canon})
		},
		// Origins re-reads the file; the hook must still fire once.
	}
	var cfg legacyCfg
	require.NoError(t, config.Load(&cfg, opts))
	assert.Equal(t, []call{{legacyJSON, canonical}}, calls)

	calls = nil
	_, err := config.LoadWithOrigins(&cfg, opts)
	require.NoError(t, err)
	assert.Len(t, calls, 1)
}

func TestLegacy_HookNotFiredByReadsOrPathListing(t *testing.T) {
	canonical, legacyJSON, _ := legacyEnv(t)
	writeFile(t, legacyJSON, `{"name": "old"}`)
	n := 0
	opts := config.Options{
		UserConfigPath:        canonical,
		LegacyUserConfigPaths: []string{legacyJSON},
		OnLegacyConfig:        func(string, string) { n++ },
	}
	_, err := config.Get("name", opts)
	require.NoError(t, err)
	_, err = config.List(opts)
	require.NoError(t, err)
	config.PathsForToolWithLegacy(t.TempDir(), "tool", []string{legacyJSON})
	assert.Zero(t, n)
}

func TestLegacy_OriginReportsLegacyFile(t *testing.T) {
	canonical, legacyJSON, _ := legacyEnv(t)
	writeFile(t, legacyJSON, `{"name": "old", "port": 1}`)
	var cfg legacyCfg
	got, err := config.LoadWithOrigins(&cfg, config.Options{
		UserConfigPath:        canonical,
		LegacyUserConfigPaths: []string{legacyJSON},
	}, "name", "port")
	require.NoError(t, err)
	for _, k := range []string{"name", "port"} {
		assert.Equal(t, config.LayerUser, got[k].Layer)
		assert.Equal(t, legacyJSON, got[k].File)
	}
}

func TestLegacy_PathKeysResolveAgainstLegacyDir(t *testing.T) {
	canonical, legacyJSON, _ := legacyEnv(t)
	writeFile(t, legacyJSON, `{"dir": "data"}`)
	var cfg legacyCfg
	require.NoError(t, config.Load(&cfg, config.Options{
		UserConfigPath:        canonical,
		LegacyUserConfigPaths: []string{legacyJSON},
		PathKeys:              []string{"dir"},
	}))
	assert.Equal(t, filepath.Join(filepath.Dir(legacyJSON), "data"), cfg.Dir)
}

func TestLegacy_MigrationsApplyButNeverWriteBack(t *testing.T) {
	canonical, legacyJSON, _ := legacyEnv(t)
	orig := `{"name": "old"}`
	writeFile(t, legacyJSON, orig)
	migrated := 0
	opts := config.Options{
		UserConfigPath:        canonical,
		LegacyUserConfigPaths: []string{legacyJSON},
		Migrations: []config.Migration{{
			From: 0, To: 1,
			Apply: func(m map[string]any) (map[string]any, error) {
				m["port"] = 7
				return m, nil
			},
		}},
		MigrateOptions: config.MigrateOptions{
			WriteBack:   true,
			OnMigration: func(string, config.Migration) { migrated++ },
		},
	}
	var cfg legacyCfg
	require.NoError(t, config.Load(&cfg, opts))
	assert.Equal(t, 7, cfg.Port)
	assert.Positive(t, migrated)
	assert.Equal(t, orig, readFile(t, legacyJSON))
	assert.NoFileExists(t, canonical)
}

func TestLegacy_BrokenFileErrorsNamingFile(t *testing.T) {
	canonical, legacyJSON, legacyYAML := legacyEnv(t)
	writeFile(t, legacyJSON, "{not: [valid")
	writeFile(t, legacyYAML, "name: fallthrough\n")
	var cfg legacyCfg
	err := config.Load(&cfg, config.Options{
		UserConfigPath:        canonical,
		LegacyUserConfigPaths: []string{legacyJSON, legacyYAML},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), legacyJSON)
	assert.Empty(t, cfg.Name)
}

func TestLegacy_DirectoryErrorsNamingPath(t *testing.T) {
	canonical, legacyJSON, legacyYAML := legacyEnv(t)
	require.NoError(t, os.MkdirAll(legacyJSON, 0o755))
	writeFile(t, legacyYAML, "name: fallthrough\n")
	var cfg legacyCfg
	err := config.Load(&cfg, config.Options{
		UserConfigPath:        canonical,
		LegacyUserConfigPaths: []string{legacyJSON, legacyYAML},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), legacyJSON)
}

func TestLegacy_UnreadableFileErrorsNamingFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	canonical, legacyJSON, legacyYAML := legacyEnv(t)
	writeFile(t, legacyJSON, `{"name": "x"}`)
	require.NoError(t, os.Chmod(legacyJSON, 0o000))
	t.Cleanup(func() { _ = os.Chmod(legacyJSON, 0o644) })
	writeFile(t, legacyYAML, "name: fallthrough\n")
	var cfg legacyCfg
	err := config.Load(&cfg, config.Options{
		UserConfigPath:        canonical,
		LegacyUserConfigPaths: []string{legacyJSON, legacyYAML},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), legacyJSON)
}

func TestLegacy_TildeExpandsToHome(t *testing.T) {
	home := pathsFixture(t)
	canonical, _, _ := legacyEnv(t)
	writeFile(t, filepath.Join(home, ".config", "tool", "config.json"), `{"name": "tilde"}`)
	var cfg legacyCfg
	require.NoError(t, config.Load(&cfg, config.Options{
		UserConfigPath:        canonical,
		LegacyUserConfigPaths: []string{"~/.config/tool/config.json"},
	}))
	assert.Equal(t, "tilde", cfg.Name)
}

func TestLegacy_RelativeEntryUsedAsWritten(t *testing.T) {
	canonical, _, _ := legacyEnv(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "rel.json"), `{"name": "rel"}`)
	t.Chdir(dir)
	var cfg legacyCfg
	require.NoError(t, config.Load(&cfg, config.Options{
		UserConfigPath:        canonical,
		LegacyUserConfigPaths: []string{"rel.json"},
	}))
	assert.Equal(t, "rel", cfg.Name)
}

func TestLegacy_GetAndListFollowLegacy(t *testing.T) {
	canonical, legacyJSON, _ := legacyEnv(t)
	writeFile(t, legacyJSON, `{"name": "old", "nested": {"k": "v"}}`)
	opts := config.Options{
		UserConfigPath:        canonical,
		LegacyUserConfigPaths: []string{legacyJSON},
	}
	v, err := config.Get("nested.k", opts)
	require.NoError(t, err)
	assert.Equal(t, "v", v)

	entries, err := config.List(opts)
	require.NoError(t, err)
	var keys []string
	for _, e := range entries {
		assert.Equal(t, config.ScopeUser, e.Scope)
		keys = append(keys, e.Key)
	}
	assert.Equal(t, []string{"name", "nested.k"}, keys)
}

func TestLegacy_GetIgnoresLegacyWhenCanonicalExists(t *testing.T) {
	canonical, legacyJSON, _ := legacyEnv(t)
	writeFile(t, canonical, "name: new\n")
	writeFile(t, legacyJSON, `{"name": "old", "port": 1}`)
	opts := config.Options{
		UserConfigPath:        canonical,
		LegacyUserConfigPaths: []string{legacyJSON},
	}
	_, err := config.Get("port", opts)
	assert.ErrorIs(t, err, config.ErrKeyNotFound)
}

func TestLegacy_SetSeedsCanonicalAndLeavesLegacyAlone(t *testing.T) {
	canonical, legacyJSON, _ := legacyEnv(t)
	orig := `{"name": "old", "port": 1}`
	writeFile(t, legacyJSON, orig)
	opts := config.Options{
		UserConfigPath:        canonical,
		LegacyUserConfigPaths: []string{legacyJSON},
	}
	require.NoError(t, config.Set("name", "new", config.ScopeUser, opts))

	assert.Equal(t, orig, readFile(t, legacyJSON))
	var cfg legacyCfg
	require.NoError(t, config.Load(&cfg, opts))
	assert.Equal(t, "new", cfg.Name)
	assert.Equal(t, 1, cfg.Port, "legacy content seeded into canonical")
	assert.NotContains(t, readFile(t, canonical), "{", "seeded file is block-style yaml")

	// Canonical now exists: later legacy edits are ignored.
	writeFile(t, legacyJSON, `{"port": 99}`)
	require.NoError(t, config.Load(&cfg, opts))
	assert.Equal(t, 1, cfg.Port)
}

func TestLegacy_SetValueSeedsCanonical(t *testing.T) {
	canonical, legacyJSON, _ := legacyEnv(t)
	orig := `{"name": "old", "ratio": 0.5}`
	writeFile(t, legacyJSON, orig)
	opts := config.Options{
		UserConfigPath:        canonical,
		LegacyUserConfigPaths: []string{legacyJSON},
	}
	require.NoError(t, config.SetValue("port", 42, config.ScopeUser, opts))

	assert.Equal(t, orig, readFile(t, legacyJSON))
	var cfg legacyCfg
	require.NoError(t, config.Load(&cfg, opts))
	assert.Equal(t, 42, cfg.Port)
	assert.Equal(t, 0.5, cfg.Ratio)
	assert.Equal(t, "old", cfg.Name)
}

func TestLegacy_SetOnOtherScopeDoesNotSeedUser(t *testing.T) {
	canonical, legacyJSON, _ := legacyEnv(t)
	writeFile(t, legacyJSON, `{"name": "old"}`)
	proj := filepath.Join(t.TempDir(), "p.yaml")
	opts := config.Options{
		UserConfigPath:        canonical,
		ProjectConfigPath:     proj,
		LegacyUserConfigPaths: []string{legacyJSON},
	}
	require.NoError(t, config.Set("port", "1", config.ScopeProject, opts))
	assert.NoFileExists(t, canonical)
}

func TestLegacy_UnsetSeedsSoKeyIsGone(t *testing.T) {
	canonical, legacyJSON, _ := legacyEnv(t)
	orig := `{"name": "old", "port": 1}`
	writeFile(t, legacyJSON, orig)
	opts := config.Options{
		UserConfigPath:        canonical,
		LegacyUserConfigPaths: []string{legacyJSON},
	}
	require.NoError(t, config.Unset("name", config.ScopeUser, opts))

	assert.Equal(t, orig, readFile(t, legacyJSON))
	_, err := config.Get("name", opts)
	assert.ErrorIs(t, err, config.ErrKeyNotFound)
	v, err := config.Get("port", opts)
	require.NoError(t, err)
	assert.Equal(t, 1, v)
	var cfg legacyCfg
	require.NoError(t, config.Load(&cfg, opts))
	assert.Empty(t, cfg.Name)
}

func TestLegacy_UnsetMissingKeyCreatesNothing(t *testing.T) {
	canonical, legacyJSON, _ := legacyEnv(t)
	writeFile(t, legacyJSON, `{"name": "old"}`)
	opts := config.Options{
		UserConfigPath:        canonical,
		LegacyUserConfigPaths: []string{legacyJSON},
	}
	err := config.Unset("absent", config.ScopeUser, opts)
	assert.ErrorIs(t, err, config.ErrKeyNotFound)
	assert.NoFileExists(t, canonical)
}

func TestLegacy_SetWithBrokenLegacyErrorsAndWritesNothing(t *testing.T) {
	canonical, legacyJSON, _ := legacyEnv(t)
	writeFile(t, legacyJSON, "{not: [valid")
	opts := config.Options{
		UserConfigPath:        canonical,
		LegacyUserConfigPaths: []string{legacyJSON},
	}
	err := config.Set("name", "x", config.ScopeUser, opts)
	require.Error(t, err)
	assert.NoFileExists(t, canonical)
}

func TestLegacy_PathsChainListsLegacyAfterUser(t *testing.T) {
	home := pathsFixture(t)
	legacy := filepath.Join(home, ".config", "tool", "config.json")
	missing := filepath.Join(home, ".config", "tool", "other.json")
	touch(t, legacy)

	chain := config.PathsForToolWithLegacy(home, "tool", []string{legacy, "", missing})
	var sources []string
	for _, p := range chain {
		sources = append(sources, p.Source)
	}
	ui := -1
	for i, s := range sources {
		if s == "user" {
			ui = i
		}
	}
	require.GreaterOrEqual(t, ui, 0)
	require.Equal(t, "user-legacy", sources[ui+1])
	require.Equal(t, "user-legacy", sources[ui+2])
	assert.Equal(t, "system", sources[ui+3])
	assert.Equal(t, legacy, chain[ui+1].Path)
	assert.True(t, chain[ui+1].Exists)
	assert.Equal(t, missing, chain[ui+2].Path)
	assert.False(t, chain[ui+2].Exists)
	assert.Len(t, findBySource(chain, "user-legacy"), 2)
}

func TestLegacy_PathsChainUnchangedWithoutLegacy(t *testing.T) {
	home := pathsFixture(t)
	assert.Equal(t,
		config.PathsForTool(home, "tool"),
		config.PathsForToolWithLegacy(home, "tool", nil))
}

func TestLegacy_WithLegacyUserPathsComposesWithAnyChain(t *testing.T) {
	home := pathsFixture(t)
	base := config.PathsForToolWithMarkers(home, "tool", []string{".tool.yaml"})
	chain := config.WithLegacyUserPaths(base, []string{"/nope/config.json"})
	require.Len(t, chain, len(base)+1)
	require.Len(t, findBySource(chain, "user-legacy"), 1)
}

func TestLegacy_DotConfigPathHelper(t *testing.T) {
	home := pathsFixture(t)
	assert.Equal(t,
		filepath.Join(home, ".config", "tool", "config.json"),
		config.LegacyDotConfigPath("tool", "config.json"))
}

func TestLegacy_ReloadSourcePathsReportFileInUse(t *testing.T) {
	canonical, legacyJSON, _ := legacyEnv(t)
	writeFile(t, legacyJSON, `{"name": "old"}`)
	opts := config.Options{
		UserConfigPath:        canonical,
		LegacyUserConfigPaths: []string{legacyJSON},
	}
	var cfg legacyCfg
	require.NoError(t, config.Load(&cfg, opts))
	pub := &recordingPublisher{}
	r := config.New(&cfg, opts, config.WithReloadPublisher(pub))
	writeFile(t, legacyJSON, `{"name": "newer"}`)
	require.Error(t, r.Reload(opts), "name is immutable: veto proves the legacy file is re-read")

	events := waitForEvent(t, pub, string(config.DefaultReloadTopics.ReloadFailed), 1)
	failed := events[0].Payload.(config.ReloadFailedPayload)
	assert.Equal(t, []string{"", legacyJSON, ""}, failed.SourcePaths)
}
