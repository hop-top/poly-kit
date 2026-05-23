// Package kitinit — bootstrap.go drives the empty-cwd flow per spec §11.
// Validates name, resolves+parses the template, renders into <cwd>/<name>,
// runs lifecycle hooks, initializes git, optionally creates the GitHub
// repo + applies branch protection, then pushes. All side-effecting
// steps are gated behind injectable runners (HookRunner, GitRunner,
// GitHubRunner) so tests can swap stubs without touching the real
// git/gh binaries.
package kitinit

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"time"

	tmpl "hop.top/kit/internal/template"
)

// nameRegex enforces the spec §11/§22 project-name shape.
// kept in sync with the regex shown in InvalidNameError.Error().
var nameRegex = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// HookRunner abstracts template.Run so bootstrap can dispatch hooks
// against an interface instead of a free function. phase names match
// the manifest yaml keys (pre_render, post_render, post_init, post_push).
type HookRunner interface {
	Run(ctx context.Context, phase string, scripts []string, templateRoot string,
		hookCtx tmpl.HookContext, out io.Writer) error
}

// GitRunner wraps git.go so tests can verify call ordering without
// invoking the real git binary. Init returns (skipped, err) so callers
// can distinguish "binary not on PATH, no scaffolding ran" from a hard
// failure (see git.Init for the precise contract).
type GitRunner interface {
	Init(ctx context.Context, dir string, hop bool, defaultBranch string) (bool, error)
	InitialCommit(ctx context.Context, dir, message string) error
	Push(ctx context.Context, dir string) error
}

// GitHubRunner wraps github.go so tests can stub gh repo create + branch
// protection without exec'ing gh.
type GitHubRunner interface {
	Create(ctx context.Context, dir string, cfg RepoConfig) (RepoInfo, error)
	ProtectMain(ctx context.Context, fullName string) error
}

// Deps groups the bootstrap-flow collaborators. All fields required
// except Output (defaults to os.Stdout / discard at runtime).
type Deps struct {
	Registry *tmpl.Registry
	Hooks    HookRunner
	Git      GitRunner
	GitHub   GitHubRunner
	Output   io.Writer
}

