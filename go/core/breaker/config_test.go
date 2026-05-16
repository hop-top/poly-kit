package breaker_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/breaker"
)

// withConfigHome points XDG_CONFIG_HOME at a fresh temp dir.
func withConfigHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return dir
}

// writeBreakerConfig writes breaker.yaml under <dir>/<tool>/.
func writeBreakerConfig(t *testing.T, dir, tool, content string) string {
	t.Helper()
	toolDir := filepath.Join(dir, tool)
	require.NoError(t, os.MkdirAll(toolDir, 0o755))
	path := filepath.Join(toolDir, "breaker.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func TestApply_AllOptions(t *testing.T) {
	t.Cleanup(func() { breaker.Unregister("apply-all") })

	cfg := map[string]any{
		"on_trip":        "halt",
		"max_per_minute": 100,
		"max_bytes":      int64(1024),
		"max_ops":        50,
		"reset_after":    "5s",
	}
	b, err := breaker.Apply("apply-all", cfg)
	require.NoError(t, err)
	require.NotNil(t, b)
	assert.Equal(t, "apply-all", b.Name())
}

func TestApply_RejectsUnknownKeys(t *testing.T) {
	cfg := map[string]any{"bogus_key": 1}
	_, err := breaker.Apply("apply-bad", cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bogus_key")
}

func TestApply_RejectsBadActionString(t *testing.T) {
	cfg := map[string]any{"on_trip": "explode"}
	_, err := breaker.Apply("apply-bad-action", cfg)
	require.Error(t, err)
}

func TestApply_AcceptsActionAliases(t *testing.T) {
	t.Cleanup(func() { breaker.Unregister("apply-degrade") })
	t.Cleanup(func() { breaker.Unregister("apply-warn") })

	for _, action := range []string{"degrade", "warn"} {
		_, err := breaker.Apply("apply-"+action, map[string]any{"on_trip": action})
		require.NoError(t, err, "action %q", action)
	}
}

func TestApply_TimeoutAndConcurrent(t *testing.T) {
	t.Cleanup(func() { breaker.Unregister("apply-conc") })

	cfg := map[string]any{
		"timeout":        "30s",
		"max_concurrent": 4,
	}
	b, err := breaker.Apply("apply-conc", cfg)
	require.NoError(t, err)
	require.NotNil(t, b)
}

func TestApply_CircuitNested(t *testing.T) {
	t.Cleanup(func() { breaker.Unregister("apply-circuit") })

	cfg := map[string]any{
		"circuit": map[string]any{
			"failure_threshold": 5,
			"success_threshold": 2,
			"delay":             "30s",
		},
	}
	b, err := breaker.Apply("apply-circuit", cfg)
	require.NoError(t, err)
	require.NotNil(t, b)
}

func TestFromConfig_MissingFileReturnsEmpty(t *testing.T) {
	withConfigHome(t)
	m, err := breaker.FromConfig("nonexistent-tool-zzz")
	require.NoError(t, err)
	assert.Empty(t, m)
}

func TestFromConfig_ReadsYAMLFile(t *testing.T) {
	dir := withConfigHome(t)
	writeBreakerConfig(t, dir, "mytool", `breakers:
  file-writes:
    on_trip: halt
    max_per_minute: 100
    max_bytes: 1024
  exec-spawns:
    on_trip: warn
    max_per_minute: 30
`)
	t.Cleanup(func() {
		breaker.Unregister("file-writes")
		breaker.Unregister("exec-spawns")
	})

	m, err := breaker.FromConfig("mytool")
	require.NoError(t, err)
	assert.Len(t, m, 2)
	assert.Contains(t, m, "file-writes")
	assert.Contains(t, m, "exec-spawns")
}

func TestFromConfig_RejectsBadYAML(t *testing.T) {
	dir := withConfigHome(t)
	writeBreakerConfig(t, dir, "badyaml", "breakers:\n  bad-one:\n    bogus_key: 1\n")
	_, err := breaker.FromConfig("badyaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bogus_key")
}
