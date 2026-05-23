// Package kitinit — augment.go runs the augment flow per spec §12.
//
// Unlike bootstrap, augment operates on an existing project directory: it
// renders the requested template tier into cwd, surfacing differing files
// as .kit-suggested.<name> siblings (engine contract). It does NOT init a
// git repo, create a GitHub repo, or push — those steps are bootstrap-only.
package kitinit

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"hop.top/kit/cmd/kit/init/buswf"
	tmpl "hop.top/kit/internal/template"
)

// runAugment renders the tier-filtered template into cwd. Existing files
// that differ become .kit-suggested.<name> siblings; identical content is
// a silent no-op (engine contract). Hooks run only outside DryRun.
func runAugment(ctx context.Context, deps Deps, in Inputs, cwd string) (Summary, error) {
	// Step 1: name fallback. In production Gather already applies the
	// basename(cwd) → KIT_NAME chain, but tests call runAugment directly
	// with hand-built Inputs. Why: keep this as a defense-in-depth net so
	// callers who skip Gather don't trip the engine on an empty {{.name}}.
	if in.Name == "" {
		in.Name = filepath.Base(cwd)
	}

	// Step 2: module fallback from existing go.mod.
	if in.Module == "" {
		if mod, err := readGoModule(cwd); err == nil && mod != "" {
			in.Module = mod
			if in.Vars != nil {
				in.Vars["module"] = mod
			}
		}
	}

	// Step 3: resolve template + parse manifest.
	if in.Template == "" {
		in.Template = "cli-go"
	}
	src, err := deps.Registry.Resolve(ctx, in.Template)
	if err != nil {
		return Summary{}, fmt.Errorf("augment: resolve template %q: %w", in.Template, err)
	}
	manifest, err := parseManifestFS(src)
	if err != nil {
		return Summary{}, fmt.Errorf("augment: %w", err)
	}

	// Step 4: vars (Mode + Tier) layered on top of resolved Inputs.Vars.
	// Mirror Name/Module under both the lowercase and PascalCase keys so
	// templates keep parity with Gather's setVar convention. Why: cli-go
	// templates reference {{.Name}}; missing the PascalCase key here would
	// render an empty project name even though in.Name is populated.
	vars := cloneVars(in.Vars)
	vars["mode"] = "augment"
	vars["tier"] = in.Tier
	vars["name"] = in.Name
	vars["Name"] = in.Name
	if in.Module != "" {
		vars["module"] = in.Module
		vars["Module"] = in.Module
	}

	// Step 5: tiers.yaml + engine.
	tiers, err := tmpl.LoadTiers(src)
	if err != nil {
		return Summary{}, fmt.Errorf("augment: load tiers: %w", err)
	}
	engine := tmpl.NewEngineWithRules(src, cwd, vars, manifest.Files, manifest.RenderRules, tiers, in.Tier, in.Force)

	// Step 6: render (or dry-run).
	var result tmpl.Result
	if in.DryRun {
		result, err = engine.DryRun(ctx)
	} else {
		result, err = engine.Render(ctx)
	}
	if err != nil {
		return Summary{}, fmt.Errorf("augment: render: %w", err)
	}

	// Step 6b: after-PR-open hook generation (T-0774, contract
	// §5/§6/§8). Honors the same non-destructive semantics in augment
	// mode: differing existing → .kit-suggested sibling.
	postHookSummary, posterr := GeneratePostPROpenHook(cwd, in.WithGithookPostPROpen, in.DryRun)
	if posterr != nil {
		return Summary{}, fmt.Errorf("augment: post-pr-open hook: %w", posterr)
	}

	// Steps 7-8: side-effects skipped under --dry-run.
	if !in.DryRun {
		hookCtx := tmpl.HookContext{
			Vars:      vars,
			Mode:      "augment",
			Tier:      in.Tier,
			TargetDir: cwd,
		}
		root := resolveTemplateRoot(src)
		if err := deps.Hooks.Run(ctx, "post_render", manifest.Hooks.PostRender, root, hookCtx, deps.Output); err != nil {
			return Summary{}, fmt.Errorf("augment: post_render hook: %w", err)
		}

		// Step 8: write .kit/version. We already errored on
		// ModeAlreadyKit upstream so overwrite is fine; the explicit
		// write keeps the invariant after a successful augment.
		if err := writeKitVersion(cwd, manifest.Name); err != nil {
			return Summary{}, fmt.Errorf("augment: %w", err)
		}
	}

	// Render `.github/workflows/*-caller.yml` stubs in augment mode.
	// The renderer reads `.kit/generated.json` and honors the
	// .kit-suggested fallback when a tracked file diverges from the
	// recorded hash.
	var workflowActions []WorkflowAction
	if in.WithGitHubWorkflows {
		wfActions, werr := renderWorkflows(cwd, in.Runtime, in, nil)
		if werr != nil {
			return Summary{}, fmt.Errorf("augment: render github workflows: %w", werr)
		}
		workflowActions = wfActions
	}

	// Steps 9-10: NO git.Init, NO github.Create — existing repo.

	// Existing hook is either refreshed (manifest hash matches) or
	// surfaced as a .kit-suggested sibling (user-edited). Dry-run
	// produces the same report shape without writes.
	var preprResult *PrePrResult
	if in.WithPrePrHook {
		pr, perr := GeneratePrePrHook(cwd, in.DryRun, time.Now().UTC())
		if perr != nil {
			return Summary{}, fmt.Errorf("augment: pre-pr hook: %w", perr)
		}
		preprResult = &pr
	}

	// Step 11: tlc init (best-effort). Skipped when tlc is missing or
	// when running under --dry-run.
	var tlcSkipped bool
	if !in.DryRun {
		var terr error
		tlcSkipped, terr = runTLCInit(ctx, cwd)
		if terr != nil {
			return Summary{}, fmt.Errorf("augment: %w", terr)
		}
	}

	var busPlan buswf.Plan
	if in.WithBusWorkflows {
		opts := buswf.Defaults(cwd)
		opts.DryRun = in.DryRun
		plan, err := buswf.WriteAll(opts)
		if err != nil {
			return Summary{}, fmt.Errorf("augment: bus workflows: %w", err)
		}
		busPlan = plan
	}

	summary := Summary{
		Mode:         "augment",
		Name:         in.Name,
		Target:       cwd,
		Template:     in.Template,
		Result:       result,
		TLCSkipped:   tlcSkipped,
		PrePrHook:    preprResult,
		Workflows:    workflowActions,
		BusWorkflows: busPlan.Entries,
		NextSteps:    NextSteps("augment", in.Name, nil),
	}
	applyPostHookToSummary(&summary, postHookSummary)
	return summary, nil
}

// readGoModule returns the module path declared in <dir>/go.mod. Missing
// file or absent "module" line yields ("", nil) so callers can decide
// whether to fall back to other sources without treating absence as fatal.
func readGoModule(dir string) (string, error) {
	f, err := os.Open(filepath.Join(dir, "go.mod"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("augment: read go.mod: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "module ") {
			continue
		}
		path := strings.TrimSpace(strings.TrimPrefix(line, "module "))
		if i := strings.Index(path, "//"); i >= 0 {
			path = strings.TrimSpace(path[:i])
		}
		path = strings.Trim(path, `"`)
		return path, nil
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("augment: scan go.mod: %w", err)
	}
	return "", nil
}