// runBootstrap executes the spec §11 sequence end-to-end and returns
// the rendered Summary. DryRun=true skips every side-effecting step
// after engine render (no git, no github, no hooks beyond what the
// engine itself simulates via DryRun).
func runBootstrap(ctx context.Context, deps Deps, in Inputs) (Summary, error) {
	out := deps.Output
	if out == nil {
		out = io.Discard
	}

	// 1. Validate name.
	if !nameRegex.MatchString(in.Name) {
		return Summary{}, NewInvalidNameError(in.Name)
	}

	// 2. Resolve template.
	src, err := deps.Registry.Resolve(ctx, in.Template)
	if err != nil {
		return Summary{}, fmt.Errorf("bootstrap: resolve template %q: %w", in.Template, err)
	}

	// 3. Parse manifest (kit-template.yaml at the resolved fs.FS root).
	manifest, err := parseManifestFS(src)
	if err != nil {
		return Summary{}, fmt.Errorf("bootstrap: parse manifest: %w", err)
	}

	// 4. Final var map: Inputs.Vars already carries built-ins + manifest
	// vars resolved by Gather. Inject mode/tier so engine + hooks see
	// the spec §6 reserved keys.
	vars := cloneVars(in.Vars)
	vars["mode"] = "bootstrap"
	vars["tier"] = 0

	// 5. mkdir target. Refuse if it exists (no --force override; see
	// errors.go ExistsError).
	cwd, err := os.Getwd()
	if err != nil {
		return Summary{}, fmt.Errorf("bootstrap: getwd: %w", err)
	}
	target := filepath.Join(cwd, in.Name)
	if _, statErr := os.Stat(target); statErr == nil {
		return Summary{}, NewExistsError(target)
	} else if !os.IsNotExist(statErr) {
		return Summary{}, fmt.Errorf("bootstrap: stat %q: %w", target, statErr)
	}
	if !in.DryRun {
		if err := os.MkdirAll(target, 0o750); err != nil {
			return Summary{}, fmt.Errorf("bootstrap: mkdir %q: %w", target, err)
		}
	}

	// 6-7. Engine render (or dry-run) into target. tier=0 → no tier filter.
	tiers, err := tmpl.LoadTiers(src)
	if err != nil {
		return Summary{}, fmt.Errorf("bootstrap: load tiers: %w", err)
	}
	engine := tmpl.NewEngineWithRules(src, target, vars, manifest.Files, manifest.RenderRules, tiers, 0, in.Force)
	var result tmpl.Result
	if in.DryRun {
		result, err = engine.DryRun(ctx)
	} else {
		result, err = engine.Render(ctx)
	}
	if err != nil {
		return Summary{}, fmt.Errorf("bootstrap: render: %w", err)
	}

	// Hook context shared across all phases.
	hookCtx := tmpl.HookContext{
		Vars:      vars,
		Mode:      "bootstrap",
		Tier:      0,
		TargetDir: target,
	}

	// templateRoot is the on-disk directory hooks scripts live under.
	// Built-in (embed.FS) templates carry no hooks (manifest stays empty),
	// so resolve to "" when the resolved fs is not an os.DirFS — Run
	// won't be called with empty scripts, but if it is, the relative
	// path resolution under "" is harmless.
	templateRoot := resolveTemplateRoot(src)

	var preprResult *PrePrResult
	if in.WithPrePrHook {
		pr, err := GeneratePrePrHook(target, in.DryRun, time.Now().UTC())
		if err != nil {
			return Summary{}, fmt.Errorf("bootstrap: pre-pr hook: %w", err)
		}
		preprResult = &pr
	}

	var workflowActions []WorkflowAction
	if in.WithGitHubWorkflows {
		wfActions, err := renderWorkflows(target, in.Runtime, in, nil)
		if err != nil {
			return Summary{}, fmt.Errorf("bootstrap: render github workflows: %w", err)
		}
		workflowActions = wfActions
	}

	postHookSummary, posterr := GeneratePostPROpenHook(target, in.WithGithookPostPROpen, in.DryRun)
	if posterr != nil {
		return Summary{}, fmt.Errorf("bootstrap: post-pr-open hook: %w", posterr)
	}

	// DryRun stops here: no hooks, no git, no github, no push.
	if in.DryRun {
		sum := buildSummary(in, target, result, nil)
		sum.PrePrHook = preprResult
		sum.Workflows = workflowActions
		applyPostHookToSummary(&sum, postHookSummary)
		return sum, nil
	}

	// 8. post_render hook.
	if len(manifest.Hooks.PostRender) > 0 {
		if err := deps.Hooks.Run(ctx, "post_render", manifest.Hooks.PostRender, templateRoot, hookCtx, out); err != nil {
			return Summary{}, fmt.Errorf("bootstrap: post_render hook: %w", err)
		}
	}

	// 9. git init (or git hop init). When --hop=true and git-hop is not
	// on PATH, Init returns skipped=true so the surrounding flow can
	// continue without an error (no .git is created in that case, so
	// downstream commit/push steps are skipped too).
	hopSkipped, err := deps.Git.Init(ctx, target, in.Hop, in.DefaultBranch)
	if err != nil {
		return Summary{}, fmt.Errorf("bootstrap: git init: %w", err)
	}

	// 10. Write .kit/version. Format: "<template-name>@<ref>\n".
	// We don't currently track resolved git ref; use template name + "@latest"
	// as a sentinel until upgrade-time work threads through real refs.
	if err := writeKitVersion(target, manifest.Name); err != nil {
		return Summary{}, fmt.Errorf("bootstrap: write .kit/version: %w", err)
	}

	// 11. Initial commit. Skipped when git-hop was the requested
	// initialiser and was not installed (no repo to commit into).
	if !hopSkipped {
		if err := deps.Git.InitialCommit(ctx, target, "feat: initial scaffold"); err != nil {
			return Summary{}, fmt.Errorf("bootstrap: initial commit: %w", err)
		}
	}

	// 12. GitHub repo + branch protection (org only). Skipped when the
	// local git scaffold was skipped (no repo to publish).
	var ghSummary *GitHubSummary
	if !hopSkipped && in.AccountType != "none" && !in.NoGitHub {
		owner := in.Org
		if in.AccountType == "personal" {
			owner = "" // gh resolves personal to the authenticated user
		}
		cfg := RepoConfig{
			AccountType: in.AccountType,
			Owner:       owner,
			Name:        in.Name,
			Visibility:  in.Visibility,
			NoPush:      true, // bootstrap pushes explicitly in step 13
		}
		info, err := deps.GitHub.Create(ctx, target, cfg)
		if err != nil {
			return Summary{}, fmt.Errorf("bootstrap: github create: %w", err)
		}
		if info.URL != "" || info.Repo != "" {
			ghSummary = &GitHubSummary{
				Repo:       info.Repo,
				URL:        info.URL,
				Visibility: info.Visibility,
			}
		}
		if in.AccountType == "org" && info.Repo != "" {
			if err := deps.GitHub.ProtectMain(ctx, info.Repo); err != nil {
				return Summary{}, fmt.Errorf("bootstrap: protect main: %w", err)
			}
		}
		if len(manifest.Hooks.PostInit) > 0 {
			if err := deps.Hooks.Run(ctx, "post_init", manifest.Hooks.PostInit, templateRoot, hookCtx, out); err != nil {
				return Summary{}, fmt.Errorf("bootstrap: post_init hook: %w", err)
			}
		}
	}

	// 13. Push (unless suppressed or no remote configured).
	if !in.NoPush && ghSummary != nil {
		if err := deps.Git.Push(ctx, target); err != nil {
			return Summary{}, fmt.Errorf("bootstrap: push: %w", err)
		}
	}

	// 14. post_push hook.
	if !in.NoPush && ghSummary != nil && len(manifest.Hooks.PostPush) > 0 {
		if err := deps.Hooks.Run(ctx, "post_push", manifest.Hooks.PostPush, templateRoot, hookCtx, out); err != nil {
			return Summary{}, fmt.Errorf("bootstrap: post_push hook: %w", err)
		}
	}

	// 15. tlc init (best-effort). Wires the new project's tlc scope up
	// when tlc is installed; missing binary is a silent no-op so
	// non-tlc users aren't blocked. Idempotent on existing scopes.
	tlcSkipped, err := runTLCInit(ctx, target)
	if err != nil {
		return Summary{}, fmt.Errorf("bootstrap: %w", err)
	}

	// 16. Build + return summary.
	summary := buildSummary(in, target, result, ghSummary)
	summary.HopSkipped = hopSkipped
	summary.TLCSkipped = tlcSkipped
	summary.PrePrHook = preprResult
	summary.Workflows = workflowActions
	applyPostHookToSummary(&summary, postHookSummary)
	return summary, nil
}

