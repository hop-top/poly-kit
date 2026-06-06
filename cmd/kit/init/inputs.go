// Package kitinit — inputs.go resolves the final set of template variables
// for an init run via the precedence chain defined in spec §14.
//
// Highest priority first:
//  1. CLI flag value (FlagSet pointer fields — nil = unset)
//  2. Env var KIT_<UPPER_NAME>
//  3. defaults.yaml field (Defaults)
//  4. Manifest variable Default (text/template-rendered against vars
//     resolved so far — order matters; manifest authors list dependents
//     after their dependencies)
//  5. Built-in default (Author/Email from git config; Year, Date, License,
//     Visibility derived from AccountType)
//  6. Wizard prompt (only when interactive — i.e. !Yes — and the variable
//     is required and still missing)
//
// The Inputs return value mirrors the design-doc surface: scalar fields
// drive the orchestrator (bootstrap/augment), while Vars carries the full
// resolved map (built-ins + manifest variables) for template rendering.
package kitinit

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	tmpl "hop.top/kit/internal/template"
)

// Inputs is the resolved view of all init-run knobs.
type Inputs struct {
	Name    string
	Module  string
	License string
	// Author is the single-string projection of Copyrights[0].Holder
	// (or git config user.name when no --author was supplied) kept for
	// downstream templates that still reference {{.Author}}:
	// package.json, pyproject.toml, Cargo.toml, README.md, etc. The
	// authoritative multi-holder list lives on Copyrights.
	Author        string
	Copyrights    []Copyright
	Email         string
	Org           string
	AccountType   string
	Visibility    string
	Theme         string
	Template      string
	Description   string
	DefaultBranch string

	Runtime []string
	Tier    int

	Hop      bool
	NoGitHub bool
	NoPush   bool
	DryRun   bool
	JSON     bool
	Force    bool
	Yes      bool

	WithPrePrHook         bool
	WithGitHubWorkflows   bool
	WithGithookPostPROpen bool
	WithBusWorkflows      bool

	Mode Mode // populated by caller from detect.Detect

	// Vars carries the union of built-in vars and resolved manifest
	// variables. Used to drive template rendering downstream.
	Vars map[string]any
}

// FlagSet mirrors the init cobra flag surface. Pointer fields distinguish
// "user supplied" (non-nil) from "left at default" (nil) so the precedence
// chain treats unset flags as transparent. Bool/int flags use *bool/*int
// for the same reason; string slices use nil-vs-non-nil.
type FlagSet struct {
	Name    *string
	Module  *string
	License *string
	// Author is the repeatable --author flag value list. Empty/nil means
	// the user didn't pass --author at all and the default copyright
	// block should be synthesized downstream. Each value may contain
	// ;-delimited holder chunks; see ParseCopyrights.
	Author []string
	// AuthorChanged tracks whether the --author flag was supplied at
	// all (cobra.Flag.Changed) so an explicit empty value can be
	// distinguished from "user left it alone". Wired by buildFlagSet.
	AuthorChanged bool
	Email         *string
	Org           *string
	AccountType   *string
	Visibility    *string
	Theme         *string
	Template      *string // --from
	Description   *string
	DefaultBranch *string

	Runtime []string // nil = unset
	Tier    *int

	Hop      *bool
	NoGitHub *bool
	NoPush   *bool
	DryRun   *bool
	Force    *bool
	Yes      *bool

	WithGitHubWorkflows   *bool
	WithPrePrHook         *bool
	WithGithookPostPROpen *bool
	WithBusWorkflows      *bool

	ModeOverride *string // --mode flag value before parsing
}

