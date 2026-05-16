// Package kitinit — git.go wraps the git binary for bootstrap flow steps:
// initialize a repo, create the first commit, and push to upstream. Errors
// include trimmed stderr for diagnostic clarity.
package kitinit

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Init initializes a git repo at dir. When hop=true it dispatches to
// `git hop init` (kit ecosystem default, bare worktree layout); otherwise it
// runs `git init --initial-branch=<defaultBranch>`. Empty defaultBranch
// falls back to "main".
//
// Returns (skipped, err). skipped=true indicates a best-effort no-op
// because the requested binary (git-hop) is not on PATH; the caller
// should treat this as "no git scaffolding ran" but proceed without
// surfacing an error. Runtime failures from running the binary still
// surface as errors — only the "not on PATH" case skips silently.
func Init(ctx context.Context, dir string, hop bool, defaultBranch string) (bool, error) {
	var cmd *exec.Cmd
	if hop {
		// Best-effort: skip cleanly when git-hop is not installed. Any
		// runtime error from `git hop init` itself still propagates.
		if _, err := exec.LookPath("git-hop"); err != nil {
			return true, nil
		}
		cmd = exec.CommandContext(ctx, "git", "hop", "init", dir)
	} else {
		if defaultBranch == "" {
			defaultBranch = "main"
		}
		cmd = exec.CommandContext(ctx, "git", "init", "--initial-branch="+defaultBranch, dir)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return false, fmt.Errorf("git init: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return false, nil
}

// InitialCommit stages all files in dir and creates the first commit with
// the given message. Equivalent to:
//
//	git -C <dir> add -A && git -C <dir> commit -m <message>
func InitialCommit(ctx context.Context, dir, message string) error {
	add := exec.CommandContext(ctx, "git", "-C", dir, "add", "-A")
	if out, err := add.CombinedOutput(); err != nil {
		return fmt.Errorf("git add: %s: %w", strings.TrimSpace(string(out)), err)
	}
	commit := exec.CommandContext(ctx, "git", "-C", dir, "commit", "-m", message)
	if out, err := commit.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// Push pushes the current branch in dir to its upstream, setting
// `-u origin HEAD`. Returns a wrapped error including stderr (e.g. when no
// remote is configured).
func Push(ctx context.Context, dir string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "push", "-u", "origin", "HEAD")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git push: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}
