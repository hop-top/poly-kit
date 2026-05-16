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
