// Package kitinit — shared.go composes the built-in "shared" template
// (CI workflows, gitignore/gitattributes, LICENSE, contribution docs,
// release scripts) into a scaffold, alongside whatever --from template
// was rendered. Bootstrap and augment both call renderShared; before
// this step existed, `kit init` emitted only the --from template and
// every fresh repo was born without CI, LICENSE, or a .gitignore — the
// bash scaffolder (templates/scaffold.sh) composed shared/ but the Go
// path never did (T-0983).
//
// The shared template is not a 1:1 file tree: several source groups
// need output mapping (gitignore fragments concatenate into one
// .gitignore, ci/ci-<lang>.yml lands under .github/workflows/, the
// license rule picks one LICENSE). The mapping here mirrors
// templates/lib.sh (compose_gitignore, compose_gitattributes,
// copy_polyglot_ci_workflows) so bash and Go scaffolds agree.
//
// Files with no mapping (the managed-block emitter machinery:
// emit-*.sh, apply-services.*, tool-versions.toml, *.bats) are
// reported as skipped with a reason — they are consumed by
// `kit init --update`, not copied into projects.
package kitinit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tmpl "hop.top/kit/internal/template"
)

// SharedSkip names one shared-template file that was not placed, and why.
// Reasons are machine-readable: "no-output-mapping", "tier-filter",
// "exclude-rule", "identical", "runtime-not-selected".
type SharedSkip struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// SharedSummary reports the shared-template composition outcome.
type SharedSummary struct {
	Written   []string     `json:"written,omitempty"`
	Suggested []string     `json:"suggested,omitempty"`
	Skipped   []SharedSkip `json:"skipped,omitempty"`
	License   string       `json:"license,omitempty"`
}

// sharedManagedMarkers wrap composed .gitignore/.gitattributes content
// so later tooling can locate and refresh the kit-owned block. Matches
// templates/lib.sh compose_* output byte-for-byte.
const (
	giHeader = "# >>> kit-managed: gitignore >>>\n"
	giFooter = "# <<< kit-managed: gitignore <<<\n"
	gaHeader = "# >>> kit-managed: gitattributes >>>\n"
	gaFooter = "# <<< kit-managed: gitattributes <<<\n"
)

