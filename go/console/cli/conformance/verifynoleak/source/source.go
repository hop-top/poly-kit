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
	"sort"
	"strings"
)

// ErrNotAGitRepo is returned by Staged / Diff when invoked outside a
// git working tree. Callers map this to the io_error exit class.
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

// Paths normalises an explicit list of paths (the --paths flag).
// Each entry is resolved relative to cwd; missing entries surface
// as errors rather than silent skips, since explicit means
// intentional.
//
// Directories expand to the scannable files beneath them, matching
// what verify-stories accepts. Returning a directory verbatim let
// the scanner skip it as an unsupported extension and report zero
// scanned files while still exiting clean — a pass that measured
// nothing. A directory holding no scannable file is an error for
// the same reason.
func Paths(cwd string, paths []string) ([]string, error) {
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
			return nil, fmt.Errorf("%w: %s: %w", ErrBadPaths, p, err)
		}
		if !info.IsDir() {
			add(p)
			continue
		}
		nested, err := scannableUnder(p)
		if err != nil {
			return nil, err
		}
		if len(nested) == 0 {
			return nil, fmt.Errorf("%w: %s: directory holds no scannable files", ErrBadPaths, p)
		}
		for _, n := range nested {
			add(n)
		}
	}
	return out, nil
}

// scannableExts are the extensions the directory walk collects.
// Explicitly named files bypass this filter: naming a file is an
// instruction, naming a directory is a search.
var scannableExts = map[string]bool{
	".yaml": true,
	".yml":  true,
	".md":   true,
	".json": true,
}

// scannableUnder returns the sorted scannable files below dir,
// skipping dot-directories the way verify-stories does.
func scannableUnder(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != dir && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if scannableExts[strings.ToLower(filepath.Ext(p))] {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("source: walk %s: %w", dir, err)
	}
	sort.Strings(out)
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
