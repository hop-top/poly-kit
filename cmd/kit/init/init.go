// Package kitinit — init.go wires the cobra `kit init` command and
// orchestrates Detect → Gather → bootstrap/augment → output (spec §17).
//
// Flag parsing populates a local FlagSet whose pointer fields are nil
// until cmd.Flags().Changed(name) is true; this preserves the
// nil=unset semantics that inputs.Gather relies on for its precedence
// chain (T-0849).
//
// InitCmd accepts a nil *cli.Root so tests and embedding callers can
// drive the cobra command directly without constructing a full Root.
// See InitCmd's doc-comment for the fallback behavior (T-0229..T-0231).
package kitinit

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"hop.top/kit/go/console/cli"
	kitlog "hop.top/kit/go/console/log"
	"hop.top/kit/go/core/xdg"
	"hop.top/kit/go/runtime/sideeffect"
	tmpl "hop.top/kit/internal/template"
)

// InitCmd builds the `kit init` cobra command.
//
// root may be nil. Tests and downstream callers that drive the cobra
// command directly (without going through cli.New) pass nil to avoid
// constructing a full *cli.Root. In that case RunE falls back to a
// fresh, empty *viper.Viper for log configuration; root-bound features
// (theme, hints, auth, identity) are not used here so nothing else is
// degraded.
func InitCmd(root *cli.Root) *cobra.Command {
	// Locals captured by the closure; flag pointers are read from the
	// cobra FlagSet via Changed-aware ptrIfChanged below.
	var (
		fromFlag                     string
		moduleFlag                   string
		runtimeFlag                  []string
		tierFlag                     int
		modeFlag                     string
		accountTypeFlag              string
		orgFlag                      string
		visibilityFlag               string
		noGitHubFlag                 bool
		noPushFlag                   bool
		licenseFlag                  string
		hopFlag                      bool
		defaultBranchFlag            string
		authorFlag                   []string
		emailFlag                    string
		themeFlag                    string
		descriptionFlag              string
		dryRunFlag                   bool
		forceFlag                    bool
		yesFlag                      bool
		withPrePrHookFlag            bool
		withoutPrePrHookFlag         bool
		withGitHubWorkflowsFlag      bool
		withoutGitHubWorkflowsFlag   bool
		withGithookPostPROpenFlag    bool
		withoutGithookPostPROpenFlag bool
		withBusWorkflowsFlag         bool
		withoutBusWorkflowsFlag      bool

		// Managed-block refresh flags (T-0810). When any of these is
		// set, RunE short-circuits before the detect/Gather flow and
		// dispatches to RunManaged. See cmd/kit/init/managed.go.
		updateFlag        bool
		checkFlag         bool
		langsFlag         string
		addServiceFlag    string
		removeServiceFlag string
	)

	cmd := &cobra.Command{
		Use:     "init [name]",
		Aliases: []string{"i"},
		Args:    cobra.MaximumNArgs(1),
		Short:   "Bootstrap or augment a kit-powered CLI project",
		Long: "Detect whether the current directory is a fresh project " +
			"(bootstrap mode) or an existing kit project (augment mode), " +
			"gather inputs via flags or interactive wizard, and resolve " +
			"the chosen template (built-in, @org/name, git URL, or path). " +
			"--dry-run previews every file write without touching disk; " +
			"--yes skips the wizard for non-interactive runs.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			// Guard against nil root (documented as valid input — see
			// the InitCmd doc-comment). Fall back to a zero-value viper
			// so the logger respects defaults rather than panicking on
			// a nil deref of root.Viper.
			var vp *viper.Viper
			if root != nil {
				vp = root.Viper
			}
			if vp == nil {
				vp = viper.New()
			}
			logger := kitlog.New(vp)

			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("kit init: getwd: %w", err)
			}

			// T-0810 short-circuit: managed-block refresh / drift
			// check / service ops. These flags bypass the full
			// bootstrap/augment flow because their job is to operate
			// on an existing project's managed blocks only.
			if updateFlag || checkFlag || addServiceFlag != "" || removeServiceFlag != "" {
				return RunManaged(ctx, ManagedOptions{
					Cwd:           cwd,
					Langs:         langsFlag,
					Check:         checkFlag,
					AddService:    addServiceFlag,
					RemoveService: removeServiceFlag,
					Stdout:        os.Stdout,
					Stderr:        os.Stderr,
				})
			}

			// 1. Mode resolution. Translate detect → typed errors early
			// so callers get a hint-rich Error() (spec §19).
			override, err := parseModeOverride(modeFlag)
			if err != nil {
				return err
			}
			var positionalName string
			if len(args) > 0 {
				positionalName = args[0]
			}
			mode, version, err := DetectWithName(cwd, positionalName, override)
			if err != nil {
				return fmt.Errorf("kit init: detect: %w", err)
			}
			switch mode {
			case ModeAlreadyKit:
				return NewAlreadyKitError(version)
			case ModeBareWorktree:
				common, _ := runGitRevParse(cwd, "--git-common-dir")
				gitdir, _ := runGitRevParse(cwd, "--git-dir")
				return NewModeBareWorktreeError(common, gitdir)
			}

			// 2. Defaults: best-effort. Read errors degrade to empty;
			// surface as a warning so users can debug a malformed file.
			defaults, derr := Read()
			if derr != nil {
				logger.Warn("kit init: read defaults", "err", derr)
			}

			// 3. Registry — pre-resolve template + parse manifest so
			// Gather can fold manifest variables into Inputs.Vars.
			// bootstrap/augment re-resolve internally; the cost is
			// one extra cache-hit clone (built-ins resolve from embed
			// in O(µs) so duplication is negligible).
			cacheDir, cerr := xdg.CacheDir("kit")
			if cerr != nil {
				logger.Warn("kit init: cache dir", "err", cerr)
				cacheDir = ""
			}
			registry := tmpl.NewRegistry(defaults.TemplateRegistry, cacheDir)

			// Resolve template name precedence for the pre-parse:
			// flag > defaults > built-in default ("cli-go").
			tmplSpec := fromFlag
			if !cmd.Flags().Changed("from") {
				if defaults.Template != "" {
					tmplSpec = defaults.Template
				} else {
					tmplSpec = "cli-go"
				}
			}

			// Manifest pre-parse is best-effort — if it fails we hand
			// Gather an empty manifest and let bootstrap/augment surface
			// the resolution error with full context.
			var manifest tmpl.Manifest
			if src, rerr := registry.Resolve(ctx, tmplSpec); rerr == nil {
				if m, perr := parseManifestFS(src); perr == nil {
					manifest = m
				}
			}

			// 4. Build FlagSet with pointers only for changed flags.
			flagset := buildFlagSet(
				cmd,
				&fromFlag, &moduleFlag, runtimeFlag, &tierFlag, &modeFlag,
				&accountTypeFlag, &orgFlag, &visibilityFlag, &noGitHubFlag,
				&noPushFlag, &licenseFlag, &hopFlag, &defaultBranchFlag,
				authorFlag, &emailFlag, &themeFlag, &descriptionFlag,
				&dryRunFlag, &forceFlag, &yesFlag,
				&withGitHubWorkflowsFlag, &withoutGitHubWorkflowsFlag,
				&withPrePrHookFlag, &withoutPrePrHookFlag,
				&withGithookPostPROpenFlag, &withoutGithookPostPROpenFlag,
				&withBusWorkflowsFlag, &withoutBusWorkflowsFlag,
			)

			// 5. Wizard only spins up for interactive (non-yes) runs.
			var wizard Wizarder
			if !yesFlag {
				wizard = NewTTYWizard(os.Stdin, os.Stdout)
			}

			inputs, err := Gather(ctx, args, flagset, manifest, defaults, wizard)
			if err != nil {
				return err
			}
			inputs.Mode = mode
			// Pilot for ADR-0019: route the kit-global --dry-run flag
			// (sideeffect.IsDryRun ctx tag) into the existing
			// per-leaf in.DryRun field. The two flags compose: either
			// path enables dry-run; both enable dry-run too.
			if sideeffect.IsDryRun(ctx) {
				inputs.DryRun = true
			}
			// JSON-summary toggle reads from the kit-owned `--format`
			// global (parity contract §3.3): the deprecated --json
			// init-local flag was removed in favor of `--format json`.
			inputs.JSON = vp.GetString("format") == "json"

			// 6. Dispatch.
			deps := Deps{
				Registry: registry,
				Hooks:    NewHookRunner(),
				Git:      NewGitRunner(),
				GitHub:   NewGitHubRunner(),
				Output:   os.Stdout,
			}

			var summary Summary
			switch mode {
			case ModeBootstrap:
				summary, err = runBootstrap(ctx, deps, inputs)
			case ModeAugment:
				summary, err = runAugment(ctx, deps, inputs, cwd)
			default:
				return fmt.Errorf("kit init: unsupported mode %s", mode)
			}
			if err != nil {
				return err
			}

			// 7. Render summary.
			if inputs.JSON {
				return WriteJSON(os.Stdout, summary)
			}
			return WriteHuman(os.Stdout, summary)
		},
	}

	f := cmd.Flags()
	f.StringVar(&fromFlag, "from", "cli-go", "Template spec (built-in name, @org/name, git URL, or path)")
	f.StringVar(&moduleFlag, "module", "", "Go module path (defaults to github.com/<owner>/<name>)")
	f.StringSliceVar(&runtimeFlag, "runtime", []string{"go"}, "Runtimes to scaffold (go, ts, py, php, rs)")
	f.IntVar(&tierFlag, "tier", 4, "Augment tier (0-4)")
	f.StringVar(&modeFlag, "mode", "", "Mode override (bootstrap|augment); empty = auto-detect")
	f.StringVar(&accountTypeFlag, "account-type", "personal", "GitHub account type (personal|org|none)")
	f.StringVar(&orgFlag, "org", "", "GitHub organization (required when --account-type=org)")
	f.StringVar(&visibilityFlag, "visibility", "", "Repo visibility (public|private|internal); empty = per-account-type default")
	f.BoolVar(&noGitHubFlag, "no-github", false, "Skip GitHub repo creation")
	f.BoolVar(&noPushFlag, "no-push", false, "Skip initial push")
	f.StringVar(&licenseFlag, "license", "", "License id (empty = per-account-type default)")
	f.BoolVar(&hopFlag, "hop", true, "Use git hop for repo init")
	f.StringVar(&defaultBranchFlag, "default-branch", "main", "Default branch name")
	f.StringSliceVar(&authorFlag, "author", nil,
		"Copyright holder(s) for LICENSE files. Repeatable; each value may "+
			"contain ';'-delimited holders. Grammar: '<year-or-range> <holder>"+
			"[ <<URL>>]'. Bare names use the current year. Empty defaults to "+
			"the canonical 4-holder block.")
	f.StringVar(&emailFlag, "email", "", "Author email (defaults to git config user.email)")
	f.StringVar(&themeFlag, "theme", "daylight", "Theme")
	f.StringVar(&descriptionFlag, "description", "", "Project description")
	f.BoolVar(&dryRunFlag, "dry-run", false, "Show what would be written without touching disk")
	f.BoolVar(&forceFlag, "force", false, "Bypass non-destructive guards (does not overwrite existing files)")
	f.BoolVarP(&yesFlag, "yes", "y", false, "Non-interactive: skip wizard prompts")
	f.BoolVar(&withPrePrHookFlag, "with-githook-pre-pr", true,
		"Generate .githooks/pre-pr (lint/test/scratchpad gates)")
	f.BoolVar(&withoutPrePrHookFlag, "without-githook-pre-pr", false,
		"Skip .githooks/pre-pr generation (complement of --with-githook-pre-pr)")
	f.BoolVar(&withGitHubWorkflowsFlag, "with-github-workflows", true,
		"Generate .github/workflows/*-caller.yml stubs that use hop-top/.github reusable workflows")
	f.BoolVar(&withoutGitHubWorkflowsFlag, "without-github-workflows", false,
		"Skip .github/workflows/*-caller.yml generation (alias for --with-github-workflows=false)")
	f.BoolVar(&withGithookPostPROpenFlag, "with-githook-post-pr-open", true,
		"Generate .githooks/post-pr-open (after-PR-open hook)")
	f.BoolVar(&withoutGithookPostPROpenFlag, "without-githook-post-pr-open", false,
		"Skip generation of .githooks/post-pr-open (complement of --with-githook-post-pr-open)")
	f.BoolVar(&withBusWorkflowsFlag, "with-bus-workflows", false,
		"Render .github/workflows/kit-bus-*.yml PR-lifecycle bus workflows (opt-in)")
	f.BoolVar(&withoutBusWorkflowsFlag, "without-bus-workflows", false,
		"Skip rendering kit-bus PR-lifecycle workflows (no-op when already disabled)")

	// T-0810: managed-block refresh / drift / service ops. These
	// flags short-circuit the bootstrap/augment flow at the top of
	// RunE; see managed.go for the orchestration logic.
	f.BoolVar(&updateFlag, "update", false,
		"Refresh kit-managed blocks (mise.toml, devcontainer, compose, env) idempotently")
	f.BoolVar(&checkFlag, "check", false,
		"Exit non-zero if any kit-managed block drifted from the current manifest")
	f.StringVar(&langsFlag, "langs", "",
		"Comma-separated lang subset (go,ts,py,rs); empty = auto-detect from cwd")
	f.StringVar(&addServiceFlag, "add-service", "",
		"Append a curated service (postgres|redis|minio|mailpit|redpanda) to docker-compose")
	f.StringVar(&removeServiceFlag, "remove-service", "",
		"Inverse of --add-service")

	// kit init scaffolds new project trees (mkdir/write) and
	// augments existing ones — declare the side-effect tier per
	// cli-conventions §3.5. ADR-0020 drives --dry-run support off
	// this tier: write|destructive leaves accept --dry-run by
	// default, so the kit-global --dry-run reaches RunE without an
	// explicit opt-in. The pre-existing local --dry-run flag still
	// works; both flags compose to enable dry-run.
	cli.SetSideEffect(cmd, cli.SideEffectWrite)
	cli.SetIdempotency(cmd, cli.IdempotencyConditional)
	cli.SetTopLevelVerb(cmd)
	return cmd
}

