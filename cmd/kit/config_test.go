package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestKitConfig_PathReturnsDefaultsWhenNoConfigFiles exercises
// `kit config path` from a clean tmpdir + tmp HOME: no real config
// files exist, so the highest-precedence Exists=true entry is the
// in-binary defaults sentinel.
func TestKitConfig_PathReturnsDefaultsWhenNoConfigFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("requires building the kit binary")
	}
	bin := buildBinary(t)

	cwd := t.TempDir()
	cmd := exec.Command(bin, "config", "path")
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "stderr=%s", string(out))
	assert.Equal(t, "<defaults>", strings.TrimSpace(string(out)))
}

// TestKitConfig_PathsEmitsChain confirms `paths` returns the
// resolution chain (cwd markers, walk-up, user, system, defaults)
// even when no real files exist.
func TestKitConfig_PathsEmitsChain(t *testing.T) {
	if testing.Short() {
		t.Skip("requires building the kit binary")
	}
	bin := buildBinary(t)

	cwd := t.TempDir()
	cmd := exec.Command(bin, "config", "paths")
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "stderr=%s", string(out))
	body := strings.TrimSpace(string(out))
	assert.NotEmpty(t, body)
	lines := strings.Split(body, "\n")
	assert.Contains(t, lines, "<defaults>")
}

// TestKitConfig_PathsJSONEmitsArray confirms json formatting works
// end-to-end and emits a non-empty JSON array.
func TestKitConfig_PathsJSONEmitsArray(t *testing.T) {
	if testing.Short() {
		t.Skip("requires building the kit binary")
	}
	bin := buildBinary(t)

	cwd := t.TempDir()
	cmd := exec.Command(bin, "config", "paths", "--format", "json")
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "stderr=%s", string(out))
	body := strings.TrimSpace(string(out))
	assert.True(t, strings.HasPrefix(body, "["), "want json array, got %q", body)
	assert.True(t, strings.HasSuffix(body, "]"), "want json array, got %q", body)
	assert.Contains(t, body, `"source": "default"`)
}

// TestKitConfig_HelpListsSubcommands ensures help output lists the
// path/paths subcommands, so users discover them via --help.
func TestKitConfig_HelpListsSubcommands(t *testing.T) {
	if testing.Short() {
		t.Skip("requires building the kit binary")
	}
	bin := buildBinary(t)

	cmd := exec.Command(bin, "config", "--help")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "stderr=%s", string(out))
	body := string(out)
	assert.Contains(t, body, "path")
	assert.Contains(t, body, "paths")
}
