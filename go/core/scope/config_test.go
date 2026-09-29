package scope_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/scope"
)

// withConfigHome points XDG_CONFIG_HOME at a fresh temp dir and returns its path.
func withConfigHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return dir
}

func writeConfig(t *testing.T, dir, tool, content string) string {
	t.Helper()
	toolDir := filepath.Join(dir, tool)
	require.NoError(t, os.MkdirAll(toolDir, 0o755))
	path := filepath.Join(toolDir, "scope.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func TestFromConfig_MissingFileReturnsEmpty(t *testing.T) {
	withConfigHome(t)
	p, err := scope.FromConfig("nonexistent-tool")
	require.NoError(t, err)
	assert.Equal(t, scope.Strict, p.Mode())
	assert.Empty(t, p.Rules())
}

func TestFromConfig_ParsesModeAndRules(t *testing.T) {
	dir := withConfigHome(t)
	writeConfig(t, dir, "mytool", `mode: warn
allow:
  - "~/Documents/**"
  - "~/Downloads/**"
deny:
  - "~/Documents/Private/**"
`)
	p, err := scope.FromConfig("mytool")
	require.NoError(t, err)
	assert.Equal(t, scope.Warn, p.Mode())

	rules := p.Rules()
	require.Len(t, rules, 2)
	assert.True(t, rules[0].Allow)
	assert.False(t, rules[1].Allow)
	assert.Len(t, rules[0].Patterns, 2)
}

func TestFromConfig_MacroExpansion(t *testing.T) {
	dir := withConfigHome(t)
	writeConfig(t, dir, "mytool", `mode: strict
allow:
  - "tool:config"
  - "tool:data"
  - "tool:cache"
  - "tool:state"
  - "tool:runtime"
  - "tool:bin"
`)
	p, err := scope.FromConfig("mytool")
	require.NoError(t, err)
	rules := p.Rules()
	require.Len(t, rules, 1)
	// Each macro yields ≥1 pattern; expect at least 6.
	assert.GreaterOrEqual(t, len(rules[0].Patterns), 6)
}

func TestFromConfig_UnknownMacroErrors(t *testing.T) {
	dir := withConfigHome(t)
	writeConfig(t, dir, "mytool", `mode: strict
allow:
  - "tool:bogus"
`)
	_, err := scope.FromConfig("mytool")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tool macro")
}

func TestFromConfig_UnknownModeErrors(t *testing.T) {
	dir := withConfigHome(t)
	writeConfig(t, dir, "mytool", `mode: chaotic
`)
	_, err := scope.FromConfig("mytool")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown mode")
}

func TestFromConfig_BadYAMLErrors(t *testing.T) {
	dir := withConfigHome(t)
	writeConfig(t, dir, "mytool", `mode: [oops`)
	_, err := scope.FromConfig("mytool")
	require.Error(t, err)
}

func TestFromConfig_MacroRespectsToolName(t *testing.T) {
	dir := withConfigHome(t)
	writeConfig(t, dir, "alpha", `mode: strict
allow:
  - "tool:data"
`)
	p, err := scope.FromConfig("alpha")
	require.NoError(t, err)
	rules := p.Rules()
	require.Len(t, rules, 1)
	require.Len(t, rules[0].Patterns, 1)
	// pattern is XDG_DATA_HOME / "alpha" / "**" — but XDG_DATA_HOME is unset
	// so it falls back to OS default; just assert "alpha" appears in it.
	assert.Contains(t, string(rules[0].Patterns[0]), "alpha")
}

func TestMustFromConfig_PanicsOnParseError(t *testing.T) {
	dir := withConfigHome(t)
	writeConfig(t, dir, "mytool", `mode: chaotic`)
	assert.Panics(t, func() { scope.MustFromConfig("mytool") })
}

func TestMustFromConfig_OkOnMissingFile(t *testing.T) {
	withConfigHome(t)
	assert.NotPanics(t, func() { scope.MustFromConfig("missing-tool") })
}

// check is a small helper: Check(path, op) must not error; returns the decision.
func check(t *testing.T, p *scope.Policy, path string, op scope.Op) scope.Decision {
	t.Helper()
	dec, err := p.Check(scope.Path(path), op)
	require.NoError(t, err)
	return dec
}

func TestFromConfig_PerOpRules(t *testing.T) {
	dir := withConfigHome(t)
	writeConfig(t, dir, "mytool", `mode: strict
allow:
  - path: "/srv/proj/**"
    ops: [read]
deny:
  - path: "/srv/proj/**"
    ops: [write, exec]
`)
	p, err := scope.FromConfig("mytool")
	require.NoError(t, err)

	assert.Equal(t, scope.Allowed, check(t, p, "/srv/proj/a.txt", scope.Read))
	assert.Equal(t, scope.Denied, check(t, p, "/srv/proj/a.txt", scope.Write))
	assert.Equal(t, scope.Denied, check(t, p, "/srv/proj/a.txt", scope.Exec))

	rules := p.Rules()
	require.Len(t, rules, 2)
	assert.True(t, rules[0].Allow)
	assert.Equal(t, scope.Read, rules[0].Ops)
	assert.False(t, rules[1].Allow)
	assert.Equal(t, scope.Write|scope.Exec, rules[1].Ops)
}

func TestFromConfig_PerOpAllowLeavesOtherOpsUnknown(t *testing.T) {
	dir := withConfigHome(t)
	writeConfig(t, dir, "mytool", `allow:
  - path: "/srv/proj/**"
    ops: [read]
`)
	p, err := scope.FromConfig("mytool")
	require.NoError(t, err)
	assert.Equal(t, scope.Allowed, check(t, p, "/srv/proj/a", scope.Read))
	assert.Equal(t, scope.Unknown, check(t, p, "/srv/proj/a", scope.Write))
	require.ErrorIs(t, p.Enforce("/srv/proj/a", scope.Write), scope.ErrDenied)
}

func TestFromConfig_MixedBareAndPerOpEntries(t *testing.T) {
	dir := withConfigHome(t)
	writeConfig(t, dir, "mytool", `allow:
  - "/srv/open/**"
  - path: "/srv/ro/**"
    ops: [read]
  - "/srv/also-open/**"
`)
	p, err := scope.FromConfig("mytool")
	require.NoError(t, err)

	// Bare strings keep meaning "all ops".
	for _, op := range []scope.Op{scope.Read, scope.Write, scope.Exec} {
		assert.Equal(t, scope.Allowed, check(t, p, "/srv/open/x", op))
		assert.Equal(t, scope.Allowed, check(t, p, "/srv/also-open/x", op))
	}
	assert.Equal(t, scope.Allowed, check(t, p, "/srv/ro/x", scope.Read))
	assert.Equal(t, scope.Unknown, check(t, p, "/srv/ro/x", scope.Write))
}

func TestFromConfig_PerOpEntryWithoutOpsCoversAllOps(t *testing.T) {
	dir := withConfigHome(t)
	writeConfig(t, dir, "mytool", `deny:
  - path: "/srv/x/**"
`)
	p, err := scope.FromConfig("mytool")
	require.NoError(t, err)
	rules := p.Rules()
	require.Len(t, rules, 1)
	assert.Equal(t, scope.Read|scope.Write|scope.Exec, rules[0].Ops)
}

func TestFromConfig_PerOpDenyWins(t *testing.T) {
	dir := withConfigHome(t)
	writeConfig(t, dir, "mytool", `allow:
  - "/srv/x/**"
  - path: "/srv/y/**"
    ops: [write]
deny:
  - path: "/srv/x/**"
    ops: [write]
  - "/srv/y/**"
`)
	p, err := scope.FromConfig("mytool")
	require.NoError(t, err)
	// Bare allow + per-op deny: only the denied op flips.
	assert.Equal(t, scope.Allowed, check(t, p, "/srv/x/f", scope.Read))
	assert.Equal(t, scope.Denied, check(t, p, "/srv/x/f", scope.Write))
	// Per-op allow + bare deny: deny still wins.
	assert.Equal(t, scope.Denied, check(t, p, "/srv/y/f", scope.Write))
}

func TestFromConfig_PerOpMacroExpansion(t *testing.T) {
	dir := withConfigHome(t)
	writeConfig(t, dir, "alpha", `allow:
  - path: "tool:data"
    ops: [read, write]
`)
	p, err := scope.FromConfig("alpha")
	require.NoError(t, err)
	rules := p.Rules()
	require.Len(t, rules, 1)
	assert.Equal(t, scope.Read|scope.Write, rules[0].Ops)
	assert.Equal(t, scope.ToolData("alpha"), rules[0].Patterns)
}

func TestFromConfig_PerOpOpsCaseInsensitive(t *testing.T) {
	dir := withConfigHome(t)
	writeConfig(t, dir, "mytool", `allow:
  - path: "/srv/x/**"
    ops: [Read, " EXEC "]
`)
	p, err := scope.FromConfig("mytool")
	require.NoError(t, err)
	require.Len(t, p.Rules(), 1)
	assert.Equal(t, scope.Read|scope.Exec, p.Rules()[0].Ops)
}

func TestFromConfig_PerOpInvalidEntries(t *testing.T) {
	cases := map[string]struct {
		yaml string
		want string
	}{
		"unknown op": {
			yaml: "allow:\n  - path: /x/**\n    ops: [fly]\n",
			want: `unknown op "fly"`,
		},
		"empty ops": {
			yaml: "allow:\n  - path: /x/**\n    ops: []\n",
			want: "ops",
		},
		"missing path": {
			yaml: "deny:\n  - ops: [read]\n",
			want: "path",
		},
		"empty path": {
			yaml: "deny:\n  - path: \"\"\n    ops: [read]\n",
			want: "path",
		},
		// A typo must not silently widen an allow to all ops.
		"unknown key": {
			yaml: "allow:\n  - path: /x/**\n    op: [read]\n",
			want: `unknown key "op"`,
		},
		"sequence entry": {
			yaml: "allow:\n  - [/x/**, read]\n",
			want: "entry",
		},
		"unknown macro": {
			yaml: "allow:\n  - path: tool:bogus\n    ops: [read]\n",
			want: "tool macro",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := withConfigHome(t)
			writeConfig(t, dir, "mytool", tc.yaml)
			_, err := scope.FromConfig("mytool")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}
