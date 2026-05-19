package conformance_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/conformance"
)

// runInstall invokes "install-hooks" through the conformance command
// tree with the provided args, returning combined out+err and any
// exec error.
func runInstall(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := conformance.Cmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(append([]string{"install-hooks"}, args...))
	err := cmd.Execute()
	return buf.String(), err
}

// initRepo runs `git init` in dir so installer's git operations
// succeed against a throwaway repo.
func initRepo(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init", "--quiet", dir)
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git init failed: %s", out)
}

// scrubGitEnv unsets the GIT_* env vars that git inherits from a
// parent process (notably from a pre-push hook). Without this, every
// `git` subprocess we spawn in a tempdir would resolve back to the
// outer repo via GIT_DIR / GIT_WORK_TREE. Empty-string values aren't
// safe — git rejects an empty GIT_DIR — so we Unsetenv and restore on
// cleanup.
func scrubGitEnv(t *testing.T) {
	t.Helper()
	for _, v := range []string{
		"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE",
		"GIT_NAMESPACE", "GIT_COMMON_DIR", "GIT_PREFIX",
		"GIT_OBJECT_DIRECTORY",
	} {
		key := v
		orig, had := os.LookupEnv(key)
		os.Unsetenv(key)
		if had {
			t.Cleanup(func() { os.Setenv(key, orig) })
		}
	}
}

func TestInstallHooks_FreshInstall(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	out, err := runInstall(t, "--root", dir)
	require.NoError(t, err, "fresh install should succeed: %s", out)

	// Shim files created with correct content + markers.
	pc, readErr := os.ReadFile(filepath.Join(dir, ".githooks", "pre-commit"))
	require.NoError(t, readErr)
	assert.Contains(t, string(pc), "VERIFY_NO_LEAK_SHIM_V1")
	assert.Contains(t, string(pc), "verify-no-leak --staged")

	cm, readErr := os.ReadFile(filepath.Join(dir, ".githooks", "commit-msg"))
	require.NoError(t, readErr)
	assert.Contains(t, string(cm), "VERIFY_NO_LEAK_MSG_SHIM_V1")
	assert.Contains(t, string(cm), "--commit-msg-file=")

	// core.hooksPath was set.
	cfg, cfgErr := exec.Command("git", "-C", dir, "config", "--get", "core.hooksPath").Output()
	require.NoError(t, cfgErr)
	assert.Equal(t, ".githooks", strings.TrimSpace(string(cfg)))

	// Shims are executable.
	st, _ := os.Stat(filepath.Join(dir, ".githooks", "pre-commit"))
	assert.NotZero(t, st.Mode()&0o100, "pre-commit should be executable")
}

func TestInstallHooks_Idempotent(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	_, err := runInstall(t, "--root", dir)
	require.NoError(t, err)

	out, err := runInstall(t, "--root", dir)
	require.NoError(t, err, "re-run should succeed: %s", out)

	// Second run should report "identical" + "already set".
	assert.Contains(t, out, "identical to managed shim")
	assert.Contains(t, out, "already set to .githooks")
}

func TestInstallHooks_DryRunDoesNotWrite(t *testing.T) {
	scrubGitEnv(t)
	dir := t.TempDir()
	initRepo(t, dir)

	out, err := runInstall(t, "--root", dir, "--dry-run")
	require.NoError(t, err, "dry-run should succeed: %s", out)

	// No .githooks directory should have been created.
	_, statErr := os.Stat(filepath.Join(dir, ".githooks"))
	assert.True(t, os.IsNotExist(statErr), "dry-run must not create .githooks")

	// core.hooksPath should NOT have been set.
	cfg, _ := exec.Command("git", "-C", dir, "config", "--get", "core.hooksPath").Output()
	assert.Empty(t, strings.TrimSpace(string(cfg)), "dry-run must not set core.hooksPath")

	// Output advertises dry-run mode.
	assert.Contains(t, out, "[dry-run]")
}

