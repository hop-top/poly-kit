// Package shim holds the small set of mapping helpers that adapters
// reuse when a target CLI is missing a universal option's exact
// equivalent. The catalog is closed (S-1…S-6 in spec §15.5);
// adapters do not invent new shims locally.
package shim

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"hop.top/kit/go/core/uxp/invoke"
)

// ExpandToParentDirs (S-1) returns the minimal set of directories
// that contain every file in files, with no directory subsumed by
// another in the result.
//
// Used by adapters whose target CLI accepts directory scope but not
// individual files (gemini, codex, copilot, qwen, kimi). Output is
// sorted ascending and deduplicated. Empty input → empty output.
//
// Behavior:
//   - Each path's filepath.Dir is taken as its initial parent.
//   - The result is the set of ancestors with no other ancestor as
//     a prefix. (a/b is dropped if a is also present.)
//
// The function does not stat the filesystem; it works purely on
// path strings. Callers should pass cleaned (filepath.Clean) paths.
func ExpandToParentDirs(files []string) []string {
	if len(files) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(files))
	for _, f := range files {
		if f == "" {
			continue
		}
		seen[filepath.Dir(filepath.Clean(f))] = struct{}{}
	}
	parents := make([]string, 0, len(seen))
	for p := range seen {
		parents = append(parents, p)
	}
	sort.Strings(parents)

	// Drop any parent that is a descendant of an earlier (shorter) one.
	out := parents[:0]
	for _, p := range parents {
		subsumed := false
		for _, kept := range out {
			if isAncestor(kept, p) {
				subsumed = true
				break
			}
		}
		if !subsumed {
			out = append(out, p)
		}
	}
	return out
}

// isAncestor reports whether parent is an ancestor of child (or
// equal). Both must be cleaned. Uses string-prefix check with
// separator awareness so /a is not considered an ancestor of /ab.
func isAncestor(parent, child string) bool {
	if parent == child {
		return true
	}
	sep := string(filepath.Separator)
	prefix := parent
	if !strings.HasSuffix(prefix, sep) {
		prefix += sep
	}
	return strings.HasPrefix(child, prefix)
}

// EnumerateDirFiles (S-2) walks dir and returns regular file paths up
// to max. The returned overflow is true when more files exist than
// max permits; callers typically refuse the build in that case
// rather than silently truncate.
//
// filter, if non-nil, is consulted for every file: returning false
// excludes the file from the result. .gitignore semantics are NOT
// implemented here — adapters that want them wire a filter that
// shells out to `git check-ignore` (cheap when the tree is already
// a git repo) or accept the broader walk. Keeping the dep surface
// of this package empty is intentional.
//
// Skipped automatically:
//   - directories (only files are returned),
//   - any path failing fs.WalkDir (errors propagate).
//
// max <= 0 means "no cap"; the walk completes and overflow is false.
func EnumerateDirFiles(dir string, max int, filter func(path string) bool) (files []string, overflow bool, err error) {
	if dir == "" {
		return nil, false, fmt.Errorf("shim: EnumerateDirFiles: empty dir")
	}
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if filter != nil && !filter(path) {
			return nil
		}
		if max > 0 && len(files) >= max {
			overflow = true
			return fs.SkipAll
		}
		files = append(files, path)
		return nil
	})
	if walkErr != nil {
		return nil, false, walkErr
	}
	sort.Strings(files)
	return files, overflow, nil
}

// FormatFileBlock (S-3) renders a deterministic prompt prefix listing
// files. Used as a fallback for CLIs without native file/dir scoping.
//
// Files render as paths relative to cwd when cwd != "" and the file
// lies under it; otherwise the original path is preserved. Empty
// input → empty string (caller skips the prefix).
func FormatFileBlock(files []string, cwd string) string {
	if len(files) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "The following files are relevant to this task:\n")
	rendered := make([]string, len(files))
	for i, f := range files {
		rendered[i] = relTo(cwd, f)
	}
	sort.Strings(rendered)
	for _, r := range rendered {
		fmt.Fprintf(&b, "- %s\n", r)
	}
	if cwd != "" {
		fmt.Fprintf(&b, "(%d files total; tree rooted at %s.)\n", len(files), cwd)
	} else {
		fmt.Fprintf(&b, "(%d files total.)\n", len(files))
	}
	return b.String()
}

// relTo returns p relative to base when possible, else p unchanged.
// Empty base or paths that escape base (.. prefix) return p as-is.
func relTo(base, p string) string {
	if base == "" {
		return p
	}
	rel, err := filepath.Rel(base, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return p
	}
	return rel
}

// RefuseDangerousDegradation builds the standard error-level
// Diagnostic for the "never silently widen authority" rule (spec
// §15.5 anti-shims). option is the universal option name (e.g.
// "Approval"); requested is the value the caller asked for; nativeFlag
// is the dangerous native flag the adapter might have used; safer
// lists alternative values the caller could pick instead.
func RefuseDangerousDegradation(option, requested, nativeFlag string, safer []string) invoke.Diagnostic {
	var b strings.Builder
	fmt.Fprintf(&b, "%s=%q has no safe equivalent on this CLI; ", option, requested)
	fmt.Fprintf(&b, "native flag %q would widen authority beyond the requested scope. ", nativeFlag)
	if len(safer) > 0 {
		fmt.Fprintf(&b, "Safer alternatives: %s.", strings.Join(safer, ", "))
	} else {
		fmt.Fprintf(&b, "No safer alternative available.")
	}
	return invoke.Diagnostic{
		Level:   "error",
		Option:  option,
		Message: b.String(),
	}
}

// SplitConfigList parses a comma-separated Config value with
// backslash-escape support. Used by adapters for repeatable Config
// keys (e.g. "copilot.allow_tool=shell(git:*),write,WebSearch").
//
// Whitespace around items is trimmed; empty items after trimming are
// dropped.
func SplitConfigList(value string) []string {
	if value == "" {
		return nil
	}
	var (
		out  []string
		cur  strings.Builder
		prev rune
	)
	for _, r := range value {
		switch {
		case prev == '\\':
			cur.WriteRune(r)
			prev = 0
		case r == '\\':
			prev = r
		case r == ',':
			if s := strings.TrimSpace(cur.String()); s != "" {
				out = append(out, s)
			}
			cur.Reset()
			prev = 0
		default:
			cur.WriteRune(r)
			prev = 0
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, s)
	}
	return out
}
