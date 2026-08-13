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
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := t.TempDir()
	bareDir := filepath.Join(root, "hub.git")
	gitRun(t, "", "init", "--bare", bareDir)
	gitRun(t, "", "-C", bareDir, "config", "user.email", "test@example.com")
	gitRun(t, "", "-C", bareDir, "config", "user.name", "Test")

	// Seed an initial commit via plumbing — `worktree add -b` fails on
	// an unborn HEAD in a bare repo (the TestDetect_BareWorktree recipe
	// skips there), and this test must not skip on a fresh machine.
	tree := gitRun(t, "", "-C", bareDir, "mktree")
	commit := gitRun(t, "", "-C", bareDir, "commit-tree", tree, "-m", "seed")
	gitRun(t, "", "-C", bareDir, "update-ref", "refs/heads/seed", commit)

	worktreeDir := filepath.Join(root, "wt")
	gitRun(t, "", "-C", bareDir, "worktree", "add", "-b", "main", worktreeDir, "seed")

	// Precondition: auto-detect refuses this tree.
	mode, _, err := Detect(worktreeDir, ModeUnset)
	require.NoError(t, err)
	require.Equal(t, ModeBareWorktree, mode,
		"bare-worktree cwd must surface as ModeBareWorktree under auto-detect")

	// Documented bypass: an explicit --mode augment override wins.
	mode, _, err = Detect(worktreeDir, ModeAugment)
	require.NoError(t, err)
	require.Equal(t, ModeAugment, mode,
		"explicit ModeAugment override must bypass the bare-worktree refusal")

	hubBefore := dirNames(t, bareDir)

	tplPath := fixtureTemplate(t)
	deps, _ := fixtureDeps()
	sum, err := runAugment(context.Background(), deps, baseInputs(tplPath, "demo", 1), worktreeDir)
	require.NoError(t, err)

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
