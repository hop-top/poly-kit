// Package suppress implements the two-tier finding-suppression rules
// from design.md §4:
//
//   - .verifynoleak.allow — gitignore-syntax path globs at the
//     adopter repo root. Negation supported (!path/to/include-again).
//   - In-file ignore comments — HTML (Markdown) or YAML form,
//     requiring a non-empty reason after "—" (or "--" fallback).
//   - In-Markdown "ignore-next-block" comment scoping the suppression
//     to the very next fence opening, ≤ 2 blank lines away.
//
// The package is pure with respect to the network/filesystem for
// match logic; it does perform filesystem reads when loading the
// allowlist file (which is itself an explicit caller op).
package suppress

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// ErrBareIgnoreRejected is returned when an "ignore" comment was
// detected but lacked a reason. Callers map this to ErrConfig.
var ErrBareIgnoreRejected = errors.New("verify-no-leak: ignore comment is missing required reason after \"—\" or \"--\"")

// Allowlist is the set of path globs loaded from .verifynoleak.allow
// plus any kit-internal defaults the caller layered on top.
type Allowlist struct {
	// patterns ordered as in file; negations (!foo) flip earlier
	// matches off — gitignore semantics.
	patterns []allowPattern
	repoRoot string // for resolving relative globs
}

type allowPattern struct {
	glob   string
	negate bool
}

// LoadAllowlist reads .verifynoleak.allow from repoRoot. Returns an
// empty allowlist (not nil) when the file doesn't exist — adopters
// can run without one.
func LoadAllowlist(repoRoot string) (*Allowlist, error) {
	path := filepath.Join(repoRoot, ".verifynoleak.allow")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Allowlist{repoRoot: repoRoot}, nil
		}
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	al := &Allowlist{repoRoot: repoRoot}
	sc := bufio.NewScanner(f)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		negate := false
		if strings.HasPrefix(line, "!") {
			negate = true
			line = strings.TrimPrefix(line, "!")
		}
		al.patterns = append(al.patterns, allowPattern{glob: line, negate: negate})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return al, nil
}

// Add appends a glob pattern to the allowlist. Used by the
// kit-internal default allowlist (design.md §5).
func (al *Allowlist) Add(globs ...string) {
	for _, g := range globs {
		negate := false
		if strings.HasPrefix(g, "!") {
			negate = true
			g = strings.TrimPrefix(g, "!")
		}
		al.patterns = append(al.patterns, allowPattern{glob: g, negate: negate})
	}
}

// Matches reports whether path is allowlisted. Path may be absolute
// or relative; matching is done against the path's repo-relative
// form when the allowlist has a repoRoot.
func (al *Allowlist) Matches(path string) bool {
	if al == nil || len(al.patterns) == 0 {
		return false
	}
	rel := path
	if al.repoRoot != "" && filepath.IsAbs(path) {
		if r, err := filepath.Rel(al.repoRoot, path); err == nil {
			rel = r
		}
	}
	// Gitignore semantics: walk patterns in order; later matches win.
	allowed := false
	for _, p := range al.patterns {
		hit, err := doublestar.Match(p.glob, rel)
		if err != nil || !hit {
			continue
		}
		allowed = !p.negate
	}
	return allowed
}

// Empty reports whether no patterns are loaded. Useful for the
// command layer to decide whether to bother running the matcher.
func (al *Allowlist) Empty() bool { return al == nil || len(al.patterns) == 0 }

// ── Ignore comments ──────────────────────────────────────────────

// ignoreCommentRE matches both YAML (#) and HTML (<!-- ... -->) ignore
// directives. The captured group is the reason; missing or empty
// reasons fail the bare-ignore check.
//
// Two separators accepted: U+2014 EM DASH "—" (canonical per design)
// and ASCII "--" followed by whitespace (fallback for environments
// without easy em-dash input). The whitespace requirement on "--"
// disambiguates from the HTML close "-->" so that
// "<!-- verify-no-leak: ignore -->" is correctly recognized as a
// bare ignore rather than an "ignore" with a reason of ">".
var ignoreCommentRE = regexp.MustCompile(`verify-no-leak:\s*ignore(?:\s*(?:—|--\s)\s*(.*?))?\s*(?:-->|$)`)

// ignoreNextBlockRE matches the Markdown-only "scope to the very
// next fence" form. Same reason rules.
var ignoreNextBlockRE = regexp.MustCompile(`verify-no-leak:\s*ignore-next-block(?:\s*(?:—|--\s)\s*(.*?))?\s*(?:-->|$)`)

// IgnoreDirective describes one matched ignore comment with its
// scope and validated reason.
type IgnoreDirective struct {
	Kind   IgnoreKind
	Line   int    // 1-based line of the directive in the source file
	Reason string // trimmed, never empty when Err is nil
}

// IgnoreKind enumerates the scope of a directive.
type IgnoreKind int

const (
	// IgnoreFile suppresses every finding in the file.
	IgnoreFile IgnoreKind = iota
	// IgnoreNextBlock suppresses findings only in the next fenced YAML block.
	IgnoreNextBlock
)

