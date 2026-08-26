// Package source resolves the verify-no-leak scan-source flags into
// a concrete list of file paths. Each scan-source is a function
// returning ([]string, error); the command layer picks exactly one
// based on the flag combination (design.md §6: mutually-exclusive
// scan sources).
package source

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrNotAGitRepo is returned by Staged / Diff when invoked outside a
// git working tree. Callers map this to the io (transient) exit class.
var ErrNotAGitRepo = errors.New("source: not inside a git repository")

// ErrBadPaths wraps every --paths resolution failure: a missing
// entry, or a directory holding nothing scannable. Callers map this
// to the config_error exit class rather than io_error, because the
// conformance action excludes io_error from its fail-on set — an
// unscannable --paths would otherwise pass CI silently, which is the
// vacuous result this expansion exists to prevent.
var ErrBadPaths = errors.New("source: unusable --paths")

// Staged lists files in the git index (those that would be in the
// next commit). Used by --staged / the pre-commit hook.
func Staged(cwd string) ([]string, error) {
	out, err := runGit(cwd, "diff", "--cached", "--name-only", "--diff-filter=ACMR")
	if err != nil {
		return nil, err
	}
	return splitPaths(out, cwd), nil
}

// Diff lists files changed between the two ends of a ref spec like
// "origin/main...HEAD". Used by --diff in CI.
func Diff(cwd, spec string) ([]string, error) {
	if spec == "" {
		return nil, errors.New("source: --diff requires a ref spec like \"origin/main...HEAD\"")
	}
	out, err := runGit(cwd, "diff", "--name-only", "--diff-filter=ACMR", spec)
	if err != nil {
		return nil, err
	}
	return splitPaths(out, cwd), nil
}

// Audit lists every tracked file in the working tree. Used by
// --audit. By design we never consult .gitignore here — see survey
// R2: an accidentally `git add -f`ed scenario is exactly what audit
// mode should still catch.
func Audit(cwd string) ([]string, error) {
	out, err := runGit(cwd, "ls-files")
	if err != nil {
		// Audit mode without a git repo: fall back to walking from
		// cwd. Useful for scanning a directory that isn't a checkout.
		if errors.Is(err, ErrNotAGitRepo) {
			return walkPaths(cwd)
		}
		return nil, err
	}
	return splitPaths(out, cwd), nil
}

// Paths expands an explicit list of paths (the --paths flag) into
// concrete file paths. Each entry is resolved relative to cwd.
//
// Directory entries are walked recursively: dot-directories are pruned
// (except an explicitly passed root) and only files matching the
// supported predicate are collected, mirroring verify-stories'
// expansion. A nil predicate collects every regular file. The combined
// list is deduplicated; per-directory walk output is lexicographic.
//
// An entry that does not resolve is an error, not a silent skip: it
// wraps ErrBadPaths so the command layer classifies it as a config
// error. io_error is excluded from the conformance action's fail-on
// set, so passing a typo'd --paths through would let it go green in
// CI having scanned nothing. Callers that genuinely want the lenient
// behavior opt in via PathsAllowingMissing.
//
// A directory that resolves but holds no scannable file is NOT an
// error — an all-Go tree is legitimately clean. That case exits 0 and
// the command layer warns "0 files scanned" on stderr.
func Paths(cwd string, paths []string, supported func(string) bool) ([]string, error) {
	return resolvePaths(cwd, paths, supported, false)
}

// PathsAllowingMissing is Paths with unusable entries downgraded from
// errors to pass-throughs: an unresolvable entry reaches the scanner
// and surfaces in the report's skipped list instead of failing.
//
// This trades a loud failure for a silent one, so it is opt-in and
// never the default. A gate that resolves no files still exits clean
// under this mode — only pass it when scanning nothing is an
// acceptable outcome.
func PathsAllowingMissing(cwd string, paths []string, supported func(string) bool) ([]string, error) {
	return resolvePaths(cwd, paths, supported, true)
}

