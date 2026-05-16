package kitinit_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	kitinit "hop.top/kit/cmd/kit/init"
)

func skipIfNoGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
}

func configureGitIdentity(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, exec.Command("git", "-C", dir, "config", "user.email", "test@example.com").Run())
	require.NoError(t, exec.Command("git", "-C", dir, "config", "user.name", "Test").Run())
}

func TestInit_PlainGit(t *testing.T) {
	skipIfNoGit(t)
	dir := t.TempDir()
	skipped, err := kitinit.Init(context.Background(), dir, false, "main")
	require.NoError(t, err)
	assert.False(t, skipped, "plain git init must not report skipped")
	assert.DirExists(t, filepath.Join(dir, ".git"))
	head, err := os.ReadFile(filepath.Join(dir, ".git", "HEAD"))
	require.NoError(t, err)
	assert.Contains(t, string(head), "refs/heads/main")
}

func TestInit_GitHop(t *testing.T) {
	skipIfNoGit(t)
	// Probe: bare `git hop` prints usage with exit 0 when subcommand exists.
	// `git hop --help` invokes man and may fail even when git-hop is installed.
	if err := exec.Command("git", "hop").Run(); err != nil {
		t.Skip("git-hop subcommand not available")
	}
	dir := t.TempDir()
	skipped, err := kitinit.Init(context.Background(), dir, true, "")
	if err != nil {
		t.Skipf("git hop init not usable in this env: %v", err)
	}
	assert.False(t, skipped, "git-hop on PATH must not report skipped")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.NotEmpty(t, entries, "git hop init should populate dir")
}

// TestInit_GitHopMissing — T-1061: when --hop=true and git-hop is not on
// PATH, Init must return (skipped=true, nil) rather than an error so the
// surrounding flow can proceed best-effort. We simulate "missing git-hop"
// by clearing PATH for the test; the parent `git` invocation is never
// reached because the LookPath check short-circuits first.
func TestInit_GitHopMissing(t *testing.T) {
	t.Setenv("PATH", "")
	dir := t.TempDir()
	skipped, err := kitinit.Init(context.Background(), dir, true, "")
	require.NoError(t, err, "missing git-hop must NOT surface as an error")
	assert.True(t, skipped, "missing git-hop must report skipped=true")
	// No .git created — Init returned before exec.
	_, statErr := os.Stat(filepath.Join(dir, ".git"))
	assert.True(t, os.IsNotExist(statErr), "no .git should be created when git-hop is missing")
}

func TestInitialCommit_Succeeds(t *testing.T) {
	skipIfNoGit(t)
	dir := t.TempDir()
	_, err := kitinit.Init(context.Background(), dir, false, "main")
	require.NoError(t, err)
	configureGitIdentity(t, dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644))
	err = kitinit.InitialCommit(context.Background(), dir, "feat: test")
	require.NoError(t, err)
	out, err := exec.Command("git", "-C", dir, "log", "--oneline").Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), "feat: test")
}

func TestInitialCommit_FailsOnEmpty(t *testing.T) {
	skipIfNoGit(t)
	dir := t.TempDir()
	_, err := kitinit.Init(context.Background(), dir, false, "main")
	require.NoError(t, err)
	configureGitIdentity(t, dir)
	err = kitinit.InitialCommit(context.Background(), dir, "feat: empty")
	require.Error(t, err)
	assert.True(t,
		strings.Contains(err.Error(), "nothing to commit") ||
			strings.Contains(err.Error(), "nothing added"),
		"expected empty-commit error, got: %v", err)
}

func TestPush_NoRemote(t *testing.T) {
	skipIfNoGit(t)
	dir := t.TempDir()
	_, err := kitinit.Init(context.Background(), dir, false, "main")
	require.NoError(t, err)
	err = kitinit.Push(context.Background(), dir)
	require.Error(t, err)
	msg := err.Error()
	assert.True(t,
		strings.Contains(msg, "no upstream") ||
			strings.Contains(msg, "no configured push destination") ||
			strings.Contains(msg, "does not appear to be a git repository") ||
			strings.Contains(msg, "origin") ||
			strings.Contains(msg, "remote"),
		"expected no-remote error, got: %v", err)
}