// renderShared stages the built-in shared template through the normal
// engine (so vars, tier filtering, and the license rule behave exactly
// as for --from templates), then maps staged outputs into target with
// the same non-destructive semantics the engine uses: new files are
// written, identical files are skipped, differing files become
// .kit-suggested.<name> siblings.
//
// tier follows the caller's mode: bootstrap passes 0 (everything),
// augment passes in.Tier. dryRun plans without touching target —
// staging happens in a throwaway tempdir either way.
func renderShared(ctx context.Context, deps Deps, in Inputs, target string, tier int, dryRun bool) (SharedSummary, error) {
	var sum SharedSummary

	src, err := deps.Registry.Resolve(ctx, "shared")
	if err != nil {
		return sum, fmt.Errorf("shared: resolve: %w", err)
	}
	manifest, err := parseManifestFS(src)
	if err != nil {
		return sum, fmt.Errorf("shared: manifest: %w", err)
	}
	tiers, err := tmpl.LoadTiers(src)
	if err != nil {
		return sum, fmt.Errorf("shared: tiers: %w", err)
	}

	staging, err := os.MkdirTemp("", "kit-shared-*")
	if err != nil {
		return sum, fmt.Errorf("shared: staging: %w", err)
	}
	defer func() { _ = os.RemoveAll(staging) }()

	vars := cloneVars(in.Vars)
	vars["mode"] = "shared"
	vars["tier"] = tier
	// Defensive defaults: production Gather populates all of these, but
	// direct runBootstrap/runAugment callers (tests, embedders) build
	// Inputs by hand. Shared templates must render regardless.
	year := time.Now().UTC().Year()
	if y, ok := vars["Year"].(int); ok {
		year = y
	} else {
		vars["Year"] = year
	}
	if _, ok := vars["Copyrights"]; !ok {
		vars["Copyrights"] = DefaultCopyrights(year)
	}
	for _, k := range []string{"Description", "Email", "Author", "Module", "Name"} {
		if _, ok := vars[k]; !ok {
			vars[k] = ""
		}
	}
	if vars["Name"] == "" {
		vars["Name"] = in.Name
	}

	engine := tmpl.NewEngineWithRules(src, staging, vars, manifest.Files, manifest.RenderRules, tiers, tier, in.Force)
	res, err := engine.Render(ctx)
	if err != nil {
		return sum, fmt.Errorf("shared: render: %w", err)
	}
	// Carry engine-level skips (tier filter, exclude rules) into the
	// summary so --json accounts for every shared source file.
	for _, p := range res.Skipped {
		reason := res.SkipReasons[p]
		if reason == "" {
			reason = "tier-filter"
		}
		sum.Skipped = append(sum.Skipped, SharedSkip{Path: p, Reason: reason})
	}
	sum.License = res.LicensePicked

	runtimes := in.Runtime
	if len(runtimes) == 0 {
		runtimes = []string{"go"}
	}
	rtSet := map[string]bool{}
	for _, r := range runtimes {
		rtSet[r] = true
	}

	// place writes content at rel under target honoring conflict
	// semantics; in dryRun it only records the would-be action.
	place := func(rel string, content []byte, mode os.FileMode) error {
		dest := filepath.Join(target, rel)
		if existing, rerr := os.ReadFile(dest); rerr == nil {
			if string(existing) == string(content) {
				sum.Skipped = append(sum.Skipped, SharedSkip{Path: rel, Reason: "identical"})
				return nil
			}
			suggested := filepath.Join(filepath.Dir(rel), ".kit-suggested."+filepath.Base(rel))
			sum.Suggested = append(sum.Suggested, suggested)
			if dryRun {
				return nil
			}
			if err := os.MkdirAll(filepath.Dir(filepath.Join(target, suggested)), 0o750); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(target, suggested), content, mode)
		}
		sum.Written = append(sum.Written, rel)
		if dryRun {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
			return err
		}
		return os.WriteFile(dest, content, mode)
	}

	read := func(rel string) ([]byte, bool) {
		b, rerr := os.ReadFile(filepath.Join(staging, rel))
		return b, rerr == nil
	}

	// 1. .gitignore / .gitattributes — concat common + selected runtimes,
	// wrapped in kit-managed markers (lib.sh parity).
	composeConcat := func(dir, header, footer, out string) error {
		var buf strings.Builder
		buf.WriteString(header)
		parts := append([]string{"common"}, runtimes...)
		found := false
		for _, part := range parts {
			ext := strings.TrimPrefix(filepath.Ext(out), ".") // gitignore | gitattributes
			if b, ok := read(filepath.Join(dir, part+"."+ext)); ok {
				buf.Write(b)
				if len(b) > 0 && b[len(b)-1] != '\n' {
					buf.WriteByte('\n')
				}
				found = true
			}
		}
		buf.WriteString(footer)
		if !found {
			sum.Skipped = append(sum.Skipped, SharedSkip{Path: out, Reason: "tier-filter"})
			return nil
		}
		return place(out, []byte(buf.String()), 0o644)
	}
	if err := composeConcat("gitignore", giHeader, giFooter, ".gitignore"); err != nil {
		return sum, fmt.Errorf("shared: gitignore: %w", err)
	}
	if err := composeConcat("gitattributes", gaHeader, gaFooter, ".gitattributes"); err != nil {
		return sum, fmt.Errorf("shared: gitattributes: %w", err)
	}

	// 2. CI workflows per selected runtime + dependabot.
	for _, rt := range runtimes {
		rel := filepath.Join("ci", "ci-"+rt+".yml")
		if b, ok := read(rel); ok {
			if err := place(filepath.Join(".github", "workflows", "ci-"+rt+".yml"), b, 0o644); err != nil {
				return sum, fmt.Errorf("shared: ci-%s: %w", rt, err)
			}
		}
	}
	if b, ok := read(filepath.Join("ci", "dependabot.yml")); ok {
		if err := place(filepath.Join(".github", "dependabot.yml"), b, 0o644); err != nil {
			return sum, fmt.Errorf("shared: dependabot: %w", err)
		}
	}

	// 3. Walk remaining staged files with explicit mapping rules.
	err = filepath.WalkDir(staging, func(path string, d os.DirEntry, werr error) error {
		if werr != nil || d.IsDir() {
			return werr
		}
		rel, rerr := filepath.Rel(staging, path)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)

		switch {
		case strings.HasPrefix(rel, "gitignore/"), strings.HasPrefix(rel, "gitattributes/"):
			return nil // consumed by composeConcat above
		case strings.HasPrefix(rel, "ci/"):
			base := strings.TrimPrefix(rel, "ci/")
			if base == "dependabot.yml" {
				return nil // placed above
			}
			if lang, ok := strings.CutSuffix(strings.TrimPrefix(base, "ci-"), ".yml"); ok && !rtSet[lang] {
				sum.Skipped = append(sum.Skipped, SharedSkip{Path: rel, Reason: "runtime-not-selected"})
			}
			return nil
		case rel == "LICENSE":
			b, _ := os.ReadFile(path)
			return place("LICENSE", b, 0o644)
		case strings.HasPrefix(rel, "LICENSE-"):
			return nil // unpicked license sources stay in staging
		case rel == "CONTRIBUTING.md" || rel == "SECURITY.md" || rel == "RELEASING.md":
			b, _ := os.ReadFile(path)
			return place(rel, b, 0o644)
		case strings.HasPrefix(rel, "docs/"):
			b, _ := os.ReadFile(path)
			return place(rel, b, 0o644)
		case strings.HasPrefix(rel, "scripts/"):
			b, _ := os.ReadFile(path)
			return place(rel, b, 0o755)
		default:
			// Managed-emitter machinery and anything unmapped: named
			// explicitly so --json never hides a skipped file again.
			sum.Skipped = append(sum.Skipped, SharedSkip{
				Path:   rel,
				Reason: "no-output-mapping",
			})
			return nil
		}
	})
	if err != nil {
		return sum, fmt.Errorf("shared: map staged files: %w", err)
	}

	sort.Strings(sum.Written)
	sort.Strings(sum.Suggested)
	sort.Slice(sum.Skipped, func(i, j int) bool { return sum.Skipped[i].Path < sum.Skipped[j].Path })
	return sum, nil
}
