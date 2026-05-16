package kitinit

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Mode int

const (
	ModeUnset        Mode = iota - 1 // sentinel "no override" (-1)
	ModeBootstrap                    // 0
	ModeAugment                      // 1
	ModeAlreadyKit                   // 2
	ModeBareWorktree                 // 3
)

func (m Mode) String() string {
	switch m {
	case ModeBootstrap:
		return "bootstrap"
	case ModeAugment:
		return "augment"
	case ModeAlreadyKit:
		return "already_kit"
	case ModeBareWorktree:
		return "bare_worktree"
	default:
		return "unset"
	}
}

// Detect determines the appropriate mode for `kit init` in cwd.
// override != ModeUnset takes precedence (escape hatch).
//
// Detection order (when override == ModeUnset):
//  1. git rev-parse --git-common-dir != --git-dir → ModeBareWorktree
//  2. .kit/version exists → ModeAlreadyKit (file content = version)
//  3. .git/ exists → ModeAugment
//  4. else → ModeBootstrap
//
// Always returns nil error; callers translate ModeAlreadyKit/ModeBareWorktree
// to errors via errors.go factories.
//
// For ModeAlreadyKit, the second return value is the version string from .kit/version.
func Detect(cwd string, override Mode) (Mode, string, error) {
	if override != ModeUnset {
		return override, "", nil
	}

	// 1. Bare-worktree detection via git rev-parse
	if isBareWorktree(cwd) {
		return ModeBareWorktree, "", nil
	}

	// 2. .kit/version
	versionPath := filepath.Join(cwd, ".kit", "version")
	if data, err := os.ReadFile(versionPath); err == nil {
		return ModeAlreadyKit, strings.TrimSpace(string(data)), nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return ModeBootstrap, "", err // unexpected read error
	}

	// 3. .git/ exists
	if _, err := os.Stat(filepath.Join(cwd, ".git")); err == nil {
		return ModeAugment, "", nil
	}

	// 4. else
	return ModeBootstrap, "", nil
}

// DetectWithName extends Detect by considering a positional name arg.
// If name != "" AND cwd/name does not yet exist, force ModeBootstrap
// regardless of cwd's git/.kit state — the user is creating a new
// project under cwd, not augmenting cwd itself.
//
// This protects against accidental scaffold-into-parent: e.g.,
// running `kit init mytool` from a populated dir would otherwise
// detect ModeAugment from cwd's .git/ and render templates over the
// parent's files. With a name + non-existent target, Bootstrap is
// the unambiguous intent.
//
// override still takes precedence; bare-worktree + already-kit cwd
// states still surface as errors before the name check (a populated
// cwd that's already a kit project is a stronger signal than the
// user's positional arg).
func DetectWithName(cwd, name string, override Mode) (Mode, string, error) {
	mode, version, err := Detect(cwd, override)
	if err != nil {
		return mode, version, err
	}
	if override != ModeUnset {
		return mode, version, nil
	}
	switch mode {
	case ModeBareWorktree, ModeAlreadyKit:
		return mode, version, nil
	}
	if name == "" {
		return mode, version, nil
	}
	target := filepath.Join(cwd, name)
	if _, statErr := os.Stat(target); errors.Is(statErr, fs.ErrNotExist) {
		return ModeBootstrap, "", nil
	}
	return mode, version, nil
}

// isBareWorktree returns true if cwd is inside a git worktree whose
// gitdir differs from common-dir (i.e., not a regular clone).
func isBareWorktree(cwd string) bool {
	common, errCommon := runGitRevParse(cwd, "--git-common-dir")
	gitdir, errGit := runGitRevParse(cwd, "--git-dir")
	if errCommon != nil || errGit != nil {
		return false // not in a git repo at all → not a bare worktree
	}
	return common != gitdir
}

func runGitRevParse(cwd, flag string) (string, error) {
	cmd := exec.Command("git", "rev-parse", flag)
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
