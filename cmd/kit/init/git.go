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

// GitInitOutcome reports what Init actually did. Skipped=true means a
// best-effort no-op because git-hop was requested but not on PATH (no
// repo exists afterwards). FellBack=true means the git-hop path failed
// on an interactivity requirement under a non-interactive run and Init
// recovered with plain `git init` (a repo DOES exist afterwards).
type GitInitOutcome struct {
	Skipped  bool
	FellBack bool
}

// Init initializes a git repo at dir. When hop=true it dispatches to
// `git hop init` (kit ecosystem default, bare worktree layout); otherwise it
// runs `git init --initial-branch=<defaultBranch>`. Empty defaultBranch
// falls back to "main".
//
// nonInteractive (from --yes) makes child processes prompt-free: git-hop
// gets --no-prompt, and if it still demands a TTY (its structure-
// conversion wizard when an enclosing repo is detected) Init falls back
// to plain `git init` instead of dying mid-scaffold with partial state
// (T-0982). Runtime failures that are NOT interactivity-related still
// propagate as errors.
func Init(ctx context.Context, dir string, hop bool, defaultBranch string, nonInteractive bool) (GitInitOutcome, error) {
	if defaultBranch == "" {
		defaultBranch = "main"
	}
	plain := func() (GitInitOutcome, error) {
		cmd := exec.CommandContext(ctx, "git", "init", "--initial-branch="+defaultBranch, dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			return GitInitOutcome{}, fmt.Errorf("git init: %s: %w", strings.TrimSpace(string(out)), err)
		}
		return GitInitOutcome{}, nil
	}
	if !hop {
		return plain()
	}
	// Best-effort: skip cleanly when git-hop is not installed. Any
	// runtime error from `git hop init` itself still propagates.
	if _, err := exec.LookPath("git-hop"); err != nil {
		return GitInitOutcome{Skipped: true}, nil
	}
	args := []string{"hop", "init"}
	if nonInteractive {
		args = append(args, "--no-prompt")
	}
	args = append(args, dir)
	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return GitInitOutcome{}, nil
	}
	if nonInteractive && isInteractivityFailure(string(out)) {
		outcome, ferr := plain()
		if ferr != nil {
			return outcome, fmt.Errorf("git hop init needed a TTY and plain-git fallback failed: %w", ferr)
		}
		outcome.FellBack = true
		return outcome, nil
	}
	return GitInitOutcome{}, fmt.Errorf("git init: %s: %w", strings.TrimSpace(string(out)), err)
}

// isInteractivityFailure sniffs git-hop output for its cannot-prompt
// failure shapes ("cannot prompt for confirmation on a non-interactive
// stdin", "pass --no-prompt", interactive chooser banners). Kept loose
// on purpose: any prompt-shaped failure under --yes is grounds for the
// plain-git fallback rather than a mid-scaffold abort.
func isInteractivityFailure(out string) bool {
	l := strings.ToLower(out)
	return strings.Contains(l, "cannot prompt") ||
		strings.Contains(l, "--no-prompt") ||
		strings.Contains(l, "non-interactive stdin") ||
		strings.Contains(l, "choose [")
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