// Gather resolves Inputs by walking the precedence chain (spec §14).
//
// Behavior:
//   - args: positional args; args[0] (when present) becomes the project Name
//     unless --name overrides. When neither is set, KIT_NAME and then
//     basename(cwd) close the chain so augment-mode runs that omit a
//     positional name still get a non-empty Name before the manifest
//     required-var loop runs (T-0411).
//   - The first scalar resolution pass populates the orchestrator-facing
//     Inputs fields and seeds the Vars map with built-ins.
//   - The second pass walks manifest.Variables in declaration order so
//     each variable's text/template Default can reference vars resolved
//     earlier in the same run.
//   - --account-type=org with empty Org returns ErrOrgRequired.
//   - --yes (Inputs.Yes==true) suppresses the wizard; missing required
//     manifest vars then return ErrMissingRequired.
func Gather(
	ctx context.Context,
	args []string,
	flags *FlagSet,
	manifest tmpl.Manifest,
	defaults Defaults,
	wizard Wizarder,
) (Inputs, error) {
	if flags == nil {
		flags = &FlagSet{}
	}

	in := Inputs{Vars: map[string]any{}}

	// Bool/int flags are pure flag→default fallthrough; no env or
	// defaults.yaml hook for these (they are session knobs, not template
	// vars). Capture them up front.
	in.Yes = derefBool(flags.Yes, false)
	in.DryRun = derefBool(flags.DryRun, false)
	// Inputs.JSON is now populated by the caller from viper("format")
	// after Gather returns (parity contract §3.3 — the init-local
	// --json flag was removed in favor of `--format json`).
	in.Force = derefBool(flags.Force, false)
	in.NoGitHub = derefBool(flags.NoGitHub, false)
	in.NoPush = derefBool(flags.NoPush, false)
	// Bus event workflows are opt-in (spec §8): nil pointer →
	// default false. The --without-bus-workflows complement is a
	// no-op when the default is already false; we still parse it
	// for symmetry with the other --with/--without pairs.
	in.WithBusWorkflows = derefBool(flags.WithBusWorkflows, false)
	// Hop default is true (spec §17). Precedence: flag > defaults.yaml >
	// built-in. Both flag and defaults.Hop are *bool so unset (nil) falls
	// through to the next layer; explicit false is honored.
	switch {
	case flags.Hop != nil:
		in.Hop = *flags.Hop
	case defaults.Hop != nil:
		in.Hop = *defaults.Hop
	default:
		in.Hop = true
	}
	in.Tier = derefInt(flags.Tier, 4)
	in.WithGitHubWorkflows = derefBool(flags.WithGitHubWorkflows, true)
	in.WithPrePrHook = derefBool(flags.WithPrePrHook, true)
	in.WithGithookPostPROpen = derefBool(flags.WithGithookPostPROpen, true)

	// Name: walk the full precedence chain here (instead of leaving it to
	// the manifest required-var loop below) so the orchestrator-facing
	// scalar in.Name and the rendered vars["Name"] share a single source
	// of truth. Without this, augment-mode's basename fallback ran AFTER
	// Gather and diverged from vars["Name"] populated via KIT_NAME (T-0411
	// problem B), or never ran at all because the manifest's required
	// check fired first under --yes (T-0411 problem A).
	//
	// Order: --name flag > positional arg > KIT_NAME env > basename(cwd).
	// basename is a universally-safe last resort: augment treats cwd as
	// the project; bootstrap re-validates Name against nameRegex so a
	// non-conforming basename still surfaces InvalidNameError.
	switch {
	case flags.Name != nil:
		in.Name = *flags.Name
	case len(args) > 0:
		in.Name = args[0]
	case os.Getenv("KIT_NAME") != "":
		in.Name = os.Getenv("KIT_NAME")
	default:
		if cwd, err := os.Getwd(); err == nil {
			in.Name = filepath.Base(cwd)
		}
	}

	// Account-related fields drive built-ins (visibility, license),
	// so resolve them first via the standard chain (flag > env > defaults).
	in.AccountType = resolveScalar("account_type", flags.AccountType, defaults.AccountType, "personal")
	in.Org = resolveScalar("org", flags.Org, defaults.Org, "")

	// Now apply scalar precedence to the rest. Built-in fallbacks are
	// computed once dependencies (AccountType, Author/Name) are available.
	//
	// Author/Copyrights precedence (see ADR + track plan):
	//   1. --author (repeatable; ;-delimited within a value) → parsed into Copyrights
	//   2. KIT_AUTHOR env (single legacy holder)
	//   3. defaults.yaml author field (single legacy holder)
	//   4. git config user.name (single legacy holder)
	//   5. canonical 4-holder default block (only when nothing above applies)
	//
	// The single-string in.Author is kept as a derived value for the
	// templates that still reference {{.Author}} (README.md, package.json,
	// composer.json, Cargo.toml, pyproject.toml). LICENSE templates
	// consume {{.Copyrights}} directly.
	now := time.Now()
	year := now.Year()
	switch {
	case flags.AuthorChanged:
		cps, err := ParseCopyrights(flags.Author, year)
		if err != nil {
			return Inputs{}, err
		}
		in.Copyrights = cps
	case os.Getenv("KIT_AUTHOR") != "":
		cps, err := ParseCopyrights([]string{os.Getenv("KIT_AUTHOR")}, year)
		if err != nil {
			return Inputs{}, err
		}
		in.Copyrights = cps
	case defaults.Author != "":
		cps, err := ParseCopyrights([]string{defaults.Author}, year)
		if err != nil {
			return Inputs{}, err
		}
		in.Copyrights = cps
	default:
		if gn := gitConfig("user.name"); gn != "" {
			cps, perr := ParseCopyrights([]string{gn}, year)
			if perr == nil {
				in.Copyrights = cps
			}
		}
		if len(in.Copyrights) == 0 {
			in.Copyrights = DefaultCopyrights(year)
		}
	}
	// Derive the single-string Author projection for legacy templates.
	if len(in.Copyrights) > 0 {
		in.Author = in.Copyrights[0].Holder
	}

	in.Email = resolveScalar("email", flags.Email, defaults.Email, "")
	if in.Email == "" {
		in.Email = gitConfig("user.email")
	}

	in.License = resolveScalar("license", flags.License, defaults.License, "")
	if in.License == "" {
		in.License = builtinLicense(in.AccountType)
	}

	in.Visibility = resolveScalar("visibility", flags.Visibility, defaults.Visibility, "")
	if in.Visibility == "" {
		in.Visibility = builtinVisibility(in.AccountType)
	}

	in.Theme = resolveScalar("theme", flags.Theme, defaults.Theme, "daylight")
	in.Template = resolveScalar("template", flags.Template, defaults.Template, "cli-go")
	in.Description = resolveScalar("description", flags.Description, "", "")
	in.DefaultBranch = resolveScalar("default_branch", flags.DefaultBranch, "", "main")

	// Module default: github.com/<owner>/<name> when both pieces are known.
	in.Module = resolveScalar("module", flags.Module, "", "")
	if in.Module == "" && in.Name != "" {
		owner := in.Org
		if owner == "" {
			owner = strings.ToLower(in.Author)
			owner = strings.ReplaceAll(owner, " ", "-")
		}
		if owner != "" {
			in.Module = fmt.Sprintf("github.com/%s/%s", owner, in.Name)
		}
	}

	// Runtime: nil flag → defaults.Runtime → ["go"].
	switch {
	case flags.Runtime != nil:
		in.Runtime = flags.Runtime
	case len(defaults.Runtime) > 0:
		in.Runtime = defaults.Runtime
	default:
		in.Runtime = []string{"go"}
	}

	// Validation: account-type=org requires --org.
	if in.AccountType == "org" && in.Org == "" {
		return Inputs{}, NewOrgRequiredError()
	}

	// Seed Vars with built-ins. Mirror each key under both lowercase
	// (kit convention) and PascalCase (Go text/template convention used
	// by built-in templates: {{.Name}}, {{.Module}}, …). Templates and
	// manifest defaults can reference either casing.
	setVar(in.Vars, "Name", in.Name)
	setVar(in.Vars, "Module", in.Module)
	setVar(in.Vars, "License", in.License)
	setVar(in.Vars, "Author", in.Author)
	setVar(in.Vars, "Copyrights", in.Copyrights)
	setVar(in.Vars, "Email", in.Email)
	setVar(in.Vars, "Org", in.Org)
	setVar(in.Vars, "AccountType", in.AccountType)
	setVar(in.Vars, "Visibility", in.Visibility)
	setVar(in.Vars, "Theme", in.Theme)
	setVar(in.Vars, "Template", in.Template)
	setVar(in.Vars, "Description", in.Description)
	setVar(in.Vars, "DefaultBranch", in.DefaultBranch)
	setVar(in.Vars, "Runtime", in.Runtime)
	setVar(in.Vars, "Tier", in.Tier)
	setVar(in.Vars, "Year", year)
	setVar(in.Vars, "Date", now.Format("2006-01-02"))

	// Manifest variables: walk in declaration order so Default templates
	// can interpolate earlier vars. Required + missing → wizard or
	// ErrMissingRequired under --yes.
	for _, v := range manifest.Variables {
		key := v.Name
		if val, ok := lookupResolved(in.Vars, key); ok && val != "" {
			// Already resolved by a built-in or earlier pass.
			continue
		}

		// Layer 2: env.
		if env := os.Getenv("KIT_" + strings.ToUpper(key)); env != "" {
			setVar(in.Vars, key, env)
			continue
		}

		// Layer 4: manifest Default (rendered against vars known so far).
		if v.Default != "" {
			rendered, err := renderTemplate(v.Default, in.Vars)
			if err != nil {
				return Inputs{}, fmt.Errorf("inputs: render default for %q: %w", key, err)
			}
			if rendered != "" {
				setVar(in.Vars, key, rendered)
				continue
			}
		}

		// Layer 6: wizard (interactive only, when required).
		if v.Required {
			if in.Yes || wizard == nil {
				return Inputs{}, NewMissingRequiredError(key)
			}
			ans, err := wizard.Ask(key, promptOf(v), "", v.Choices)
			if err != nil {
				return Inputs{}, fmt.Errorf("inputs: prompt %q: %w", key, err)
			}
			if ans == "" {
				return Inputs{}, NewMissingRequiredError(key)
			}
			setVar(in.Vars, key, ans)
			continue
		}

		// Optional + unresolved: leave unset so templates see the zero value.
	}

	_ = ctx // reserved for future cancellable resolution paths
	return in, nil
}

