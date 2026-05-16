// Engine renders a template fs.FS into a target directory per spec
// §7-§8: walks the source tree, applies DecideFile per entry,
// honors kit-conditional gating, filters by tier, and writes with
// conflict-aware semantics. Hooks, registry resolution, and shelling
// out to git/gh are NOT this engine's concern.
//
// See docs/superpowers/specs/2026-04-26-kit-init-design.md §7-§8.
package template

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// Result reports what happened (or would happen, in DryRun) per file.
type Result struct {
	Written       []string // paths written under target
	Suggested     []string // <path>.kit-suggested due to conflict
	Skipped       []string // exclude rules or tier filter
	Conditional   []string // false conditional subtrees
	Removed       []string // RenderRules.RemoveAfterRender + license cleanup
	LicensePicked string   // chosen license target path (empty if rule absent)
}

// Engine renders src into target. Construct via New; invoke Render or
// DryRun. Engine is single-use; create a new one per render.
type Engine struct {
	src         fs.FS
	target      string
	vars        map[string]any
	rules       FileRules
	renderRules RenderRules
	tiers       map[string][]int
	tier        int
	force       bool // overwrite differing non-sacred files
	dryRun      bool
}

// NewEngine constructs an Engine with a legacy-compatible RenderRules
// block (strip ".tmpl"; no remove-after-render; no license rule).
// target should be an absolute directory.
// tiers maps output paths to applicable tier numbers; tier=0 disables
// the filter (bootstrap mode). force=true overwrites differing existing
// files except for sacred paths (see isSacred); force=false preserves
// the original conflict-aware behavior (sibling .kit-suggested).
//
// Production callers should resolve render_rules from the manifest via
// NewEngineWithRules; this constructor exists for tests and existing
// callers that don't yet thread the manifest through.
func NewEngine(src fs.FS, target string, vars map[string]any,
	rules FileRules, tiers map[string][]int, tier int, force bool) *Engine {
	legacy := RenderRules{StripSuffixes: []string{".tmpl"}}
	return NewEngineWithRules(src, target, vars, rules, legacy, tiers, tier, force)
}

// NewEngineWithRules is NewEngine plus an explicit RenderRules block
// from the manifest. Pass RenderRules{} for legacy behavior.
func NewEngineWithRules(src fs.FS, target string, vars map[string]any,
	rules FileRules, renderRules RenderRules, tiers map[string][]int, tier int, force bool) *Engine {
	return &Engine{
		src:         src,
		target:      target,
		vars:        vars,
		rules:       rules,
		renderRules: renderRules,
		tiers:       tiers,
		tier:        tier,
		force:       force,
	}
}

// Render walks src and writes outputs into target.
func (e *Engine) Render(ctx context.Context) (Result, error) {
	e.dryRun = false
	return e.walk(ctx)
}

// DryRun walks src and reports what would happen without writing.
func (e *Engine) DryRun(ctx context.Context) (Result, error) {
	e.dryRun = true
	return e.walk(ctx)
}

