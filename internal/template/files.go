// Per-file rule decisions for the kit template engine.
//
// DecideFile classifies a single source path against FileRules and the
// kit-conditional/.tmpl naming conventions, producing a FileDecision the
// engine consumes when walking the template tree.
//
// See docs/superpowers/specs/2026-04-26-kit-init-design.md §7.
package template

import (
	"bytes"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"text/template"
)

// FileAction enumerates the outcomes of evaluating a source path.
type FileAction int

const (
	// ActionRender runs the file through text/template.
	ActionRender FileAction = iota
	// ActionCopyVerbatim copies bytes without templating (binary).
	ActionCopyVerbatim
	// ActionSkip drops the file entirely (matched files.exclude).
	ActionSkip
	// ActionConditional defers to expression evaluation; the engine
	// either renders the contents or skips the subtree based on Conditional.
	ActionConditional
)

// FileDecision is the outcome of DecideFile for one source path.
type FileDecision struct {
	// Action is what the engine should do with the file.
	Action FileAction
	// OutputPath is the post-substitution, post-suffix-strip target
	// path (relative). Empty when Action is ActionSkip.
	OutputPath string
	// Conditional is the raw expression from a kit-conditional.<expr>
	// segment when Action is ActionConditional.
	Conditional string
}

// kitConditionalPrefix marks a path segment whose contents are gated on
// an expression (e.g. "kit-conditional.AccountType=org").
const kitConditionalPrefix = "kit-conditional."

// DecideFile classifies srcPath per spec §7. Precedence:
//  1. files.exclude  → ActionSkip
//  2. files.binary   → ActionCopyVerbatim (path segments substituted)
//  3. kit-conditional.<expr>/ segment → ActionConditional
//  4. matches any stripSuffixes  → ActionRender (suffix stripped)
//  5. fallback       → ActionRender
//
// Path segments are always substituted via substitutePath (text/template
// over each "/"-separated segment) for actions other than Skip.
//
// stripSuffixes lists suffixes (each starting with ".") to strip from
// the output path. nil/empty means no suffix stripping — every file
// renders to its source name. Manifests declare suffixes via
// render_rules.strip_suffixes.
func DecideFile(srcPath string, vars map[string]any, rules FileRules, stripSuffixes []string) (FileDecision, error) {
	if matchAny(srcPath, rules.Exclude) {
		return FileDecision{Action: ActionSkip}, nil
	}

	if matchAny(srcPath, rules.Binary) {
		out, err := substitutePath(srcPath, vars)
		if err != nil {
			return FileDecision{}, fmt.Errorf("binary path %q: %w", srcPath, err)
		}
		return FileDecision{Action: ActionCopyVerbatim, OutputPath: out}, nil
	}

	if expr, stripped, ok := stripConditional(srcPath); ok {
		out, err := substitutePath(stripped, vars)
		if err != nil {
			return FileDecision{}, fmt.Errorf("conditional path %q: %w", srcPath, err)
		}
		return FileDecision{
			Action:      ActionConditional,
			OutputPath:  out,
			Conditional: expr,
		}, nil
	}

	for _, suf := range stripSuffixes {
		if strings.HasSuffix(srcPath, suf) {
			stripped := strings.TrimSuffix(srcPath, suf)
			out, err := substitutePath(stripped, vars)
			if err != nil {
				return FileDecision{}, fmt.Errorf("strip path %q: %w", srcPath, err)
			}
			return FileDecision{Action: ActionRender, OutputPath: out}, nil
		}
	}

	out, err := substitutePath(srcPath, vars)
	if err != nil {
		return FileDecision{}, fmt.Errorf("path %q: %w", srcPath, err)
	}
	return FileDecision{Action: ActionRender, OutputPath: out}, nil
}

// substitutePath runs text/template over each "/"-separated segment of
// p, joining the results back. Empty segments (e.g. leading slash) are
// preserved so absolute-style paths round-trip cleanly.
func substitutePath(p string, vars map[string]any) (string, error) {
	if !strings.Contains(p, "{{") {
		return p, nil
	}
	segments := strings.Split(p, "/")
	for i, seg := range segments {
		if seg == "" || !strings.Contains(seg, "{{") {
			continue
		}
		rendered, err := renderSegment(seg, vars)
		if err != nil {
			return "", fmt.Errorf("segment %q: %w", seg, err)
		}
		segments[i] = rendered
	}
	return strings.Join(segments, "/"), nil
}

func renderSegment(seg string, vars map[string]any) (string, error) {
	t, err := template.New("seg").Option("missingkey=error").Parse(seg)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, vars); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// matchAny reports whether path matches any glob in globs using
// filepath.Match semantics (with "/" preserved). Returns false on an
// empty list. Malformed patterns are treated as non-matches so a single
// bad rule does not poison the whole walk.
func matchAny(p string, globs []string) bool {
	if len(globs) == 0 {
		return false
	}
	cleaned := filepath.ToSlash(p)
	base := path.Base(cleaned)
	for _, g := range globs {
		if g == "" {
			continue
		}
		if ok, err := path.Match(g, cleaned); err == nil && ok {
			return true
		}
		if ok, err := path.Match(g, base); err == nil && ok {
			return true
		}
	}
	return false
}

// stripConditional looks for a "kit-conditional.<expr>" segment in p. If
// found, it returns the expression, the path with that segment removed,
// and true. Only the first such segment is stripped (nested conditionals
// are evaluated by recursive walk).
func stripConditional(p string) (expr, stripped string, ok bool) {
	segments := strings.Split(p, "/")
	for i, seg := range segments {
		if !strings.HasPrefix(seg, kitConditionalPrefix) {
			continue
		}
		expr = strings.TrimPrefix(seg, kitConditionalPrefix)
		out := make([]string, 0, len(segments)-1)
		out = append(out, segments[:i]...)
		out = append(out, segments[i+1:]...)
		return expr, strings.Join(out, "/"), true
	}
	return "", "", false
}
