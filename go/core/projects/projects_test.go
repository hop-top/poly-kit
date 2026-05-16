package projects_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/projects"
)

// setupXDG isolates each test under its own XDG_CONFIG_HOME so the rux
// projects.yaml never touches the real user config.
func setupXDG(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return dir
}

func TestRoundTrip(t *testing.T) {
	setupXDG(t)

	want := projects.Entry{
		Path:       "/tmp/roundtrip",
		StartupCmd: "zsh",
		Source:     projects.SourceWSM,
	}
	require.NoError(t, projects.Write("ops", want))

	file, err := projects.Read()
	require.NoError(t, err)

	got, ok := file.Projects["ops"]
	require.True(t, ok, "expected entry 'ops' to be present after Write")
	assert.Equal(t, want.Path, got.Path)
	assert.Equal(t, want.StartupCmd, got.StartupCmd)
	assert.Equal(t, want.Source, got.Source)
	assert.Equal(t, projects.SchemaVersion, file.Schema)
}

func TestEmptyRead(t *testing.T) {
	setupXDG(t)

	file, err := projects.Read()
	require.NoError(t, err, "Read on missing file must return nil error")
	assert.Empty(t, file.Projects, "missing file => empty Projects map")
}

func TestMalformedRead(t *testing.T) {
	setupXDG(t)

	path, err := projects.DefaultPath()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte("::: not yaml :::\n  - [unbalanced\n"), 0o644))

	file, err := projects.Read()
	require.Error(t, err)
	assert.True(t, errors.Is(err, projects.ErrMalformed),
		"expected ErrMalformed, got %v", err)
	assert.Empty(t, file.Projects, "malformed read should yield empty File")
}

func TestSchemaVersionForward(t *testing.T) {
	setupXDG(t)

	path, err := projects.DefaultPath()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte("schema: 999\nprojects: {}\n"), 0o644))

	file, err := projects.Read()
	require.Error(t, err)
	assert.True(t, errors.Is(err, projects.ErrSchemaUnsupported),
		"expected ErrSchemaUnsupported, got %v", err)
	assert.Empty(t, file.Projects)
}

func TestDeleteNoop(t *testing.T) {
	setupXDG(t)

	// Delete on a fully missing file/key must not error.
	require.NoError(t, projects.Delete("never-existed"))

	// Seed one entry, then delete a different missing key.
	require.NoError(t, projects.Write("ops", projects.Entry{
		Path:   "/tmp/ops",
		Source: projects.SourceWSM,
	}))
	require.NoError(t, projects.Delete("not-here"))

	file, err := projects.Read()
	require.NoError(t, err)
	_, ok := file.Projects["ops"]
	assert.True(t, ok, "delete-noop must leave existing entries intact")
}

func TestDefaultPath_RespectsXDG(t *testing.T) {
	dir := setupXDG(t)

	got, err := projects.DefaultPath()
	require.NoError(t, err)

	want := filepath.Join(dir, "rux", "projects.yaml")
	assert.Equal(t, want, got)
}

func TestWrite_DefaultsSourceToWSM(t *testing.T) {
	setupXDG(t)

	require.NoError(t, projects.Write("kit", projects.Entry{
		Path: "/tmp/kit",
		// Source intentionally empty.
	}))

	file, err := projects.Read()
	require.NoError(t, err)

	got, ok := file.Projects["kit"]
	require.True(t, ok)
	assert.Equal(t, projects.SourceWSM, got.Source,
		"empty Source on Write must default to SourceWSM")
}

func TestWrite_AddsSchemaVersion(t *testing.T) {
	setupXDG(t)

	require.NoError(t, projects.Write("ops", projects.Entry{
		Path:   "/tmp/ops",
		Source: projects.SourceWSM,
	}))

	path, err := projects.DefaultPath()
	require.NoError(t, err)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	assert.True(t, strings.Contains(string(raw), "schema: 2"),
		"raw YAML must include 'schema: 2'; got:\n%s", raw)
}
