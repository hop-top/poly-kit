package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/config"
)

func TestResolveString_FlagWins(t *testing.T) {
	t.Setenv("MYTOOL_OUT_DIR", "from-env")

	flag := config.FlagLayer{
		Flags:   map[string]string{"out_dir": "from-flag"},
		Changed: map[string]bool{"out_dir": true},
	}
	env := config.EnvLayer{Prefix: "MYTOOL"}
	def := config.DefaultLayer{Defaults: map[string]string{"out_dir": "default"}}

	got := config.ResolveString("out_dir", flag, env, def)
	assert.Equal(t, "from-flag", got.Value)
	assert.Equal(t, config.OriginFlag, got.Origin)
	assert.Equal(t, "--out_dir", got.Detail)
}

func TestResolveString_EnvWinsWhenNoFlag(t *testing.T) {
	t.Setenv("MYTOOL_OUT_DIR", "from-env")

	flag := config.FlagLayer{Flags: map[string]string{}, Changed: map[string]bool{}}
	env := config.EnvLayer{Prefix: "MYTOOL"}
	def := config.DefaultLayer{Defaults: map[string]string{"out_dir": "default"}}

	got := config.ResolveString("out_dir", flag, env, def)
	assert.Equal(t, "from-env", got.Value)
	assert.Equal(t, config.OriginEnv, got.Origin)
	assert.Equal(t, "MYTOOL_OUT_DIR", got.Detail)
}

func TestResolveString_ProjectWinsOverGlobal(t *testing.T) {
	dir := t.TempDir()
	gp := filepath.Join(dir, "global.yaml")
	pp := filepath.Join(dir, "project.yaml")
	require.NoError(t, os.WriteFile(gp, []byte("out_dir: g\nlog_level: info\n"), 0o644))
	require.NoError(t, os.WriteFile(pp, []byte("out_dir: p\n"), 0o644))

	gl, err := config.LoadYAMLLayer(gp, config.OriginGlobal)
	require.NoError(t, err)
	pl, err := config.LoadYAMLLayer(pp, config.OriginProject)
	require.NoError(t, err)

	// Ladder: project before global.
	got := config.ResolveString("out_dir", pl, gl)
	assert.Equal(t, "p", got.Value)
	assert.Equal(t, config.OriginProject, got.Origin)
	assert.Equal(t, pp, got.Detail)

	// log_level only in global.
	got = config.ResolveString("log_level", pl, gl)
	assert.Equal(t, "info", got.Value)
	assert.Equal(t, config.OriginGlobal, got.Origin)
}

func TestResolveString_DefaultWhenMissingEverywhere(t *testing.T) {
	flag := config.FlagLayer{Flags: map[string]string{}}
	env := config.EnvLayer{Prefix: "NOPE"}
	def := config.DefaultLayer{Defaults: map[string]string{"k": "fallback"}}

	got := config.ResolveString("k", flag, env, def)
	assert.Equal(t, "fallback", got.Value)
	assert.Equal(t, config.OriginDefault, got.Origin)
	assert.Equal(t, "built-in", got.Detail)
}

func TestResolveString_NoMatchReturnsEmptyDefault(t *testing.T) {
	got := config.ResolveString("missing", config.DefaultLayer{Defaults: map[string]string{}})
	assert.Equal(t, "", got.Value)
	assert.Equal(t, config.OriginDefault, got.Origin)
}

func TestFlagLayer_RespectsChanged(t *testing.T) {
	// flag in map, but Changed says it wasn't user-set → skip.
	flag := config.FlagLayer{
		Flags:   map[string]string{"k": "from-flag"},
		Changed: map[string]bool{"k": false},
	}
	def := config.DefaultLayer{Defaults: map[string]string{"k": "default"}}

	got := config.ResolveString("k", flag, def)
	assert.Equal(t, "default", got.Value)
	assert.Equal(t, config.OriginDefault, got.Origin)
}

func TestFlagLayer_NilChangedConsidersAll(t *testing.T) {
	flag := config.FlagLayer{Flags: map[string]string{"k": "v"}}
	got := config.ResolveString("k", flag)
	assert.Equal(t, "v", got.Value)
	assert.Equal(t, config.OriginFlag, got.Origin)
}

func TestEnvLayer_NormalizesKey(t *testing.T) {
	t.Setenv("APP_LOG_LEVEL", "debug")
	env := config.EnvLayer{Prefix: "APP"}

	// Both dotted and dashed keys map to the same env var.
	got := config.ResolveString("log.level", env)
	assert.Equal(t, "debug", got.Value)

	got = config.ResolveString("log-level", env)
	assert.Equal(t, "debug", got.Value)
}

func TestYAMLLayer_LoadEmptyPath(t *testing.T) {
	l, err := config.LoadYAMLLayer("", config.OriginGlobal)
	require.NoError(t, err)
	_, ok := l.Lookup("anything")
	assert.False(t, ok)
}

func TestYAMLLayer_LoadMissing(t *testing.T) {
	l, err := config.LoadYAMLLayer(filepath.Join(t.TempDir(), "absent.yaml"), config.OriginProject)
	require.NoError(t, err)
	_, ok := l.Lookup("k")
	assert.False(t, ok)
}

func TestYAMLLayer_BoolAndIntCoercion(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.yaml")
	require.NoError(t, os.WriteFile(p, []byte("debug: true\nport: 8080\n"), 0o644))

	l, err := config.LoadYAMLLayer(p, config.OriginGlobal)
	require.NoError(t, err)

	v, ok := l.Lookup("debug")
	require.True(t, ok)
	assert.Equal(t, "true", v)

	v, ok = l.Lookup("port")
	require.True(t, ok)
	assert.Equal(t, "8080", v)
}

func TestResolveString_FullLadder(t *testing.T) {
	// flag → env → project → global → default
	t.Setenv("APP_K", "from-env")
	dir := t.TempDir()
	gp := filepath.Join(dir, "global.yaml")
	pp := filepath.Join(dir, "project.yaml")
	require.NoError(t, os.WriteFile(gp, []byte("k: from-global\n"), 0o644))
	require.NoError(t, os.WriteFile(pp, []byte("k: from-project\n"), 0o644))
	gl, _ := config.LoadYAMLLayer(gp, config.OriginGlobal)
	pl, _ := config.LoadYAMLLayer(pp, config.OriginProject)

	flag := config.FlagLayer{
		Flags:   map[string]string{"k": "from-flag"},
		Changed: map[string]bool{"k": true},
	}
	env := config.EnvLayer{Prefix: "APP"}
	def := config.DefaultLayer{Defaults: map[string]string{"k": "from-default"}}

	// flag set → flag.
	got := config.ResolveString("k", flag, env, pl, gl, def)
	assert.Equal(t, "from-flag", got.Value)

	// no flag → env.
	flag.Changed["k"] = false
	got = config.ResolveString("k", flag, env, pl, gl, def)
	assert.Equal(t, "from-env", got.Value)

	// no env → project.
	t.Setenv("APP_K", "")
	os.Unsetenv("APP_K")
	got = config.ResolveString("k", flag, env, pl, gl, def)
	assert.Equal(t, "from-project", got.Value)

	// no project → global.
	got = config.ResolveString("k", flag, env, gl, def)
	assert.Equal(t, "from-global", got.Value)

	// nothing → default.
	got = config.ResolveString("k", flag, env, def)
	assert.Equal(t, "from-default", got.Value)
}
