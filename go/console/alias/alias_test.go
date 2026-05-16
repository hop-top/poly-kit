package alias_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/alias"
)

func TestLoad_FromYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "aliases.yaml")
	require.NoError(t, os.WriteFile(path, []byte("d: deploy\nrs: router start\n"), 0644))

	s := alias.NewStore(path)
	require.NoError(t, s.Load())

	v, ok := s.Get("d")
	assert.True(t, ok)
	assert.Equal(t, "deploy", v)

	v, ok = s.Get("rs")
	assert.True(t, ok)
	assert.Equal(t, "router start", v)
}

func TestLoad_MissingFile(t *testing.T) {
	s := alias.NewStore("/nonexistent/aliases.yaml")
	require.NoError(t, s.Load()) // missing = empty, not error
	assert.Empty(t, s.All())
}

func TestSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "aliases.yaml")

	s := alias.NewStore(path)
	require.NoError(t, s.Set("g", "greet"))
	require.NoError(t, s.Save())

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "g:")
	assert.Contains(t, string(data), "greet")
}

func TestSave_CreatesDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "deep", "aliases.yaml")

	s := alias.NewStore(path)
	require.NoError(t, s.Set("x", "xray"))
	require.NoError(t, s.Save())

	_, err := os.Stat(path)
	require.NoError(t, err)
}

func TestSet_New(t *testing.T) {
	s := alias.NewStore("")
	require.NoError(t, s.Set("d", "deploy"))

	v, ok := s.Get("d")
	assert.True(t, ok)
	assert.Equal(t, "deploy", v)
}

func TestSet_Overwrite(t *testing.T) {
	s := alias.NewStore("")
	require.NoError(t, s.Set("d", "deploy"))
	require.NoError(t, s.Set("d", "destroy"))

	v, _ := s.Get("d")
	assert.Equal(t, "destroy", v)
}

func TestSet_InvalidName(t *testing.T) {
	s := alias.NewStore("")

	assert.Error(t, s.Set("", "deploy"))
	assert.Error(t, s.Set("a b", "deploy"))
	assert.Error(t, s.Set("a\tb", "deploy"))
}

func TestSet_EmptyTarget(t *testing.T) {
	s := alias.NewStore("")
	assert.Error(t, s.Set("d", ""))
}

func TestRemove(t *testing.T) {
	s := alias.NewStore("")
	require.NoError(t, s.Set("d", "deploy"))
	require.NoError(t, s.Remove("d"))

	_, ok := s.Get("d")
	assert.False(t, ok)
}

func TestRemove_NotFound(t *testing.T) {
	s := alias.NewStore("")
	err := s.Remove("nonexistent")
	assert.Error(t, err)
}

func TestGet_NotFound(t *testing.T) {
	s := alias.NewStore("")
	_, ok := s.Get("nope")
	assert.False(t, ok)
}

func TestAll_ReturnsCopy(t *testing.T) {
	s := alias.NewStore("")
	require.NoError(t, s.Set("a", "alpha"))
	require.NoError(t, s.Set("b", "beta"))

	all := s.All()
	assert.Len(t, all, 2)
	assert.Equal(t, "alpha", all["a"])
	assert.Equal(t, "beta", all["b"])

	// mutating returned map must not affect store
	all["c"] = "gamma"
	assert.Len(t, s.All(), 2)
}

func TestExpand_AliasMatch(t *testing.T) {
	s := alias.NewStore("")
	require.NoError(t, s.Set("rs", "router start"))

	got := s.Expand([]string{"rs", "--flag"})
	assert.Equal(t, []string{"router", "start", "--flag"}, got)
}

func TestExpand_NoMatch(t *testing.T) {
	s := alias.NewStore("")
	require.NoError(t, s.Set("rs", "router start"))

	got := s.Expand([]string{"deploy", "--flag"})
	assert.Equal(t, []string{"deploy", "--flag"}, got)
}

func TestExpand_EmptyArgs(t *testing.T) {
	s := alias.NewStore("")
	got := s.Expand(nil)
	assert.Nil(t, got)
}

func TestExpand_WithFlags(t *testing.T) {
	s := alias.NewStore("")
	require.NoError(t, s.Set("dp", "deploy --env prod --dry-run"))

	got := s.Expand([]string{"dp", "starman"})
	assert.Equal(t, []string{"deploy", "--env", "prod", "--dry-run", "starman"}, got)
}

func TestExpand_UserOverridesAliasFlag(t *testing.T) {
	s := alias.NewStore("")
	require.NoError(t, s.Set("dp", "deploy --env prod"))

	got := s.Expand([]string{"dp", "starman", "--env", "staging"})
	// alias expands; user override appended — last wins at parse time
	assert.Equal(t, []string{"deploy", "--env", "prod", "starman", "--env", "staging"}, got)
}

func TestExpand_PreservesExtraArgs(t *testing.T) {
	s := alias.NewStore("")
	require.NoError(t, s.Set("ml", "mission list"))

	got := s.Expand([]string{"ml", "--format", "json"})
	assert.Equal(t, []string{"mission", "list", "--format", "json"}, got)
}

func TestExpand_AliasWithBoolFlag(t *testing.T) {
	s := alias.NewStore("")
	require.NoError(t, s.Set("dp", "deploy --dry-run"))

	got := s.Expand([]string{"dp", "starman", "--no-dry-run"})
	assert.Equal(t, []string{"deploy", "--dry-run", "starman", "--no-dry-run"}, got)
}

func TestExpand_AliasNoFlags_UserAddsFlags(t *testing.T) {
	s := alias.NewStore("")
	require.NoError(t, s.Set("d", "deploy"))

	got := s.Expand([]string{"d", "starman", "--env", "prod", "--dry-run"})
	assert.Equal(t, []string{"deploy", "starman", "--env", "prod", "--dry-run"}, got)
}

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "aliases.yaml")

	s1 := alias.NewStore(path)
	require.NoError(t, s1.Set("d", "deploy"))
	require.NoError(t, s1.Set("rs", "router start"))
	require.NoError(t, s1.Save())

	s2 := alias.NewStore(path)
	require.NoError(t, s2.Load())
	assert.Equal(t, s1.All(), s2.All())
}