// resolveScalar walks layers 1-3 of the precedence chain for a single
// scalar var: flag (non-nil) → KIT_<UPPER> env → defaults.yaml field →
// builtin fallback. Returns "" if all layers empty.
func resolveScalar(name string, flag *string, defVal, builtin string) string {
	if flag != nil {
		return *flag
	}
	if env := os.Getenv("KIT_" + strings.ToUpper(name)); env != "" {
		return env
	}
	if defVal != "" {
		return defVal
	}
	return builtin
}

func derefBool(p *bool, fallback bool) bool {
	if p == nil {
		return fallback
	}
	return *p
}

func derefInt(p *int, fallback int) int {
	if p == nil {
		return fallback
	}
	return *p
}

// builtinLicense maps account-type to its conventional license. Spec §14:
// personal → MIT; org → Apache-2.0; none → MIT (treated as personal).
func builtinLicense(accountType string) string {
	switch accountType {
	case "org":
		return "Apache-2.0"
	default:
		return "MIT"
	}
}

// builtinVisibility maps account-type to its conventional visibility.
// personal → private; org → private; none → "" (no GitHub repo).
// Private-by-default keeps newly scaffolded repos opt-in for any wider
// visibility — callers can override via --visibility=public/internal.
func builtinVisibility(accountType string) string {
	switch accountType {
	case "none":
		return ""
	default:
		return "private"
	}
}

