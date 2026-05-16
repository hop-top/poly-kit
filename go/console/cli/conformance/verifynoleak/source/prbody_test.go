package source_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/conformance/verifynoleak/source"
)

// ── ParseOriginRepo ───────────────────────────────────────────────

func TestParseOriginRepo_SSH(t *testing.T) {
	owner, repo, err := source.ParseOriginRepo("git@github.com:hop-top/kit.git")
	require.NoError(t, err)
	assert.Equal(t, "hop-top", owner)
	assert.Equal(t, "kit", repo)
}

func TestParseOriginRepo_SSHWithoutDotGit(t *testing.T) {
	owner, repo, err := source.ParseOriginRepo("git@github.com:hop-top/kit")
	require.NoError(t, err)
	assert.Equal(t, "hop-top", owner)
	assert.Equal(t, "kit", repo)
}

func TestParseOriginRepo_HTTPS(t *testing.T) {
	owner, repo, err := source.ParseOriginRepo("https://github.com/hop-top/kit.git")
	require.NoError(t, err)
	assert.Equal(t, "hop-top", owner)
	assert.Equal(t, "kit", repo)
}

func TestParseOriginRepo_HTTPSWithoutDotGit(t *testing.T) {
	owner, repo, err := source.ParseOriginRepo("https://github.com/hop-top/kit")
	require.NoError(t, err)
	assert.Equal(t, "hop-top", owner)
	assert.Equal(t, "kit", repo)
}

func TestParseOriginRepo_SSHScheme(t *testing.T) {
	owner, repo, err := source.ParseOriginRepo("ssh://git@github.com/hop-top/kit.git")
	require.NoError(t, err)
	assert.Equal(t, "hop-top", owner)
	assert.Equal(t, "kit", repo)
}

func TestParseOriginRepo_GitLabIsUnsupported(t *testing.T) {
	_, _, err := source.ParseOriginRepo("git@gitlab.com:hop-top/kit.git")
	assert.ErrorIs(t, err, source.ErrUnsupportedOriginURL)
}

func TestParseOriginRepo_EmptyIsNoOrigin(t *testing.T) {
	_, _, err := source.ParseOriginRepo("")
	assert.ErrorIs(t, err, source.ErrNoOriginRemote)
}

func TestParseOriginRepo_Whitespace(t *testing.T) {
	// git config output frequently has a trailing newline.
	owner, repo, err := source.ParseOriginRepo("  git@github.com:hop-top/kit.git\n")
	require.NoError(t, err)
	assert.Equal(t, "hop-top", owner)
	assert.Equal(t, "kit", repo)
}

func TestParseOriginRepo_MissingRepo(t *testing.T) {
	_, _, err := source.ParseOriginRepo("git@github.com:hop-top")
	assert.ErrorIs(t, err, source.ErrUnsupportedOriginURL)
}

// ── PRBodyPathLabel ───────────────────────────────────────────────

func TestPRBodyPathLabel(t *testing.T) {
	assert.Equal(t, "pr:42:body", source.PRBodyPathLabel(42))
	assert.Equal(t, "pr:1:body", source.PRBodyPathLabel(1))
}

// ── PRBody validation ─────────────────────────────────────────────

func TestPRBody_NonPositiveN(t *testing.T) {
	_, err := source.PRBody(t.TempDir(), 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "positive PR number")
}

func TestPRBody_NoOriginRemote(t *testing.T) {
	// Fresh tempdir is not a git repo; runGit will return
	// ErrNotAGitRepo, which PRBody surfaces verbatim.
	dir := t.TempDir()
	_, err := source.PRBody(dir, 1)
	require.Error(t, err)
}

// TestPRBody_StubbedGH exercises the happy path by inserting a stub
// `gh` shell script into PATH ahead of the real one. The stub echoes
// a known body. Skipped on Windows where the shebang trick is finicky.
func TestPRBody_StubbedGH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH stub uses POSIX shebang")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	// Set up a temp git repo with a github origin.
	repoDir := t.TempDir()
	gitInit := exec.Command("git", "init", "-q", "-b", "main", repoDir)
	require.NoError(t, gitInit.Run())
	gitRemote := exec.Command("git", "-C", repoDir, "remote", "add", "origin", "git@github.com:hop-top/kit.git")
	require.NoError(t, gitRemote.Run())

	// Stub gh on PATH. The stub captures argv and emits a fixed body.
	binDir := t.TempDir()
	stub := `#!/usr/bin/env bash
# stub gh — emit a fixed PR body
echo "## summary"
echo ""
echo "stub body for PR ${@: -1}"
`
	stubPath := filepath.Join(binDir, "gh")
	require.NoError(t, os.WriteFile(stubPath, []byte(stub), 0o755))

	// Prepend our bindir so our stub wins.
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+origPath)

	body, err := source.PRBody(repoDir, 7)
	require.NoError(t, err)
	got := string(body)
	assert.Contains(t, got, "## summary")
	assert.Contains(t, got, "stub body for PR")
}

// TestPRBody_StubbedGHFailure verifies that a non-zero gh exit is
// surfaced as a wrapped error containing stderr context.
func TestPRBody_StubbedGHFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH stub uses POSIX shebang")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repoDir := t.TempDir()
	require.NoError(t, exec.Command("git", "init", "-q", "-b", "main", repoDir).Run())
	require.NoError(t, exec.Command("git", "-C", repoDir, "remote", "add", "origin", "https://github.com/hop-top/kit.git").Run())

	binDir := t.TempDir()
	stub := `#!/usr/bin/env bash
echo "gh: HTTP 401: Bad credentials" 1>&2
exit 1
`
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "gh"), []byte(stub), 0o755))
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err := source.PRBody(repoDir, 99)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Bad credentials")
}

// TestPRBody_GHNotOnPATH verifies the ErrGHNotFound sentinel. We
// replace PATH with a directory that has no `gh` binary.
func TestPRBody_GHNotOnPATH(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repoDir := t.TempDir()
	require.NoError(t, exec.Command("git", "init", "-q", "-b", "main", repoDir).Run())
	require.NoError(t, exec.Command("git", "-C", repoDir, "remote", "add", "origin", "git@github.com:hop-top/kit.git").Run())

	// Bin dir holds git only — symlink the real git so runGit still
	// works, but no gh.
	binDir := t.TempDir()
	gitPath, err := exec.LookPath("git")
	require.NoError(t, err)
	require.NoError(t, os.Symlink(gitPath, filepath.Join(binDir, "git")))

	t.Setenv("PATH", binDir)

	_, err = source.PRBody(repoDir, 1)
	require.Error(t, err)
	assert.True(t, errors.Is(err, source.ErrGHNotFound) || strings.Contains(err.Error(), "gh"),
		"want ErrGHNotFound or message about gh, got: %v", err)
}