func (e *Engine) walk(ctx context.Context) (Result, error) {
	var res Result
	// Render tier keys with the same vars as files so keys like
	// "cmd/{{.name}}/main.go" match the post-substitution output
	// paths produced by emit. Done once up-front; emit then uses
	// the rendered map for O(1) lookups via AppliesAtTier.
	rendered, err := renderTierKeys(e.tiers, e.vars)
	if err != nil {
		return res, err
	}
	e.tiers = rendered
	err = fs.WalkDir(e.src, ".", func(srcPath string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if srcPath == "." {
			return nil
		}
		decision, err := DecideFile(srcPath, e.vars, e.rules, e.renderRules.StripSuffixes)
		if err != nil {
			return fmt.Errorf("template: decide %q: %w", srcPath, err)
		}
		switch decision.Action {
		case ActionSkip:
			res.Skipped = append(res.Skipped, srcPath)
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		case ActionConditional:
			ok, err := evalConditional(decision.Conditional, e.vars)
			if err != nil {
				return fmt.Errorf("template: %q: %w", srcPath, err)
			}
			if !ok {
				res.Conditional = append(res.Conditional, srcPath)
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			// True: descend into dirs; for files under a conditional
			// dir DecideFile will still return ActionConditional, so
			// we treat them as Render after the gate passes.
			if d.IsDir() {
				return nil
			}
			return e.emit(srcPath, decision.OutputPath, true, &res)
		case ActionCopyVerbatim:
			if d.IsDir() {
				return nil
			}
			return e.emit(srcPath, decision.OutputPath, false, &res)
		case ActionRender:
			if d.IsDir() {
				return nil
			}
			return e.emit(srcPath, decision.OutputPath, true, &res)
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	if err := e.applyRenderRules(&res); err != nil {
		return res, err
	}
	return res, nil
}

// applyRenderRules runs the post-walk rules from the manifest:
//   - LicenseRule: pick a source by variable value, copy to target,
//     remove all sources.
//   - RemoveAfterRender: delete project-relative files at the project
//     root.
//
// Strip is handled inline during walk via DecideFile, not here.
// dryRun suppresses bytes-on-disk; the Result is updated regardless so
// callers see what would happen.
func (e *Engine) applyRenderRules(res *Result) error {
	if e.renderRules.LicenseRule != nil {
		if err := e.applyLicenseRule(res, e.renderRules.LicenseRule); err != nil {
			return err
		}
	}
	for _, rel := range e.renderRules.RemoveAfterRender {
		full := filepath.Join(e.target, filepath.FromSlash(rel))
		if !e.dryRun {
			if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("template: remove %q: %w", rel, err)
			}
		}
		res.Removed = append(res.Removed, rel)
	}
	return nil
}

func (e *Engine) applyLicenseRule(res *Result, lr *LicenseRule) error {
	rawVal, ok := e.vars[lr.Var]
	if !ok {
		return nil // variable not set; leave sources in place
	}
	val := fmt.Sprintf("%v", rawVal)
	srcRel, ok := lr.Sources[val]
	if !ok {
		return nil // unknown choice; leave sources in place
	}
	srcAbs := filepath.Join(e.target, filepath.FromSlash(srcRel))
	dstAbs := filepath.Join(e.target, filepath.FromSlash(lr.Target))
	if !e.dryRun {
		data, err := os.ReadFile(srcAbs)
		if err != nil {
			// Source absent (template doesn't ship the file in this
			// composition path) is not an error — license rules are
			// declarative and may apply to a different rendering
			// pipeline. Leave sources alone, no copy.
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("template: license read %q: %w", srcRel, err)
		}
		if err := os.WriteFile(dstAbs, data, 0o640); err != nil {
			return fmt.Errorf("template: license write %q: %w", lr.Target, err)
		}
	}
	res.LicensePicked = lr.Target
	for _, s := range lr.Sources {
		full := filepath.Join(e.target, filepath.FromSlash(s))
		if !e.dryRun {
			if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("template: license cleanup %q: %w", s, err)
			}
		}
		res.Removed = append(res.Removed, s)
	}
	return nil
}

// emit handles tier filtering, content production, and conflict-aware
// writing for a single file. render=true runs text/template; false
// copies bytes verbatim.
func (e *Engine) emit(srcPath, outPath string, render bool, res *Result) error {
	if !AppliesAtTier(outPath, e.tiers, e.tier) {
		res.Skipped = append(res.Skipped, srcPath)
		return nil
	}
	raw, err := fs.ReadFile(e.src, srcPath)
	if err != nil {
		return fmt.Errorf("template: read %q: %w", srcPath, err)
	}
	content := raw
	if render {
		rendered, err := renderBytes(srcPath, raw, e.vars)
		if err != nil {
			return err
		}
		content = rendered
	}
	targetPath := filepath.Join(e.target, filepath.FromSlash(outPath))
	return e.write(targetPath, content, res)
}

// write applies conflict semantics. Identical existing → silent skip.
// Differing existing → sacred files always route to sibling
// .kit-suggested (never overwritten, even with force); non-sacred
// files overwrite when force=true and otherwise route to .kit-suggested.
// Missing target → write. dryRun suppresses bytes-on-disk but mirrors
// the Result of a real run.
func (e *Engine) write(targetPath string, content []byte, res *Result) error {
	existing, err := os.ReadFile(targetPath)
	switch {
	case err == nil:
		if bytes.Equal(existing, content) {
			return nil // idempotent: silent skip
		}
		// Force overwrites only non-sacred files; sacred always routes
		// to .kit-suggested so user customizations stay intact.
		rel, relErr := filepath.Rel(e.target, targetPath)
		if relErr != nil {
			rel = targetPath
		}
		if e.force && !isSacred(filepath.ToSlash(rel)) {
			if !e.dryRun {
				if err := os.MkdirAll(filepath.Dir(targetPath), 0o750); err != nil {
					return fmt.Errorf("template: mkdir %q: %w", filepath.Dir(targetPath), err)
				}
				if err := os.WriteFile(targetPath, content, 0o640); err != nil {
					return fmt.Errorf("template: write %q: %w", targetPath, err)
				}
			}
			res.Written = append(res.Written, targetPath)
			return nil
		}
		suggested := targetPath + ".kit-suggested"
		if !e.dryRun {
			if err := os.MkdirAll(filepath.Dir(suggested), 0o750); err != nil {
				return fmt.Errorf("template: mkdir %q: %w", filepath.Dir(suggested), err)
			}
			if err := os.WriteFile(suggested, content, 0o640); err != nil {
				return fmt.Errorf("template: write %q: %w", suggested, err)
			}
		}
		res.Suggested = append(res.Suggested, suggested)
		return nil
	case os.IsNotExist(err):
		if !e.dryRun {
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o750); err != nil {
				return fmt.Errorf("template: mkdir %q: %w", filepath.Dir(targetPath), err)
			}
			if err := os.WriteFile(targetPath, content, 0o640); err != nil {
				return fmt.Errorf("template: write %q: %w", targetPath, err)
			}
		}
		res.Written = append(res.Written, targetPath)
		return nil
	default:
		return fmt.Errorf("template: stat %q: %w", targetPath, err)
	}
}

func renderBytes(srcPath string, raw []byte, vars map[string]any) ([]byte, error) {
	t, err := template.New(srcPath).Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return nil, fmt.Errorf("template: parse %q: %w", srcPath, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, vars); err != nil {
		return nil, fmt.Errorf("template: execute %q: %w", srcPath, err)
	}
	return buf.Bytes(), nil
}

// evalConditional evaluates a kit-conditional expression against vars.
//
// Grammar:
//
//	expr   = or
//	or     = and ( "||" and )*
//	and    = clause ( "&&" clause )*
//	clause = [ "!" ] key "=" value
//
// Precedence: "&&" binds tighter than "||" (standard). Parentheses are
// not supported. A clause matches when vars[key] stringifies to value;
// a missing var evaluates to false at the clause level. Leading "!"
// negates that clause result. The simple "key=value" form is preserved
// and behaves identically to v1.
//
// Whitespace surrounding operators and around the leading "!" is
// trimmed. Whitespace inside key/value is significant. All clauses are
// validated up-front; malformed input is reported even when an earlier
// disjunct already matched (no silent skip via short-circuit).
func evalConditional(expr string, vars map[string]any) (bool, error) {
	// Parse into OR of AND of clauses, validating every leaf so malformed
	// expressions surface even when boolean short-circuit would skip them.
	type clause struct {
		key, want string
		negate    bool
	}
	orGroups := strings.Split(expr, "||")
	parsed := make([][]clause, 0, len(orGroups))
	for _, og := range orGroups {
		andParts := strings.Split(og, "&&")
		group := make([]clause, 0, len(andParts))
		for _, raw := range andParts {
			c := strings.TrimSpace(raw)
			if c == "" {
				return false, fmt.Errorf("template: conditional expression %q: empty clause", expr)
			}
			negate := false
			if strings.HasPrefix(c, "!") {
				negate = true
				c = strings.TrimSpace(strings.TrimPrefix(c, "!"))
				if c == "" {
					return false, fmt.Errorf("template: conditional expression %q: empty clause after negation", expr)
				}
			}
			parts := strings.SplitN(c, "=", 2)
			if len(parts) != 2 {
				return false, fmt.Errorf("template: conditional expression %q: must be key=value", expr)
			}
			group = append(group, clause{key: parts[0], want: parts[1], negate: negate})
		}
		parsed = append(parsed, group)
	}

	for _, group := range parsed {
		allMatch := true
		for _, cl := range group {
			got, ok := vars[cl.key]
			var match bool
			if ok {
				match = fmt.Sprintf("%v", got) == cl.want
			}
			if cl.negate {
				match = !match
			}
			if !match {
				allMatch = false
				break
			}
		}
		if allMatch {
			return true, nil
		}
	}
	return false, nil
}