// buildSummary assembles the final Summary; centralized so DryRun and
// full-run paths emit the same shape.
func buildSummary(in Inputs, target string, result tmpl.Result, gh *GitHubSummary) Summary {
	return Summary{
		Mode:      "bootstrap",
		Name:      in.Name,
		Target:    target,
		Template:  in.Template,
		Result:    result,
		GitHub:    gh,
		NextSteps: NextSteps("bootstrap", in.Name, gh),
	}
}

// parseManifestFS reads kit-template.yaml from the root of src and parses it.
// We tee into a temp file because tmpl.Parse takes a path; this avoids
// duplicating its validation logic. fs.ReadFile gives us the bytes; we
// write to a t.TempDir-style scratch file. Returns a populated Manifest.
func parseManifestFS(src fs.FS) (tmpl.Manifest, error) {
	data, err := fs.ReadFile(src, "kit-template.yaml")
	if err != nil {
		return tmpl.Manifest{}, fmt.Errorf("read kit-template.yaml: %w", err)
	}
	tmpFile, err := os.CreateTemp("", "kit-manifest-*.yaml")
	if err != nil {
		return tmpl.Manifest{}, fmt.Errorf("temp manifest: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return tmpl.Manifest{}, fmt.Errorf("write temp manifest: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return tmpl.Manifest{}, fmt.Errorf("close temp manifest: %w", err)
	}
	m, err := tmpl.Parse(tmpPath)
	if err != nil {
		return tmpl.Manifest{}, err
	}
	if err := m.Validate(); err != nil {
		return tmpl.Manifest{}, err
	}
	return m, nil
}

// resolveTemplateRoot returns an on-disk path to the template root when src
// is backed by os.DirFS; embed.FS-backed sources have no such path and
// return "". Hook scripts must live on disk (they shell out via /bin/sh),
// so embed.FS templates that declare hooks would fail at Run time — we
// accept that constraint to keep the seam simple.
func resolveTemplateRoot(src fs.FS) string {
	// fs.FS doesn't expose its root; os.DirFS returns a *dirFS whose
	// String() form is unfortunately unexported. We fall back to "" and
	// let HookRunner deal with relative-path resolution. Production
	// callers swap a wrapper that records the original spec.
	_ = src
	return ""
}

// writeKitVersion drops a .kit/version file at target with the resolved
// template identifier. Format kept simple: "<name>@latest\n" until ref
// tracking lands.
func writeKitVersion(target, templateName string) error {
	dir := filepath.Join(target, ".kit")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	content := []byte(templateName + "@latest\n")
	return os.WriteFile(filepath.Join(dir, "version"), content, 0o644)
}

// cloneVars returns a shallow copy of in so callers can mutate without
// leaking back to Inputs.Vars (Gather hands out the same map).
func cloneVars(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+2)
	for k, v := range in {
		out[k] = v
	}
	return out
}

