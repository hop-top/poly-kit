package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/config"
)

type pathCfg struct {
	Name  string            `yaml:"name"`
	Dir   string            `yaml:"dir"`
	Dirs  []string          `yaml:"dirs"`
	Stash map[string]string `yaml:"stash"`
	Sub   struct {
		Log string `yaml:"log"`
	} `yaml:"sub"`
}

func writeAt(t *testing.T, path, body string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func evalDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return d
}

var pathKeys = []string{"dir", "dirs", "sub.log", "stash.*"}

func TestPathKeys_FileDirForUserAndExtra(t *testing.T) {
	root := evalDir(t)
	user := writeAt(t, filepath.Join(root, "u", "config.yaml"), "dir: data\nname: data\n")
	extra := writeAt(t, filepath.Join(root, "x", "extra.yaml"), "sub:\n  log: ../logs/a.log\n")
	var cfg pathCfg
	require.NoError(t, config.Load(&cfg, config.Options{
		UserConfigPath: user, ExtraConfigPaths: []string{extra}, PathKeys: pathKeys,
	}))
	assert.Equal(t, filepath.Join(root, "u", "data"), cfg.Dir)
	assert.Equal(t, "data", cfg.Name, "non-path key untouched")
	assert.Equal(t, filepath.Join(root, "logs", "a.log"), cfg.Sub.Log)
}

func TestPathKeys_ProjectDotDirResolvesToRoot(t *testing.T) {
	root := evalDir(t)
	proj := writeAt(t, filepath.Join(root, ".tool", "config.yaml"), "dir: ./data\n")
	var cfg pathCfg
	require.NoError(t, config.Load(&cfg, config.Options{ProjectConfigPath: proj, PathKeys: pathKeys}))
	assert.Equal(t, filepath.Join(root, "data"), cfg.Dir)
}

func TestPathKeys_ProjectDotFileResolvesToItsDir(t *testing.T) {
	root := evalDir(t)
	for _, name := range []string{".tool.yaml", "tool.yaml"} {
		proj := writeAt(t, filepath.Join(root, name), "dir: data\n")
		var cfg pathCfg
		require.NoError(t, config.Load(&cfg, config.Options{ProjectConfigPath: proj, PathKeys: pathKeys}))
		assert.Equal(t, filepath.Join(root, "data"), cfg.Dir, name)
	}
}

func TestPathKeys_ConfigYamlOutsideDotDirIsOwnDir(t *testing.T) {
	root := evalDir(t)
	proj := writeAt(t, filepath.Join(root, "conf", "config.yaml"), "dir: data\n")
	var cfg pathCfg
	require.NoError(t, config.Load(&cfg, config.Options{ProjectConfigPath: proj, PathKeys: pathKeys}))
	assert.Equal(t, filepath.Join(root, "conf", "data"), cfg.Dir)
}

func TestPathKeys_ListWildcardAbsoluteTildeEmpty(t *testing.T) {
	root := evalDir(t)
	user := writeAt(t, filepath.Join(root, "config.yaml"),
		"dirs: [a, /abs, '~/h', '']\nstash:\n  one: x\n  two: /y\n")
	var cfg pathCfg
	require.NoError(t, config.Load(&cfg, config.Options{UserConfigPath: user, PathKeys: pathKeys}))
	assert.Equal(t, []string{filepath.Join(root, "a"), "/abs", "~/h", ""}, cfg.Dirs)
	assert.Equal(t, map[string]string{"one": filepath.Join(root, "x"), "two": "/y"}, cfg.Stash)
}

func TestPathKeys_CaseFoldedKey(t *testing.T) {
	root := evalDir(t)
	m := map[string]any{}
	user := writeAt(t, filepath.Join(root, "config.yaml"), "Dir: data\n")
	require.NoError(t, config.Load(&m, config.Options{UserConfigPath: user, PathKeys: []string{"dir"}}))
	assert.Equal(t, filepath.Join(root, "data"), m["Dir"])
}

func TestPathKeys_OverridesAndEnvResolveAgainstCwd(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)
	root := evalDir(t)
	user := writeAt(t, filepath.Join(root, "config.yaml"), "dir: fromfile\n")
	var cfg pathCfg
	require.NoError(t, config.Load(&cfg, config.Options{
		UserConfigPath: user,
		PathKeys:       pathKeys,
		EnvOverride:    func(c any) { c.(*pathCfg).Sub.Log = "env.log" },
		Overrides:      map[string]any{"dir": "flag"},
	}))
	assert.Equal(t, filepath.Join(cwd, "flag"), cfg.Dir)
	assert.Equal(t, filepath.Join(cwd, "env.log"), cfg.Sub.Log)
}

