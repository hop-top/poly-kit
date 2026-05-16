package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func optsForPath(path string, scope Scope) Options {
	switch scope {
	case ScopeUser:
		return Options{UserConfigPath: path}
	case ScopeProject:
		return Options{ProjectConfigPath: path}
	default:
		return Options{SystemConfigPath: path}
	}
}

func TestSet_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	opts := optsForPath(path, ScopeProject)

	require.NoError(t, Set("name", "alice", ScopeProject, opts))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "name: alice")
}

func TestSet_UpdateExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("name: alice\nport: \"8080\"\n"), 0o644))
	opts := optsForPath(path, ScopeProject)

	require.NoError(t, Set("debug", "true", ScopeProject, opts))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "name: alice")
	assert.Contains(t, content, "port:")
	assert.Contains(t, content, "debug: \"true\"")
}

func TestSet_DeepKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	opts := optsForPath(path, ScopeProject)

	require.NoError(t, Set("a.b.c", "deep", ScopeProject, opts))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "a:")
	assert.Contains(t, content, "b:")
	assert.Contains(t, content, "c: deep")
}

func TestSet_OverwriteValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("name: alice\n"), 0o644))
	opts := optsForPath(path, ScopeProject)

	require.NoError(t, Set("name", "bob", ScopeProject, opts))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "name: bob")
	assert.NotContains(t, string(data), "alice")
}

func TestSet_ScopeUser(t *testing.T) {
	userDir := t.TempDir()
	projDir := t.TempDir()
	userPath := filepath.Join(userDir, "config.yaml")
	projPath := filepath.Join(projDir, "config.yaml")

	opts := Options{
		UserConfigPath:    userPath,
		ProjectConfigPath: projPath,
	}

	require.NoError(t, Set("key", "val", ScopeUser, opts))

	// User file should exist with the value.
	data, err := os.ReadFile(userPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "key: val")

	// Project file should NOT exist.
	_, err = os.Stat(projPath)
	assert.True(t, os.IsNotExist(err))
}

func TestSet_EmptyScope(t *testing.T) {
	err := Set("key", "val", ScopeUser, Options{})
	assert.ErrorIs(t, err, ErrEmptyScope)
}

func TestSet_CreatesDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "config.yaml")
	opts := optsForPath(path, ScopeProject)

	require.NoError(t, Set("k", "v", ScopeProject, opts))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "k: v")
}

func TestSet_CommentPreservation_Inline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := "name: alice # the user name\n"
	require.NoError(t, os.WriteFile(path, []byte(original), 0o644))
	opts := optsForPath(path, ScopeProject)

	require.NoError(t, Set("port", "9090", ScopeProject, opts))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "the user name")
	assert.Contains(t, content, "port:")
}

func TestSet_CommentPreservation_Block(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := "# block comment about name\nname: alice\n"
	require.NoError(t, os.WriteFile(path, []byte(original), 0o644))
	opts := optsForPath(path, ScopeProject)

	require.NoError(t, Set("name", "bob", ScopeProject, opts))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "block comment about name")
	assert.Contains(t, content, "name: bob")
}

func TestSet_CommentPreservation_Between(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := strings.Join([]string{
		"# first section",
		"alpha: one",
		"# second section",
		"beta: two",
		"",
	}, "\n")
	require.NoError(t, os.WriteFile(path, []byte(original), 0o644))
	opts := optsForPath(path, ScopeProject)

	require.NoError(t, Set("gamma", "three", ScopeProject, opts))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "first section")
	assert.Contains(t, content, "second section")
	assert.Contains(t, content, "alpha: one")
	assert.Contains(t, content, "beta: two")
	assert.Contains(t, content, "gamma: three")
}

func TestSet_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(""), 0o644))
	opts := optsForPath(path, ScopeProject)

	require.NoError(t, Set("key", "val", ScopeProject, opts))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "key: val")
}

func TestSet_WhitespaceOnlyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("  \n\t\n  "), 0o644))
	opts := optsForPath(path, ScopeProject)

	require.NoError(t, Set("key", "val", ScopeProject, opts))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "key: val")
}
