package kitinit_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	kitinit "hop.top/kit/cmd/kit/init"
)

func TestDetect_Bootstrap_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	mode, version, err := kitinit.Detect(dir, kitinit.ModeUnset)
	require.NoError(t, err)
	assert.Equal(t, kitinit.ModeBootstrap, mode)
	assert.Empty(t, version)
}

func TestDetect_Augment_HasGit(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0o755))
	mode, version, err := kitinit.Detect(dir, kitinit.ModeUnset)
	require.NoError(t, err)
	assert.Equal(t, kitinit.ModeAugment, mode)
	assert.Empty(t, version)
}

func TestDetect_AlreadyKit(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".kit"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".kit", "version"), []byte("1.2.3\n"), 0o644))
	mode, version, err := kitinit.Detect(dir, kitinit.ModeUnset)
	require.NoError(t, err)
	assert.Equal(t, kitinit.ModeAlreadyKit, mode)
	assert.Equal(t, "1.2.3", version)
}

func TestDetect_BareWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := t.TempDir()
	bareDir := filepath.Join(root, "repo.git")
	require.NoError(t, exec.Command("git", "init", "--bare", bareDir).Run())

	worktreeDir := filepath.Join(root, "wt")
	// Create an initial commit on a branch so worktree add has a ref.
	require.NoError(t, exec.Command("git", "-C", bareDir, "config", "user.email", "test@example.com").Run())
	require.NoError(t, exec.Command("git", "-C", bareDir, "config", "user.name", "Test").Run())
	// Use --orphan-style: create a new branch via worktree add -b on an unborn ref.
	out, err := exec.Command("git", "-C", bareDir, "worktree", "add", "-b", "main", worktreeDir).CombinedOutput()
	if err != nil {
		t.Skipf("git worktree add failed: %v: %s", err, out)
	}

	mode, version, err := kitinit.Detect(worktreeDir, kitinit.ModeUnset)
	require.NoError(t, err)
	assert.Equal(t, kitinit.ModeBareWorktree, mode)
	assert.Empty(t, version)
}

func TestDetect_Override(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0o755))
	mode, version, err := kitinit.Detect(dir, kitinit.ModeBootstrap)
	require.NoError(t, err)
	assert.Equal(t, kitinit.ModeBootstrap, mode)
	assert.Empty(t, version)
}

func TestDetectWithName_BootstrapWhenTargetMissing(t *testing.T) {
	// cwd has .git/ (would normally be ModeAugment), but a positional
	// name was given AND cwd/<name> does not exist. Force Bootstrap so
	// we scaffold INTO the new subdir, not over the parent.
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0o755))
	mode, version, err := kitinit.DetectWithName(dir, "newproj", kitinit.ModeUnset)
	require.NoError(t, err)
	assert.Equal(t, kitinit.ModeBootstrap, mode)
	assert.Empty(t, version)
}

func TestDetectWithName_AugmentWhenTargetExists(t *testing.T) {
	// cwd has .git/ AND cwd/<name> already exists. Don't bypass the
	// detected augment mode -- target collision is a legitimate
	// augment scenario (or an error caller surfaces later).
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "existing"), 0o755))
	mode, version, err := kitinit.DetectWithName(dir, "existing", kitinit.ModeUnset)
	require.NoError(t, err)
	assert.Equal(t, kitinit.ModeAugment, mode)
	assert.Empty(t, version)
}

func TestDetectWithName_NoNameFallsThroughToDetect(t *testing.T) {
	// No positional name -> behave exactly like Detect.
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0o755))
	mode, _, err := kitinit.DetectWithName(dir, "", kitinit.ModeUnset)
	require.NoError(t, err)
	assert.Equal(t, kitinit.ModeAugment, mode)
}

func TestDetectWithName_OverrideWins(t *testing.T) {
	// Explicit --mode override beats the name+target heuristic.
	dir := t.TempDir()
	mode, _, err := kitinit.DetectWithName(dir, "newproj", kitinit.ModeAugment)
	require.NoError(t, err)
	assert.Equal(t, kitinit.ModeAugment, mode)
}

func TestDetectWithName_AlreadyKitNotBypassed(t *testing.T) {
	// cwd is already a kit project. The name+target heuristic must
	// NOT silently switch to Bootstrap -- surface ModeAlreadyKit so
	// the caller errors out (don't scaffold into a new subdir of an
	// already-kit project without explicit user intent).
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".kit"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".kit", "version"), []byte("1.0\n"), 0o644))
	mode, version, err := kitinit.DetectWithName(dir, "newproj", kitinit.ModeUnset)
	require.NoError(t, err)
	assert.Equal(t, kitinit.ModeAlreadyKit, mode)
	assert.Equal(t, "1.0", version)
}