func TestPathKeys_EnvUntouchedKeyKeepsFileValue(t *testing.T) {
	root := evalDir(t)
	user := writeAt(t, filepath.Join(root, "config.yaml"), "dir: fromfile\n")
	var cfg pathCfg
	require.NoError(t, config.Load(&cfg, config.Options{
		UserConfigPath: user, PathKeys: pathKeys,
		EnvOverride: func(c any) { c.(*pathCfg).Name = "n" },
	}))
	assert.Equal(t, filepath.Join(root, "fromfile"), cfg.Dir)
}

func TestPathKeys_NoKeysUnchanged(t *testing.T) {
	root := evalDir(t)
	user := writeAt(t, filepath.Join(root, "config.yaml"), "dir: data\n")
	var cfg pathCfg
	require.NoError(t, config.Load(&cfg, config.Options{UserConfigPath: user}))
	assert.Equal(t, "data", cfg.Dir)
}

func TestPathKeys_ProjectRulesStillApply(t *testing.T) {
	root := evalDir(t)
	proj := writeAt(t, filepath.Join(root, ".tool", "config.yaml"), "dir: data\nbos: x\n")
	var cfg pathCfg
	err := config.Load(&cfg, config.Options{
		ProjectConfigPath: proj, PathKeys: pathKeys,
		Project: &config.ProjectRules{Forbidden: []string{"bos"}},
	})
	require.Error(t, err)
	proj = writeAt(t, filepath.Join(root, ".tool", "config.yaml"), "dir: data\n")
	require.NoError(t, config.Load(&cfg, config.Options{
		ProjectConfigPath: proj, PathKeys: pathKeys,
		Project: &config.ProjectRules{Forbidden: []string{"bos"}},
	}))
	assert.Equal(t, filepath.Join(root, "data"), cfg.Dir)
}

func TestOrigins_AllLayers(t *testing.T) {
	root := evalDir(t)
	sys := writeAt(t, filepath.Join(root, "sys.yaml"), "name: sys\ndir: sys\nsub:\n  log: sys\n")
	user := writeAt(t, filepath.Join(root, "user.yaml"), "dir: user\nsub:\n  log: user\n")
	proj := writeAt(t, filepath.Join(root, "proj.yaml"), "sub:\n  log: proj\n")
	extra := writeAt(t, filepath.Join(root, "extra.yaml"), "dirs: [x]\n")
	var cfg pathCfg
	got, err := config.LoadWithOrigins(&cfg, config.Options{
		Defaults:         &pathCfg{Name: "def", Stash: map[string]string{"k": "v"}},
		SystemConfigPath: sys, UserConfigPath: user, ProjectConfigPath: proj,
		ExtraConfigPaths: []string{extra},
		EnvOverride:      func(c any) { c.(*pathCfg).Name = "env" },
		Overrides:        map[string]any{"dir": "flag"},
	})
	require.NoError(t, err)
	assert.Equal(t, config.KeyOrigin{Key: "name", Layer: config.LayerEnv}, got["name"])
	assert.Equal(t, config.KeyOrigin{Key: "dir", Layer: config.LayerOverride}, got["dir"])
	assert.Equal(t, config.KeyOrigin{Key: "sub.log", Layer: config.LayerProject, File: proj}, got["sub.log"])
	assert.Equal(t, config.KeyOrigin{Key: "dirs", Layer: config.LayerExtra, File: extra}, got["dirs"])
	assert.Equal(t, config.LayerDefault, got["stash.k"].Layer)
	assert.Equal(t, "", got["stash.k"].File)
}

func TestOrigins_SystemAndUserFiles(t *testing.T) {
	root := evalDir(t)
	sys := writeAt(t, filepath.Join(root, "sys.yaml"), "name: sys\ndir: sys\n")
	user := writeAt(t, filepath.Join(root, "user.yaml"), "dir: user\n")
	var cfg pathCfg
	got, err := config.LoadWithOrigins(&cfg, config.Options{SystemConfigPath: sys, UserConfigPath: user}, "name", "dir", "nope")
	require.NoError(t, err)
	assert.Equal(t, config.KeyOrigin{Key: "name", Layer: config.LayerSystem, File: sys}, got["name"])
	assert.Equal(t, config.KeyOrigin{Key: "dir", Layer: config.LayerUser, File: user}, got["dir"])
	assert.NotContains(t, got, "nope")
	assert.Len(t, got, 2)
}