// ParseIgnoreDirectives scans the leading region of a file for
// directives. Per design.md §4:
//
//   - YAML files: scan first 5 lines.
//   - Markdown files: scan first 10 lines for IgnoreFile directives,
//     plus the entire file for IgnoreNextBlock directives (they're
//     scoped to the next fence anywhere in the file).
//
// Returns directives in source-order. A bare ignore (no reason)
// returns an empty Reason and ErrBareIgnoreRejected — the caller
// must surface this as a config error rather than honor or skip it.
func ParseIgnoreDirectives(data []byte, kind string) ([]IgnoreDirective, error) {
	switch strings.ToLower(kind) {
	case "yaml", "yml":
		return parseDirectives(data, 5, true /*fileOnly*/)
	case "md", "markdown":
		all, err := parseDirectives(data, 10, true /*fileOnly within first 10*/)
		if err != nil {
			return all, err
		}
		// Scan the whole file for ignore-next-block directives.
		extra, errExtra := parseNextBlockDirectives(data)
		if errExtra != nil {
			return append(all, extra...), errExtra
		}
		return append(all, extra...), nil
	}
	return nil, nil
}

// parseDirectives walks the first lookahead lines and emits
// IgnoreFile directives. Whole-file scope is the default; if
// fileOnly is false the caller will layer next-block detection
// separately.
func parseDirectives(data []byte, lookahead int, fileOnly bool) ([]IgnoreDirective, error) {
	var out []IgnoreDirective
	sc := bufio.NewScanner(bytes.NewReader(data))
	lineNo := 0
	for sc.Scan() && lineNo < lookahead {
		lineNo++
		line := sc.Text()
		if !strings.Contains(line, "verify-no-leak") {
			continue
		}
		// Avoid double-firing on a line that's actually a
		// next-block directive — it shares the prefix.
		if ignoreNextBlockRE.MatchString(line) {
			continue
		}
		m := ignoreCommentRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		reason := strings.TrimSpace(m[1])
		if reason == "" {
			return out, fmt.Errorf("line %d: %w", lineNo, ErrBareIgnoreRejected)
		}
		out = append(out, IgnoreDirective{Kind: IgnoreFile, Line: lineNo, Reason: reason})
	}
	return out, nil
}

// parseNextBlockDirectives finds every ignore-next-block directive
// in the source — they may appear anywhere, not just the header.
func parseNextBlockDirectives(data []byte) ([]IgnoreDirective, error) {
	var out []IgnoreDirective
	sc := bufio.NewScanner(bytes.NewReader(data))
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		if !strings.Contains(line, "verify-no-leak") {
			continue
		}
		m := ignoreNextBlockRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		reason := strings.TrimSpace(m[1])
		if reason == "" {
			return out, fmt.Errorf("line %d: %w", lineNo, ErrBareIgnoreRejected)
		}
		out = append(out, IgnoreDirective{Kind: IgnoreNextBlock, Line: lineNo, Reason: reason})
	}
	return out, nil
}

// HasFileLevelIgnore reports whether any IgnoreFile directive is in
// the slice.
func HasFileLevelIgnore(ds []IgnoreDirective) bool {
	for _, d := range ds {
		if d.Kind == IgnoreFile {
			return true
		}
	}
	return false
}

// FindIgnoreNextBlockFor returns the next-block directive (if any)
// that scopes to a fence at fenceLine, respecting the ≤ 2 blank-
// lines distance rule. Used by the scanner to decide whether to drop
// a block's findings.
//
// The rule (design.md §4): if a "ignore-next-block" comment appears
// at line L, it scopes to the immediately-following fence opener at
// line F so long as F-L ≤ 3 (== 2 blank lines + the comment line).
// Beyond that, the directive is discarded.
func FindIgnoreNextBlockFor(ds []IgnoreDirective, fenceLine int) (IgnoreDirective, bool) {
	var best IgnoreDirective
	found := false
	for _, d := range ds {
		if d.Kind != IgnoreNextBlock {
			continue
		}
		if d.Line >= fenceLine {
			continue
		}
		if fenceLine-d.Line > 3 {
			continue
		}
		if !found || d.Line > best.Line {
			best = d
			found = true
		}
	}
	return best, found
}

// DefaultKitInternalGlobs returns the default allowlist patterns
// active only when scanning the kit repo itself (design.md §5).
// Wired in by the command layer when it detects the origin.
func DefaultKitInternalGlobs() []string {
	return []string{
		".tlc/tracks/12fcc-leak/**",
		".tlc/tracks/12fcc/**",
		".tlc/tracks/12fcc-scen/**",
		".tlc/tracks/12fcc-harness/**",
		".tlc/tracks/12fcc-static/**",
		".tlc/tracks/12fcc-client/**",
		"contracts/scenario-rules.json",
		"docs/**/*verify-no-leak*.md",
		"docs/conformance/ci-integration.md",
		"go/conformance/client/testdata/**",
		"go/console/cli/conformance/verifynoleak/rules/scenario_rules_embedded.json",
		"go/conformance/scenariorules/scenario_rules_embedded.json",
		"go/conformance/scenario/testdata/**",
		"go/conformance/scenario/README.md",
		"templates/ci/grade/**",
	}
}
