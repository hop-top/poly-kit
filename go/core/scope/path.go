package scope

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// resolvePath expands "~", cleans, and follows symlinks. On ENOENT it returns
// the cleaned path (so "intent to write" still matches deny rules).
//
// For paths that don't yet exist, walks up parent dirs until one exists and
// runs EvalSymlinks on that prefix; this catches "intent to write into a
// real-but-symlinked dir" cases (e.g. writing ~/foo/x where ~/foo is a
// symlink to /etc).
func resolvePath(s string) (string, error) {
	expanded, err := expandHome(s)
	if err != nil {
		return "", err
	}
	cleaned := filepath.Clean(expanded)
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		return resolved, nil
	} else if !os.IsNotExist(err) {
		return cleaned, err
	}
	// ENOENT: walk up to the deepest existing ancestor, resolve it, and
	// re-attach the missing tail so deny rules match by intent.
	dir, tail := filepath.Split(cleaned)
	dir = strings.TrimRight(dir, string(filepath.Separator))
	for dir != "" && dir != string(filepath.Separator) {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(resolved, tail), nil
		}
		next, more := filepath.Split(dir)
		dir = strings.TrimRight(next, string(filepath.Separator))
		tail = filepath.Join(more, tail)
	}
	return cleaned, nil
}

// expandHome replaces a leading "~" or "~/..." with the user home directory.
// Other forms (e.g. "~user") are left unchanged because /etc/passwd lookup
// adds platform-specific complexity that scope deliberately avoids.
//
// The home dir is resolved through filepath.EvalSymlinks so patterns and
// resolved paths agree on platforms where $HOME is a symlink (notably macOS
// when HOME points into /var/folders, which canonicalises to /private/var/...).
func expandHome(s string) (string, error) {
	if s == "~" || strings.HasPrefix(s, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("scope: resolve home: %w", err)
		}
		if resolved, rerr := filepath.EvalSymlinks(home); rerr == nil {
			home = resolved
		}
		if s == "~" {
			return home, nil
		}
		return filepath.Join(home, s[2:]), nil
	}
	return s, nil
}

// matchAny returns true if any of the patterns matches abs (already resolved).
//
// Each pattern is expanded ("~" → home), cleaned, and any literal prefix
// (the part before the first glob meta-char) is run through EvalSymlinks so
// that patterns like /tmp/** match /private/tmp/x on platforms where /tmp is
// a symlink (notably macOS).
func matchAny(patterns []Pattern, abs string) (bool, error) {
	for _, p := range patterns {
		expanded, err := expandHome(string(p))
		if err != nil {
			return false, err
		}
		canon := canonicalisePattern(filepath.Clean(expanded))
		ok, err := doublestar.Match(canon, abs)
		if err != nil {
			return false, fmt.Errorf("scope: match %q: %w", p, err)
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// canonicalisePattern resolves the longest leading literal (non-glob) directory
// prefix of pat through EvalSymlinks so the pattern keeps matching after the
// input path is canonicalised. When the literal prefix doesn't exist, walks up
// to the deepest existing ancestor (mirroring resolvePath's behavior for
// nonexistent paths) and re-attaches the remainder.
func canonicalisePattern(pat string) string {
	parts := strings.Split(pat, string(filepath.Separator))
	cut := 0
	for i, part := range parts {
		if containsGlobMeta(part) {
			break
		}
		cut = i + 1
	}
	if cut == 0 {
		return pat
	}
	literalParts := parts[:cut]
	prefix := strings.Join(literalParts, string(filepath.Separator))
	if prefix == "" {
		return pat
	}
	// Try direct resolve first.
	if resolved, err := filepath.EvalSymlinks(prefix); err == nil {
		return joinPattern(resolved, parts[cut:])
	}
	// Walk up until we find an existing ancestor, then reattach the
	// missing literal tail and the trailing glob parts.
	for i := len(literalParts) - 1; i >= 1; i-- {
		ancestor := strings.Join(literalParts[:i], string(filepath.Separator))
		if ancestor == "" {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(ancestor); err == nil {
			tail := append([]string{}, literalParts[i:]...)
			tail = append(tail, parts[cut:]...)
			return joinPattern(resolved, tail)
		}
	}
	return pat
}

// joinPattern joins resolved with the remaining (possibly glob-bearing) parts.
func joinPattern(resolved string, rest []string) string {
	if len(rest) == 0 {
		return resolved
	}
	return resolved + string(filepath.Separator) + strings.Join(rest, string(filepath.Separator))
}

// containsGlobMeta reports whether s contains any doublestar meta character
// that would prevent it from being treated as a literal path component.
func containsGlobMeta(s string) bool {
	return strings.ContainsAny(s, "*?[]{}")
}