func TestOrigins_MappingKeyReportsHighestBeneath(t *testing.T) {
	root := evalDir(t)
	sys := writeAt(t, filepath.Join(root, "sys.yaml"), "sub:\n  log: sys\n")
	var cfg pathCfg
	got, err := config.LoadWithOrigins(&cfg, config.Options{
		SystemConfigPath: sys, Overrides: map[string]any{"sub": map[string]any{"log": "o"}},
	}, "sub")
	require.NoError(t, err)
	assert.Equal(t, config.LayerOverride, got["sub"].Layer)
}

func TestOrigins_ReplacedLeafDropsStaleDescendants(t *testing.T) {
	root := evalDir(t)
	sys := writeAt(t, filepath.Join(root, "sys.yaml"), "a:\n  b: 1\n  c: 2\n")
	user := writeAt(t, filepath.Join(root, "user.yaml"), "a:\n  b: 3\n")
	m := map[string]any{}
	got, err := config.LoadWithOrigins(&m, config.Options{SystemConfigPath: sys, UserConfigPath: user})
	require.NoError(t, err)
	assert.Equal(t, config.LayerUser, got["a.b"].Layer)
	// Without DeepMergeMaps the user's "a" replaced the system's whole, so
	// a.c is gone from dst and from the report.
	assert.NotContains(t, got, "a.c")
}

func TestOrigins_DeepMergeKeepsSiblingOrigins(t *testing.T) {
	root := evalDir(t)
	sys := writeAt(t, filepath.Join(root, "sys.yaml"), "a:\n  b: 1\n  c: 2\n")
	user := writeAt(t, filepath.Join(root, "user.yaml"), "a:\n  b: 3\n")
	m := map[string]any{}
	got, err := config.LoadWithOrigins(&m, config.Options{
		SystemConfigPath: sys, UserConfigPath: user, DeepMergeMaps: true,
	})
	require.NoError(t, err)
	assert.Equal(t, config.LayerUser, got["a.b"].Layer)
	assert.Equal(t, config.LayerSystem, got["a.c"].Layer)
}

func TestOrigins_KeyNotInDstNotReported(t *testing.T) {
	root := evalDir(t)
	user := writeAt(t, filepath.Join(root, "user.yaml"), "name: n\nunknown: 1\n")
	var cfg pathCfg
	got, err := config.LoadWithOrigins(&cfg, config.Options{UserConfigPath: user})
	require.NoError(t, err)
	assert.Contains(t, got, "name")
	assert.NotContains(t, got, "unknown")
}

func TestOrigins_MissingFileSkippedAndLoadErrorPropagates(t *testing.T) {
	root := evalDir(t)
	var cfg pathCfg
	got, err := config.LoadWithOrigins(&cfg, config.Options{UserConfigPath: filepath.Join(root, "none.yaml")})
	require.NoError(t, err)
	assert.Empty(t, got)
	bad := writeAt(t, filepath.Join(root, "bad.yaml"), "name: [\n")
	_, err = config.LoadWithOrigins(&cfg, config.Options{UserConfigPath: bad})
	require.Error(t, err)
}

func TestOrigins_ProjectFileUnderRules(t *testing.T) {
	root := evalDir(t)
	user := writeAt(t, filepath.Join(root, "user.yaml"), "name: u\n")
	proj := writeAt(t, filepath.Join(root, "proj.yaml"), "name: p\n")
	var cfg pathCfg
	got, err := config.LoadWithOrigins(&cfg, config.Options{
		UserConfigPath: user, ProjectConfigPath: proj,
		Project: &config.ProjectRules{Forbidden: []string{"bos"}},
	}, "name")
	require.NoError(t, err)
	assert.Equal(t, config.KeyOrigin{Key: "name", Layer: config.LayerProject, File: proj}, got["name"])
}