func resolvePaths(cwd string, paths []string, supported func(string) bool, allowMissing bool) ([]string, error) {
	if len(paths) == 0 {
		return nil, errors.New("source: --paths requires at least one path")
	}
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	add := func(p string) {
		if _, dup := seen[p]; dup {
			return
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			p = filepath.Join(cwd, p)
		}
		info, err := os.Stat(p)
		if err != nil {
			if allowMissing {
				// Pass through so the scanner records it skipped
				// with the stat reason.
				add(p)
				continue
			}
			return nil, fmt.Errorf("%w: %s: %w", ErrBadPaths, p, err)
		}
		if !info.IsDir() {
			add(p)
			continue
		}
		walked, werr := expandDir(p, supported)
		if werr != nil {
			return nil, fmt.Errorf("walk %s: %w", p, werr)
		}
		for _, f := range walked {
			add(f)
		}
	}
	return out, nil
}

// expandDir recursively collects supported regular files under root.
// Dot-directories are pruned; the root itself is exempt so an
// explicitly passed dot-directory still scans.
//
// Recursion goes through os.ReadDir on each directory path rather
// than filepath.WalkDir, matching verify-stories' expandPaths /
// walkYAMLs (verify_stories.go). ReadDir is called with a path
// string, so a symlink root is followed to the files behind it;
// WalkDir instead Lstats the root DirEntry, which reports a
// symlink-to-directory as neither a directory nor a regular file
// and silently yields nothing. Nested symlinked subdirectories are
// intentionally not followed here either way, consistent with
// verify-stories.
func expandDir(root string, supported func(string) bool) ([]string, error) {
	var out []string
	if err := walkDirEntries(root, root, supported, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func walkDirEntries(root, dir string, supported func(string) bool, out *[]string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if e.IsDir() {
			if p != root && strings.HasPrefix(e.Name(), ".") {
				continue
			}
			if err := walkDirEntries(root, p, supported, out); err != nil {
				return err
			}
			continue
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if supported == nil || supported(p) {
			*out = append(*out, p)
		}
	}
	return nil
}

// CommitRange lists commit-message bodies for `<base>..HEAD`. Each
// message becomes a synthetic "path" of the form "commit:<sha>" with
// the body as the content; callers feed these to the markdown
// scanner (since commit messages frequently contain fenced YAML).
func CommitRange(cwd, spec string) ([]CommitMessage, error) {
	if spec == "" {
		return nil, errors.New("source: --commit-range requires a ref spec like \"origin/main..HEAD\"")
	}
	out, err := runGit(cwd, "log", "--format=__SHA__%H__SHA__%n%B", spec)
	if err != nil {
		return nil, err
	}
	return splitCommitMessages(out), nil
}

// CommitMessage carries one commit's SHA + message body. The body is
// fed to the markdown scanner verbatim.
type CommitMessage struct {
	SHA  string
	Body []byte
}

// splitCommitMessages parses the __SHA__-delimited output of git log
// into one CommitMessage per commit.
func splitCommitMessages(out string) []CommitMessage {
	parts := strings.Split(out, "__SHA__")
	var msgs []CommitMessage
	for i := 1; i+1 < len(parts); i += 2 {
		sha := parts[i]
		body := parts[i+1]
		body = strings.TrimLeft(body, "\n")
		msgs = append(msgs, CommitMessage{SHA: sha, Body: []byte(body)})
	}
	return msgs
}

// runGit executes a git subcommand in cwd. Errors that look like
// "not a git repository" are normalised to ErrNotAGitRepo so callers
// can decide policy. Other errors are wrapped with the stderr body.
func runGit(cwd string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			stderr := string(exitErr.Stderr)
			if strings.Contains(stderr, "not a git repository") {
				return "", ErrNotAGitRepo
			}
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(stderr))
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// splitPaths splits newline-delimited git output into absolute paths.
func splitPaths(out, cwd string) []string {
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !filepath.IsAbs(line) {
			line = filepath.Join(cwd, line)
		}
		paths = append(paths, line)
	}
	return paths
}

// walkPaths is the non-git audit fallback. Lists every regular file
// under cwd; the scanner classifier filters by extension. .git/ and
// node_modules/ are pruned because they're never the leak channel
// we care about and walking them on a real project is wasteful.
func walkPaths(cwd string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(cwd, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
