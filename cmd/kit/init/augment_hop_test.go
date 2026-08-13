// Package kitinit augment_hop_test.go — regression coverage for the
// bare-worktree (hop-layout) augment path documented in
// docs/adopters/guides/create-cli-project.md §"Augment a hop worktree
// or bare-worktree-shaped repo": auto-detect refuses the tree, an
// explicit ModeAugment override bypasses the refusal, and the render
// lands entirely inside the worktree — never in the bare hub.
//
// White-box (package kitinit) so we can call unexported runAugment,
// reusing the synthetic fixture from augment_test.go.
package kitinit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAugment_BareWorktree_OverrideRendersIntoTree(t *testing.T) {
	bareDir, worktreeDir := hopFixture(t)

	// Precondition: auto-detect refuses this tree.
	mode, _, err := Detect(worktreeDir, ModeUnset)
	require.NoError(t, err)
	require.Equal(t, ModeBareWorktree, mode,
		"bare-worktree cwd must surface as ModeBareWorktree under auto-detect")

	// Documented bypass: an explicit --mode augment override wins,
	// resolving to ModeHopAugment on a bare-worktree cwd so the augment
	// flow applies the hop-specific guards.
	mode, _, err = Detect(worktreeDir, ModeAugment)
	require.NoError(t, err)
	require.Equal(t, ModeHopAugment, mode,
		"explicit ModeAugment override on a bare-worktree cwd must resolve to ModeHopAugment")

	hubBefore := dirNames(t, bareDir)

	tplPath := fixtureTemplate(t)
	deps, _ := fixtureDeps()
	in := baseInputs(tplPath, "demo", 1)
	in.Mode = mode
	sum, err := runAugment(context.Background(), deps, in, worktreeDir)
	require.NoError(t, err, "clean hop worktree must augment successfully")

	// Tier-1 file set rendered into the worktree.
	got := listRel(worktreeDir, sum.Result.Written)
	assert.Equal(t, []string{".gitignore", "Makefile", "lint.yml"}, got,
		"tier-1 set must render into the worktree")

	// Every write landed inside the worktree.
	for _, p := range sum.Result.Written {
		rel, relErr := filepath.Rel(worktreeDir, p)
		require.NoError(t, relErr)
		assert.False(t, strings.HasPrefix(rel, ".."),
			"write escaped the worktree: %s", p)
	}

	// The bare hub must be untouched: no new entries, no template files
	// in the common dir.
	assert.Equal(t, hubBefore, dirNames(t, bareDir),
		"bare hub directory must be untouched by augment")
	for _, f := range []string{"lint.yml", "Makefile", ".gitignore"} {
		_, statErr := os.Stat(filepath.Join(bareDir, f))
		assert.True(t, os.IsNotExist(statErr),
			"template file %s must not appear in the bare hub", f)
	}
}

func TestAugment_HopMode_DirtyRefuses(t *testing.T) {
	_, worktreeDir := hopFixture(t)
	dirtyHopTree(t, worktreeDir)

	tplPath := fixtureTemplate(t)
	deps, _ := fixtureDeps()
	in := baseInputs(tplPath, "demo", 1)
	in.Mode = ModeHopAugment

	_, err := runAugment(context.Background(), deps, in, worktreeDir)
	require.Error(t, err, "dirty hop worktree must refuse augment")

	var hopErr *HopDirtyError
	require.ErrorAs(t, err, &hopErr)
	assert.Equal(t, "main", hopErr.Branch)
	assert.NotEmpty(t, hopErr.DirtyList)
	assert.True(t, IsHopDirty(err))

	// Refusal happens before the engine runs: nothing rendered.
	for _, f := range []string{"lint.yml", "Makefile", ".gitignore"} {
		_, statErr := os.Stat(filepath.Join(worktreeDir, f))
		assert.True(t, os.IsNotExist(statErr),
			"template file %s must not be rendered on refusal", f)
	}
}