func TestOrigins_SameResultAsLoad(t *testing.T) {
	root := evalDir(t)
	user := writeAt(t, filepath.Join(root, "user.yaml"), "name: u\ndir: d\n")
	opts := config.Options{UserConfigPath: user, PathKeys: pathKeys}
	var a, b pathCfg
	require.NoError(t, config.Load(&a, opts))
	_, err := config.LoadWithOrigins(&b, opts)
	require.NoError(t, err)
	assert.Equal(t, a, b)
}

func TestDeepMergeMaps_NestedMapsMergeAcrossLayers(t *testing.T) {
	root := evalDir(t)
	sys := writeAt(t, filepath.Join(root, "sys.yaml"),
		"profiles:\n  dev:\n    url: a\n    opts: {x: 1, y: 2}\n  prod:\n    url: p\nlist: [1, 2]\n")
	proj := writeAt(t, filepath.Join(root, "proj.yaml"),
		"profiles:\n  dev:\n    opts: {y: 9}\nlist: [3]\n")
	m := map[string]any{}
	require.NoError(t, config.Load(&m, config.Options{
		SystemConfigPath: sys, ProjectConfigPath: proj, DeepMergeMaps: true,
	}))
	assert.Equal(t, map[string]any{
		"profiles": map[string]any{
			"dev": map[string]any{
				"url":  "a",
				"opts": map[string]any{"x": 1, "y": 9},
			},
			"prod": map[string]any{"url": "p"},
		},
		"list": []any{3},
	}, m)
}

func TestDeepMergeMaps_OffReplacesEntriesWhole(t *testing.T) {
	root := evalDir(t)
	sys := writeAt(t, filepath.Join(root, "sys.yaml"), "profiles:\n  dev:\n    url: a\n  prod:\n    url: p\n")
	proj := writeAt(t, filepath.Join(root, "proj.yaml"), "profiles:\n  dev:\n    port: 1\n")
	m := map[string]any{}
	require.NoError(t, config.Load(&m, config.Options{SystemConfigPath: sys, ProjectConfigPath: proj}))
	// Each top-level entry is replaced whole: prod and dev.url are gone.
	assert.Equal(t, map[string]any{
		"profiles": map[string]any{"dev": map[string]any{"port": 1}},
	}, m)
}

func TestDeepMergeMaps_OverridesDefaultsAndProjectRules(t *testing.T) {
	root := evalDir(t)
	proj := writeAt(t, filepath.Join(root, "proj.yaml"), "ai:\n  models:\n    allow: [a, z]\n  extra: 1\n")
	d := map[string]any{"ai": map[string]any{"keep": true}}
	m := map[string]any{}
	require.NoError(t, config.Load(&m, config.Options{
		Defaults: &d, ProjectConfigPath: proj, DeepMergeMaps: true,
		Overrides: map[string]any{"ai": map[string]any{"flag": "f"}},
		Project: &config.ProjectRules{Narrow: []config.NarrowKey{
			{Key: "ai.models.allow", Mode: config.Intersect},
		}},
	}))
	ai := m["ai"].(map[string]any)
	assert.Equal(t, true, ai["keep"])
	assert.Equal(t, "f", ai["flag"])
	assert.Equal(t, 1, ai["extra"])
	assert.Equal(t, []any{"a", "z"}, ai["models"].(map[string]any)["allow"])
}

func TestDeepMergeMaps_StructDstUnaffected(t *testing.T) {
	root := evalDir(t)
	sys := writeAt(t, filepath.Join(root, "sys.yaml"), "stash: {a: 1}\n")
	proj := writeAt(t, filepath.Join(root, "proj.yaml"), "stash: {b: 2}\n")
	var cfg pathCfg
	require.NoError(t, config.Load(&cfg, config.Options{
		SystemConfigPath: sys, ProjectConfigPath: proj, DeepMergeMaps: true,
	}))
	assert.Equal(t, map[string]string{"a": "1", "b": "2"}, cfg.Stash)
}

func TestOrigins_MappingKeyHighestOfSeveralBeneath(t *testing.T) {
	root := evalDir(t)
	sys := writeAt(t, filepath.Join(root, "sys.yaml"), "a:\n  b: 1\n")
	m := map[string]any{}
	got, err := config.LoadWithOrigins(&m, config.Options{
		SystemConfigPath: sys, DeepMergeMaps: true,
		Overrides: map[string]any{"a": map[string]any{"c": 2}},
	}, "a")
	require.NoError(t, err)
	assert.Equal(t, config.LayerOverride, got["a"].Layer)
}
