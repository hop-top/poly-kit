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

// newBareRepoWithWorktree builds a bare repo with one commit (so
// `worktree add -b` has a ref to branch from — an empty bare repo has
// no HEAD and `worktree add -b` fails outright on it) and one linked
// worktree off that commit. Returns the bare repo root and the
// worktree dir.
func newBareRepoWithWorktree(t *testing.T) (bareDir, worktreeDir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := t.TempDir()

	// Seed via a throwaway non-bare clone: commit there, then clone
	// --bare from it. This is the portable way to get a bare repo
	// with history — pushing into a bare repo's checked-out branch
	// from itself is not.
	seedDir := filepath.Join(root, "seed")
	run := func(dir string, args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoErrorf(t, err, "git %v: %s", args, out)
	}
	require.NoError(t, os.MkdirAll(seedDir, 0o755))
	run(seedDir, "init", "-q")
	run(seedDir, "config", "user.email", "test@example.com")
	run(seedDir, "config", "user.name", "Test")
	require.NoError(t, os.WriteFile(filepath.Join(seedDir, "f"), []byte("x"), 0o644))
	run(seedDir, "add", "f")
	run(seedDir, "commit", "-q", "-m", "init")

	bareDir = filepath.Join(root, "repo.git")
	out, err := exec.Command("git", "clone", "--bare", "-q", seedDir, bareDir).CombinedOutput()
	require.NoErrorf(t, err, "clone --bare: %s", out)

	worktreeDir = filepath.Join(root, "wt")
	out, err = exec.Command("git", "-C", bareDir, "worktree", "add", "-b", "feature", worktreeDir).CombinedOutput()
	require.NoErrorf(t, err, "worktree add: %s", out)
	return bareDir, worktreeDir
}

// A LINKED worktree of a bare repo is a normal, usable checkout — git
// add/commit/push all work from it — so Detect resolves it through the
// regular chain to ModeAugment. See Detect's step-1 comment: "A LINKED
// WORKTREE ... is a perfectly usable checkout ... so it flows through
// the normal chain." Verified against git 2.39 and 2.55: --git-dir !=
// --git-common-dir (so isBareWorktree is true) AND
// --is-inside-work-tree is true, so the ModeBareWorktree guard
// (isBareWorktree && !isInsideWorkTree) does not fire here.
func TestDetect_LinkedWorktreeOfBareRepo(t *testing.T) {
	_, worktreeDir := newBareRepoWithWorktree(t)

	mode, version, err := kitinit.Detect(worktreeDir, kitinit.ModeUnset)
	require.NoError(t, err)
	assert.Equal(t, kitinit.ModeAugment, mode)
	assert.Empty(t, version)
}

// The bare repo ROOT — its git internals, no working tree — is the
// case ModeBareWorktree actually names: scaffolding next to
// HEAD/objects/refs is never right, so Detect refuses it.
func TestDetect_BareRepoRoot(t *testing.T) {
	bareDir, _ := newBareRepoWithWorktree(t)

	mode, version, err := kitinit.Detect(bareDir, kitinit.ModeUnset)
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
