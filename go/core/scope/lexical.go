package scope

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// CheckLexical evaluates (path, op) against the rules exactly as they were
// written, without touching the filesystem. Use it to ask whether the policy
// names a path by a given spelling, e.g. whether a path that traverses a
// symlink is explicitly granted through that link. Check answers whether the
// file the path reaches may be touched; CheckLexical answers whether the rule
// text covers the string.
//
// Neither path nor patterns are resolved: no Lstat, no EvalSymlinks, so the
// result never depends on what exists on disk and cannot be used as an
// existence oracle. Path and patterns are cleaned, and a leading "~" in either
// expands to the home directory as reported by the environment ($HOME,
// %USERPROFILE%), not resolved through symlinks. Macros ("tool:*", Windows
// %VAR%) are expanded when rules are built, so they are already in the rule
// text. A trailing "/**" also matches the directory itself.
//
// The path must be absolute after "~" expansion; an empty or relative path is
// an error, as is a malformed pattern.
//
// Decisions are per op bit, deny-wins:
//
//   - Denied if any requested bit is denied by a matching deny rule.
//   - Allowed if every requested bit is allowed by a matching allow rule.
//   - Unknown otherwise, including when op requests no bits.
//
// This differs from Check, which counts a rule as matching when it shares any
// bit with op: under Check an AllowOp(Read) rule answers Allowed for
// Read|Write, while CheckLexical answers Unknown until Write is allowed too.
func (p *Policy) CheckLexical(path Path, op Op) (Decision, error) {
	h := &lazyHome{}
	abs, err := h.expand(string(path))
	if err != nil {
		return Unknown, err
	}
	if abs == "" {
		return Unknown, fmt.Errorf("scope: empty path")
	}
	abs = filepath.Clean(abs)
	if !filepath.IsAbs(abs) {
		return Unknown, fmt.Errorf("scope: lexical check needs an absolute path, got %q", path)
	}

	p.mu.RLock()
	rules := p.rules
	p.mu.RUnlock()

	if op == 0 {
		return Unknown, nil
	}

	// For each requested bit: denied, allowed, or neither.
	var denied, allowed Op
	for _, r := range rules {
		bits := r.Ops & op
		if bits == 0 {
			continue
		}
		matched, mErr := matchLexical(r.Patterns, abs, h)
		if mErr != nil {
			return Unknown, mErr
		}
		if !matched {
			continue
		}
		if r.Allow {
			allowed |= bits
		} else {
			denied |= bits
		}
	}
	switch {
	case denied != 0:
		return Denied, nil
	case allowed == op:
		return Allowed, nil
	default:
		return Unknown, nil
	}
}

// matchLexical reports whether any pattern, "~"-expanded and cleaned but
// otherwise as written, matches abs.
func matchLexical(patterns []Pattern, abs string, h *lazyHome) (bool, error) {
	for _, pat := range patterns {
		expanded, err := h.expand(string(pat))
		if err != nil {
			return false, err
		}
		ok, err := doublestar.Match(filepath.Clean(expanded), abs)
		if err != nil {
			return false, fmt.Errorf("scope: match %q: %w", pat, err)
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// lazyHome expands a leading "~" or "~/" using the home dir from the
// environment, looked up at most once and only when needed. Unlike
// expandHome it never resolves symlinks.
type lazyHome struct {
	dir string
	err error
	set bool
}

func (h *lazyHome) expand(s string) (string, error) {
	if s != "~" && !strings.HasPrefix(s, "~/") {
		return s, nil
	}
	if !h.set {
		h.dir, h.err = os.UserHomeDir()
		h.set = true
	}
	if h.err != nil {
		return "", fmt.Errorf("scope: resolve home: %w", h.err)
	}
	if s == "~" {
		return h.dir, nil
	}
	return filepath.Join(h.dir, s[2:]), nil
}