// parseModeOverride converts the --mode flag value into a Mode. Empty
// string yields ModeUnset (auto-detect). Unknown values return an
// error so the user gets immediate feedback.
func parseModeOverride(s string) (Mode, error) {
	switch s {
	case "":
		return ModeUnset, nil
	case "bootstrap":
		return ModeBootstrap, nil
	case "augment":
		return ModeAugment, nil
	default:
		return ModeUnset, fmt.Errorf("kit init: invalid --mode %q (want bootstrap|augment)", s)
	}
}

// buildFlagSet wires per-flag pointers into a kitinit.FlagSet, leaving
// fields nil when the user did not supply the flag (cobra's Changed
// signal). This preserves the precedence semantics of inputs.Gather.
func buildFlagSet(
	cmd *cobra.Command,
	from, module *string,
	runtime []string,
	tier *int,
	mode *string,
	accountType, org, visibility *string,
	noGitHub *bool,
	noPush *bool,
	license *string,
	hop *bool,
	defaultBranch *string,
	author []string,
	email, theme, description *string,
	dryRun, force, yes *bool,
	withGitHubWorkflows, withoutGitHubWorkflows *bool,
	withPrePrHook, withoutPrePrHook *bool,
	withGithookPostPROpen, withoutGithookPostPROpen *bool,
	withBusWorkflows, withoutBusWorkflows *bool,
) *FlagSet {
	fs := &FlagSet{}
	c := cmd.Flags().Changed

	if c("from") {
		fs.Template = from
	}
	if c("module") {
		fs.Module = module
	}
	if c("runtime") {
		fs.Runtime = runtime
	}
	if c("tier") {
		fs.Tier = tier
	}
	if c("mode") {
		fs.ModeOverride = mode
	}
	if c("account-type") {
		fs.AccountType = accountType
	}
	if c("org") {
		fs.Org = org
	}
	if c("visibility") {
		fs.Visibility = visibility
	}
	if c("no-github") {
		fs.NoGitHub = noGitHub
	}
	if c("no-push") {
		fs.NoPush = noPush
	}
	if c("license") {
		fs.License = license
	}
	if c("hop") {
		fs.Hop = hop
	}
	if c("default-branch") {
		fs.DefaultBranch = defaultBranch
	}
	if c("author") {
		fs.Author = author
	}
	// FlagSet.AuthorChanged tracks "user supplied --author at all" so
	// the default 4-holder block kicks in only when the flag is absent.
	// Storing the bool (instead of relying on len(author)>0) lets an
	// explicit `--author=` (empty value) still distinguish from unset.
	fs.AuthorChanged = c("author")
	if c("email") {
		fs.Email = email
	}
	if c("theme") {
		fs.Theme = theme
	}
	if c("description") {
		fs.Description = description
	}
	if c("dry-run") {
		fs.DryRun = dryRun
	}
	if c("force") {
		fs.Force = force
	}
	if c("yes") {
		fs.Yes = yes
	}
	switch {
	case c("without-github-workflows") && *withoutGitHubWorkflows:
		v := false
		fs.WithGitHubWorkflows = &v
	case c("with-github-workflows"):
		v := *withGitHubWorkflows
		fs.WithGitHubWorkflows = &v
	}
	switch {
	case c("without-githook-pre-pr") && *withoutPrePrHook:
		v := false
		fs.WithPrePrHook = &v
	case c("with-githook-pre-pr"):
		v := *withPrePrHook
		fs.WithPrePrHook = &v
	}
	switch {
	case c("without-githook-post-pr-open") && *withoutGithookPostPROpen:
		v := false
		fs.WithGithookPostPROpen = &v
	case c("with-githook-post-pr-open"):
		v := *withGithookPostPROpen
		fs.WithGithookPostPROpen = &v
	}
	switch {
	case c("without-bus-workflows") && *withoutBusWorkflows:
		off := false
		fs.WithBusWorkflows = &off
	case c("with-bus-workflows"):
		fs.WithBusWorkflows = withBusWorkflows
	}
	return fs
}