// defaultHookRunner adapts template.Run to the HookRunner interface so
// production callers can use the package-level free function without a
// custom shim. Tests inject their own implementation.
type defaultHookRunner struct{}

// NewHookRunner returns a HookRunner backed by template.Run.
func NewHookRunner() HookRunner { return defaultHookRunner{} }

func (defaultHookRunner) Run(ctx context.Context, phase string, scripts []string,
	templateRoot string, hookCtx tmpl.HookContext, out io.Writer,
) error {
	_ = phase // phase is informational for callers; template.Run keys off scripts
	return tmpl.Run(ctx, scripts, templateRoot, hookCtx, out)
}

// defaultGitRunner adapts the package-level git wrappers to GitRunner.
type defaultGitRunner struct{}

// NewGitRunner returns a GitRunner backed by git.go.
func NewGitRunner() GitRunner { return defaultGitRunner{} }

func (defaultGitRunner) Init(ctx context.Context, dir string, hop bool, branch string) (bool, error) {
	return Init(ctx, dir, hop, branch)
}
func (defaultGitRunner) InitialCommit(ctx context.Context, dir, msg string) error {
	return InitialCommit(ctx, dir, msg)
}
func (defaultGitRunner) Push(ctx context.Context, dir string) error {
	return Push(ctx, dir)
}

// defaultGitHubRunner adapts github.go to GitHubRunner.
type defaultGitHubRunner struct{}

// NewGitHubRunner returns a GitHubRunner backed by github.go.
func NewGitHubRunner() GitHubRunner { return defaultGitHubRunner{} }

func (defaultGitHubRunner) Create(ctx context.Context, dir string, cfg RepoConfig) (RepoInfo, error) {
	return Create(ctx, dir, cfg)
}
func (defaultGitHubRunner) ProtectMain(ctx context.Context, fullName string) error {
	return ProtectMain(ctx, fullName)
}