func TestInstallHooks_RefusesNonKitHookWithoutForce(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	hooks := filepath.Join(dir, ".githooks")
	require.NoError(t, os.MkdirAll(hooks, 0o755))
	custom := "#!/bin/sh\necho hello\n"
	require.NoError(t, os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte(custom), 0o755))

	out, err := runInstall(t, "--root", dir)
	require.Error(t, err, "should refuse without --force")
	assert.True(t, errors.Is(err, conformance.ErrUsage), "clobber refusal must map to ErrUsage")
	assert.Contains(t, out, "--- existing", "diff should appear in human output")

	// Custom hook left untouched.
	body, _ := os.ReadFile(filepath.Join(hooks, "pre-commit"))
	assert.Equal(t, custom, string(body))
}

func TestInstallHooks_ForceOverridesNonKitHook(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	hooks := filepath.Join(dir, ".githooks")
	require.NoError(t, os.MkdirAll(hooks, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte("#!/bin/sh\necho hi\n"), 0o755))

	_, err := runInstall(t, "--root", dir, "--force")
	require.NoError(t, err, "force should overwrite")

	body, _ := os.ReadFile(filepath.Join(hooks, "pre-commit"))
	assert.Contains(t, string(body), "VERIFY_NO_LEAK_SHIM_V1")
}

func TestInstallHooks_RefreshesMarkedShimWithDifferentBody(t *testing.T) {
	// An older marker-bearing shim must be refreshed without --force.
	dir := t.TempDir()
	initRepo(t, dir)
	hooks := filepath.Join(dir, ".githooks")
	require.NoError(t, os.MkdirAll(hooks, 0o755))
	stale := "#!/bin/sh\n# Marker: VERIFY_NO_LEAK_SHIM_V1\necho stale\n"
	require.NoError(t, os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte(stale), 0o755))

	_, err := runInstall(t, "--root", dir)
	require.NoError(t, err)

	body, _ := os.ReadFile(filepath.Join(hooks, "pre-commit"))
	assert.Contains(t, string(body), "verify-no-leak --staged --format=human")
	assert.NotContains(t, string(body), "echo stale")
}

func TestInstallHooks_JSONOutput(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	out, err := runInstall(t, "--root", dir, "--format", "json")
	require.NoError(t, err)

	var report struct {
		Tool    string `json:"tool"`
		Root    string `json:"root"`
		DryRun  bool   `json:"dry_run"`
		Actions []struct {
			Kind   string `json:"kind"`
			Path   string `json:"path"`
			Reason string `json:"reason"`
		} `json:"actions"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &report))
	assert.Equal(t, "install-hooks", report.Tool)
	assert.Equal(t, dir, report.Root)
	assert.False(t, report.DryRun)
	require.NotEmpty(t, report.Actions)

	// We expect at minimum: mkdir, two writes (pre-commit, commit-msg),
	// one git config.
	kinds := map[string]int{}
	for _, a := range report.Actions {
		kinds[a.Kind]++
	}
	assert.GreaterOrEqual(t, kinds["write"], 2, "expected two shim writes")
	assert.GreaterOrEqual(t, kinds["config"]+kinds["config-skip"], 1, "expected core.hooksPath action")
}

func TestInstallHooks_RefusesLegacyHookWithoutForce(t *testing.T) {
	// .git/hooks/pre-commit lacking marker should trigger refusal.
	dir := t.TempDir()
	initRepo(t, dir)

	legacy := filepath.Join(dir, ".git", "hooks")
	require.NoError(t, os.MkdirAll(legacy, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(legacy, "pre-commit"), []byte("#!/bin/sh\necho legacy\n"), 0o755))

	_, err := runInstall(t, "--root", dir)
	require.Error(t, err)
	assert.True(t, errors.Is(err, conformance.ErrUsage))
}

func TestInstallHooks_RejectsUnknownFormat(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	_, err := runInstall(t, "--root", dir, "--format", "xml")
	require.Error(t, err)
	assert.True(t, errors.Is(err, conformance.ErrUsage))
}
