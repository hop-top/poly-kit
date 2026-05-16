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
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrNotAGitRepo is returned by Staged / Diff when invoked outside a
// git working tree. Callers map this to the io_error exit class.
var ErrNotAGitRepo = errors.New("source: not inside a git repository")

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

// Paths normalises an explicit list of paths (the --paths flag).
// Each entry is resolved relative to cwd; missing entries surface
// as errors rather than silent skips, since explicit means
// intentional.
func Paths(cwd string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, errors.New("source: --paths requires at least one path")
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			p = filepath.Join(cwd, p)
		}
		out = append(out, p)
	}
	return out, nil
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