func TestAugment_HopMode_DirtyForcedProceeds(t *testing.T) {
	overrides := map[string]func(*Inputs){
		"force": func(in *Inputs) { in.Force = true },
		"yes":   func(in *Inputs) { in.Yes = true },
	}
	for name, set := range overrides {
		t.Run(name, func(t *testing.T) {
			_, worktreeDir := hopFixture(t)
			dirtyHopTree(t, worktreeDir)

			tplPath := fixtureTemplate(t)
			deps, _ := fixtureDeps()
			in := baseInputs(tplPath, "demo", 1)
			in.Mode = ModeHopAugment
			set(&in)

			sum, err := runAugment(context.Background(), deps, in, worktreeDir)
			require.NoError(t, err, "dirty hop worktree + %s must proceed", name)
			assert.Equal(t, "main", sum.HopBranch,
				"summary must carry the augmented hop branch")
			got := listRel(worktreeDir, sum.Result.Written)
			assert.Equal(t, []string{".gitignore", "Makefile", "lint.yml"}, got)
		})
	}
}

func TestAugment_HopMode_CleanProceeds(t *testing.T) {
	_, worktreeDir := hopFixture(t)

	tplPath := fixtureTemplate(t)
	deps, _ := fixtureDeps()
	in := baseInputs(tplPath, "demo", 1)
	in.Mode = ModeHopAugment

	sum, err := runAugment(context.Background(), deps, in, worktreeDir)
	require.NoError(t, err)
	assert.Equal(t, "main", sum.HopBranch,
		"summary must carry the augmented hop branch")
	got := listRel(worktreeDir, sum.Result.Written)
	assert.Equal(t, []string{".gitignore", "Makefile", "lint.yml"}, got)
}

func TestAugment_HopMode_PlainGitDir_FallsThrough(t *testing.T) {
	// Plain .git dir (non-hop): regular augment semantics, no dirty
	// guard, no branch capture — HopBranch stays zero-valued.
	cwd := t.TempDir()
	initGitDir(t, cwd)

	tplPath := fixtureTemplate(t)
	deps, _ := fixtureDeps()
	in := baseInputs(tplPath, "demo", 1)
	in.Mode = ModeAugment

	sum, err := runAugment(context.Background(), deps, in, cwd)
	require.NoError(t, err)
	assert.Empty(t, sum.HopBranch,
		"HopBranch must be zero-valued outside hop mode")
	got := listRel(cwd, sum.Result.Written)
	assert.Equal(t, []string{".gitignore", "Makefile", "lint.yml"}, got)
}

// hopFixture builds a bare hub plus an attached worktree on branch
// "main" and returns both paths. The initial commit is seeded via
// plumbing — `worktree add -b` fails on an unborn HEAD in a bare repo
// (the TestDetect_BareWorktree recipe skips there), and these tests
// must not skip on a fresh machine.
func hopFixture(t *testing.T) (bareDir, worktreeDir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := t.TempDir()
	bareDir = filepath.Join(root, "hub.git")
	gitRun(t, "", "init", "--bare", bareDir)
	gitRun(t, "", "-C", bareDir, "config", "user.email", "test@example.com")
	gitRun(t, "", "-C", bareDir, "config", "user.name", "Test")

	tree := gitRun(t, "", "-C", bareDir, "mktree")
	commit := gitRun(t, "", "-C", bareDir, "commit-tree", tree, "-m", "seed")
	gitRun(t, "", "-C", bareDir, "update-ref", "refs/heads/seed", commit)

	worktreeDir = filepath.Join(root, "wt")
	gitRun(t, "", "-C", bareDir, "worktree", "add", "-b", "main", worktreeDir, "seed")
	return bareDir, worktreeDir
}

// dirtyHopTree stages a file in the worktree then modifies it so
// `git status --porcelain` reports a tracked change. Staging (instead
// of committing) keeps the fixture plumbing-only — no commit hooks,
// no author resolution beyond the hub config.
func dirtyHopTree(t *testing.T, worktreeDir string) {
	t.Helper()
	path := filepath.Join(worktreeDir, "notes.txt")
	require.NoError(t, os.WriteFile(path, []byte("staged\n"), 0o600))
	gitRun(t, "", "-C", worktreeDir, "add", "notes.txt")
	require.NoError(t, os.WriteFile(path, []byte("modified\n"), 0o600))
}

// gitRun executes git with GIT_* env scrubbed (an inherited GIT_DIR —
// e.g. under a git hook — would silently retarget -C invocations) and
// returns trimmed stdout. Empty stdin keeps plumbing like mktree happy.
func gitRun(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Stdin = strings.NewReader(stdin)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

// dirNames returns the sorted top-level entry names of dir.
func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}