// gitConfig returns the requested git config value or "" on any error
// (missing git, unset key, non-zero exit). Used as a non-fatal built-in
// fallback for Author/Email.
func gitConfig(key string) string {
	cmd := exec.Command("git", "config", "--get", key)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// renderTemplate executes the given text/template against vars. Empty
// input returns "" without parsing. Errors propagate so callers can wrap
// with the variable name.
func renderTemplate(text string, vars map[string]any) (string, error) {
	if text == "" {
		return "", nil
	}
	t, err := template.New("default").Option("missingkey=zero").Parse(text)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, vars); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// lookupResolved returns the string form of vars[key] when present and
// non-zero. Non-string values stringify via fmt.Sprintf("%v", …).
//
// Lookup is case-insensitive: built-in vars are seeded lowercase (name,
// module, …) but manifests may declare PascalCase variables (Name,
// Module, …). A direct hit is preferred; only on miss do we case-fold
// the entire map.
func lookupResolved(vars map[string]any, key string) (string, bool) {
	if v, ok := vars[key]; ok {
		return stringify(v)
	}
	lower := strings.ToLower(key)
	for k, v := range vars {
		if strings.ToLower(k) == lower {
			return stringify(v)
		}
	}
	return "", false
}

// setVar writes val under both the supplied key and its lowercase form.
// Built-in templates use Go text/template's PascalCase convention
// ({{.Name}}); kit convention is lowercase ({{.name}}). Mirroring keeps
// both lookups O(1) and avoids per-call case-folding downstream.
func setVar(vars map[string]any, key string, val any) {
	vars[key] = val
	if lower := strings.ToLower(key); lower != key {
		vars[lower] = val
	}
}

func stringify(v any) (string, bool) {
	switch s := v.(type) {
	case string:
		return s, s != ""
	case nil:
		return "", false
	default:
		str := fmt.Sprintf("%v", s)
		return str, str != ""
	}
}

// promptOf returns the user-facing prompt string for a manifest variable,
// falling back to "<Name>" when no explicit prompt is set.
func promptOf(v tmpl.Variable) string {
	if v.Prompt != "" {
		return v.Prompt
	}
	return v.Name
}
