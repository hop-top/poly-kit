package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"hop.top/kit/contracts/parity"
	"hop.top/kit/go/console/cli/idemstore"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/console/progress"
	configoverrides "hop.top/kit/go/core/config"
	"hop.top/kit/go/core/identity"
	"hop.top/kit/go/runtime/peer"
)

// Disable controls which built-in global flags are suppressed.
// Zero value enables all built-ins.
type Disable struct {
	Format   bool // suppress --format flag
	Quiet    bool // suppress --quiet flag
	NoColor  bool // suppress --no-color flag
	Hints    bool // suppress --no-hints flag
	Chdir    bool // suppress -C/--chdir flag
	Progress bool // suppress --progress-format flag
	Config   bool // suppress -c/--config <key=value> override flag
	DryRun   bool // suppress global --dry-run flag (default registered)
}

// Flag defines a tool-specific global persistent flag.
type Flag struct {
	Name    string // long name without -- (e.g. "verbose")
	Short   string // single char, optional (e.g. "v")
	Usage   string // description shown in --help
	Default string // string default; empty = no default

	// Optional value-pointer destinations. If non-nil, the flag value is
	// bound to the pointer in addition to the viper instance, so adopters
	// can read the parsed value without going through viper. At most one
	// of StringVar/BoolVar/IntVar should be set; if more than one is
	// non-nil the first match (string → bool → int) wins. When all are
	// nil the flag falls back to string-only registration.
	StringVar *string
	BoolVar   *bool
	IntVar    *int
}

// Hooks bundles optional hook functions that compose into the cobra root's
// PersistentPreRunE chain. Adopters use these slots instead of assigning to
// r.Cmd.PersistentPreRunE directly, which would silently overwrite kit's
// built-in chain (chdir → identity → peer init).
type Hooks struct {
	// PrePersistentRunE runs after kit's built-in chain (chdir →
	// identity → peer init) and before subcommand RunE. If it returns
	// an error the command short-circuits.
	PrePersistentRunE func(cmd *cobra.Command, args []string) error
}

// GroupConfig defines a command group for the root help layout.
type GroupConfig struct {
	ID     string // cobra group ID (e.g. "management")
	Title  string // section header (e.g. "MANAGEMENT")
	Hidden bool   // hidden from default --help; shown with --help-all
}

// HelpConfig controls the root --help output layout.
// Zero value uses kit defaults loaded from contracts/parity/parity.json.
type HelpConfig struct {
	// Disclaimer is appended to Short as the Long description when non-empty.
	// Empty = no disclaimer block.
	Disclaimer string
	// SectionOrder overrides the section rendering order (e.g. ["commands","options"]).
	// Empty = use parity.json default.
	SectionOrder []string
	// ShowAliases displays command aliases in help output (e.g. "deploy  d, dp").
	// Default false — aliases work for dispatch but are hidden from help.
	ShowAliases bool
	// Groups registers additional command groups beyond the built-in
	// "COMMANDS" (default, GroupID="") and "MANAGEMENT" (GroupID="management", hidden).
	Groups []GroupConfig
}

// ValidationFailureMode selects how cli.New / Execute report a
// Root.Validate failure when Config.EnforceValidate=true.
type ValidationFailureMode string

const (
	// ValidationFailureExit writes the error to stderr and calls
	// os.Exit(int(ExitUsage))=2.
	ValidationFailureExit ValidationFailureMode = ""
	// ValidationFailureError causes Execute to return the
	// *ValidationError to its caller. The cli.NewE constructor
	// runs Validate at construction time and returns the same
	// error so adopters who own their dispatch loop (e.g. embed
	// kit inside a larger CLI) can decide how to surface it.
	ValidationFailureError ValidationFailureMode = "error"
	// ValidationFailurePanic emits a panic with the validation
	// error as the value. Useful for debugging registration-order
	// issues where a stack trace pinpoints the offending caller.
	ValidationFailurePanic ValidationFailureMode = "panic"
	// ValidationFailureSilent logs the failure via slog and
	// continues. Discouraged outside recovery-mode tooling that
	// must boot even with a misconfigured tree.
	ValidationFailureSilent ValidationFailureMode = "silent"
)

// SignatureStrictness gates the signature-validator checks
// (local-globals, reserved-name, depth-hierarchical, passthrough).
// Independent of EnforceValidate, which gates the Layer-A annotation
// checks. Three modes:
//
//	silent  — checks run but produce no diagnostics. Default zero
//	          value so adopters who don't yet know about
//	          signature validation get a silent gradual rollout.
//	warn    — checks run; violations log via slog at Warn level.
//	          Execute() still proceeds.
//	reject  — checks run; violations cause Execute() to abort via
//	          the configured ValidationFailureMode (Exit / Error /
//	          Panic / Silent).
type SignatureStrictness string

const (
	// SignatureStrictnessSilent is the zero value. Checks still run
	// but no diagnostics are emitted. Used for callers that want to
	// build a SignatureReport on demand (via Root.ValidateSignature)
	// without affecting Execute()'s control flow.
	SignatureStrictnessSilent SignatureStrictness = ""
	// SignatureStrictnessWarn logs every violation via slog at Warn
	// level and lets Execute() proceed. Recommended initial value
	// for adopters mid-migration.
	SignatureStrictnessWarn SignatureStrictness = "warn"
	// SignatureStrictnessReject treats any violation as a hard
	// failure and dispatches via ValidationFailureMode. Use once
	// the tree is clean.
	SignatureStrictnessReject SignatureStrictness = "reject"
)

// Config holds the tool identity for root command construction.
type Config struct {
	// Name is the binary name as invoked by the user (e.g. "mytool").
	Name string
	// Version is the semver string printed by --version (e.g. "1.2.3").
	Version string
	// Short is the one-line description shown in help output.
	Short string
	// Accent is an optional hex color string (e.g. "#FF0000") used as the
	// theme accent. Zero value falls back to CharmTone Charple. Ignored when
	// Palette is non-zero.
	Accent string
	// Palette overrides the entire brand color pair (Command + Flag) used by
	// the theme. Zero value uses Neon (or Accent-tinted Neon if Accent is set).
	// Pass Bauhaus, Dark, or any custom Palette here.
	Palette Palette
	// Disable opts out of specific built-in global flags. Zero value enables all.
	Disable Disable
	// Globals registers extra persistent flags on the root command.
	Globals []Flag
	// Help controls root --help layout. Zero value uses parity.json defaults.
	Help HelpConfig
	// ChdirResolver is called when -C <target> is not an existing
	// directory. Tools implement shortname/registry/fuzzy lookup
	// here. nil = path-only (target must be a dir or error).
	ChdirResolver func(target string) (dir string, err error)
	// Hooks registers additive hook functions that compose into kit's
	// PersistentPreRunE chain. See Hooks for available slots.
	Hooks Hooks
	// EnforceValidate enables the side-effect + Layer-A annotation
	// pre-flight check at Execute(). Default true. Adopters who want
	// to opt out (negative tests, fuzz harnesses, embedded use-cases)
	// set DisableValidate=true on the Config; setting EnforceValidate
	// explicitly to false has no effect because cli.New flips it
	// back to true when it equals the zero value AND DisableValidate
	// is false.
	//
	// When true (default), Execute() calls Root.Validate() and
	// dispatches to ValidationFailureMode on any leaf missing
	// required annotations or shape rules. When DisableValidate is
	// true, Execute() skips the pre-flight; callers can still
	// invoke Root.Validate() explicitly to opt into the check at
	// any point (e.g. via kitconformance.AssertCLI in unit tests).
	EnforceValidate bool
	// DisableValidate is the explicit opt-out for adopters who do
	// not want the Layer-A enforcement at boot. Cf. EnforceValidate
	// above; the two are wired so cli.New only flips EnforceValidate
	// when DisableValidate is false.
	DisableValidate bool
	// ValidationFailureMode picks how Execute() reports a
	// validation failure. Zero value is ValidationFailureExit
	// (stderr + os.Exit(int(ExitUsage))=2 — current shipped
	// behavior). ValidationFailureError causes Execute to return
	// the *ValidationError to its caller (use cli.NewE for the
	// constructor-time variant). ValidationFailurePanic emits a
	// stack trace; useful when debugging registration-order
	// issues. ValidationFailureSilent logs the error via slog
	// and continues — discouraged outside recovery-mode tooling.
	ValidationFailureMode ValidationFailureMode
	// EnforceDryRunRationale, when true, rejects opted-out --dry-run
	// on write|destructive leaves that lack the kit/dry-run-rationale
	// annotation. Default false at 0.1.0-alpha.0; flips on its own
	// follow-up track once kit-internal sweeps complete.
	EnforceDryRunRationale bool
	// EnforceDestructiveToken, when true, rejects destructive leaves
	// that have not opted into the typed-token confirmation flow via
	// SetDestructiveToken / kit/destructive-token=required.
	EnforceDestructiveToken bool
	// EnforceGuidance, when true, rejects runnable leaves that have
	// no kit/examples, and non-read leaves that have no
	// kit/next-steps. Default false; flips per follow-up track.
	EnforceGuidance bool
	// QuietBootWarnings silences the soft stderr warnings emitted at
	// cli.New for guidance/examples/next-steps absence. Useful for
	// adopters mid-migration that don't want CI noise.
	QuietBootWarnings bool
	// MaxGuidanceBytes caps the encoded byte size of kit/examples and
	// kit/next-steps annotation payloads. 0 selects the package
	// default (16 KiB).
	MaxGuidanceBytes int
	// MaxTopLevelVerbs caps the count of depth-1 leaves annotated
	// kit/top-level-verb before the validator reports an error on
	// the root. 0 selects the package default (10).
	MaxTopLevelVerbs int
	// MaxHierarchyDepth caps how deep the command tree may nest
	// (root counted as 0). 0 selects the package default (3);
	// values above the hard cap (5) are clamped.
	MaxHierarchyDepth int
	// PassthroughStrictness controls validator behavior for commands
	// annotated kit/passthrough. "warn" (default) emits stderr at
	// boot; "reject" treats the annotation as a hard error;
	// "silent" suppresses both. See PassthroughWarn / PassthroughReject
	// / PassthroughSilent constants.
	PassthroughStrictness string
	// SignatureStrictness gates the signature-validator checks
	// (local-globals, reserved-name, depth-hierarchical, passthrough).
	// Independent of EnforceValidate. Zero value (silent) is the
	// no-op default so adopters who don't yet know about signature
	// validation see no change. "warn" logs via slog and continues;
	// "reject" routes violations through ValidationFailureMode. See
	// the SignatureStrictness constants for the documented values.
	SignatureStrictness SignatureStrictness
	// ProjectMarker is the relative path inside a project root that
	// holds the project's config file. When set, a bare-directory
	// -c/--config token resolves to <dir>/<ProjectMarker> instead of
	// erroring. Example: ".rlz/config.yaml" lets adopters say
	// `tool -c /path/to/project` and have it pick up
	// /path/to/project/.rlz/config.yaml. Empty disables the
	// resolution and preserves the historical "directory -> error"
	// behavior. The marker file MUST exist under the supplied
	// directory; a missing marker is a clear ConfigArgs error
	// surfaced via Root.Validate(), not a silent skip.
	ProjectMarker string
}

// Root wraps the cobra root command, viper instance, theme, and hint
// registry.
type Root struct {
	// Cmd is the configured cobra root command. Add subcommands to it,
	// then call Execute(ctx) to run the CLI.
	Cmd *cobra.Command
	// Viper is the viper instance. Flags not suppressed by Config.Disable are
	// bound here. Subcommands should check Config.Disable before reading keys.
	Viper *viper.Viper
	// Config is the identity provided to New; retained for subcommands
	// that need the tool name or version at runtime.
	Config Config
	// Theme holds semantic colors and styles built from CharmTone +
	// the optional accent.
	Theme Theme
	// Hints is the per-command hint registry. Commands register
	// next-step hints here; the output pipeline renders them after
	// primary output when enabled.
	Hints *output.HintSet
	// Streams enforces stdout=data, stderr=human convention.
	Streams *StreamWriter
	// Auth provides credential introspection. Defaults to NoAuth.
	Auth AuthIntrospector
	// Identity holds the resolved keypair when WithIdentity is used.
	// Nil when identity management is not enabled.
	Identity *identity.Keypair
	// Mesh holds the peer mesh when WithPeers is used.
	// Nil when peer management is not enabled.
	Mesh *peer.Mesh
	// PeerRegistry holds the peer registry when WithPeers is used.
	PeerRegistry *peer.Registry
	// PeerTrust holds the trust manager when WithPeers is used.
	PeerTrust *peer.TrustManager
	// IdemStore is the idempotency-key replay backend used by the
	// RunE middleware. nil disables replay (default); adopters wire
	// one up via WithIdempotencyStore. Each tool MUST own its own
	// store — cross-tool replay is out of scope by spec (§8.5).
	IdemStore idemstore.Store

	// policyLoader is the loader for named --policy=<name> resolution
	// (§8.6). nil means policy-file support is not wired; --confirm
	// and --max-ops still work without it.
	policyLoader       PolicyLoader
	apiCfg             *APIConfig
	identityCfg        *IdentityConfig
	peerCfg            *PeerConfig
	verboseCount       int // -V count; 0=info, 1=debug, 2+=trace
	aliases            map[string]string
	aliasCompletionSet bool              // guards single ValidArgsFunction wrap
	hiddenGroups       map[string]bool   // group IDs hidden from default --help
	groupTitles        map[string]string // group ID → display title
	// hiddenDefaultFlags lists root persistent flags that are kit-owned
	// global plumbing (--config, --chdir, --dry-run, etc.). They are
	// marked Hidden=true at registration to keep --help focused on the
	// cross-language parity contract, then revealed by applyGroupVisibility
	// when --help-all is on the args.
	hiddenDefaultFlags []string
	overrideArgs       []string // captured from SetArgs for pre-parse inspection
	// configArgsErr stashes the most recent ParseConfigArgs failure
	// from ConfigArgs/ConfigOverrides so Root.Validate() can surface
	// it in the EnforceValidate pre-flight. Cleared on a successful
	// parse. Direct callers of ConfigArgs also get the error in the
	// return value.
	configArgsErr error
	// reservedSubcommands is the set of subcommand names mounted by
	// kit-shipped factories (via cli.With* opts) or by legacy
	// Register*Command shims that call MarkReserved. Populated by
	// reservedSnapshot() immediately after cli.New's functional
	// opts run; queried via IsReserved during shape validation.
	reservedSubcommands map[string]struct{}
	// statusConfig is the StatusConfig from WithStatus (§4).
	// Zero-value when WithStatus was not used.
	statusConfig StatusConfig
	// statusProviders is the registered provider map. Populated by
	// WithStatus (defaults) and RegisterStatusProvider (adopter
	// overrides). nil until WithStatus runs or an adopter calls
	// RegisterStatusProvider directly.
	statusProviders   map[string]StatusProvider
	statusProvidersMu sync.Mutex
}

// New returns a Root pre-configured to the hop-top CLI contract:
//   - no help subcommand (only -h/--help flag)
//   - completion subcommand in management group (hidden from default --help)
//   - version handled by fang (-v/--version)
//   - persistent global flags: --quiet, --no-color, -C/--chdir
//   - styled help/errors via fang
//
// Config.EnforceValidate defaults to true. Opt out by setting
// DisableValidate=true (negative tests, fuzz harnesses). Cf. cli.NewE
// for the variant that runs Validate at construction time and returns
// the error to the caller.
func New(cfg Config, opts ...func(*Root)) *Root {
	if !cfg.DisableValidate {
		cfg.EnforceValidate = true
	}
	v := viper.New()

	long := cfg.Short
	if cfg.Help.Disclaimer != "" {
		long += "\n\n" + cfg.Help.Disclaimer
	}

	cmd := &cobra.Command{
		Use:          cfg.Name,
		Short:        cfg.Short,
		Long:         long,
		SilenceUsage: true,
		Args:         cobra.NoArgs,
	}

	// Override cobra's default version template to print "<name> v<version>"
	// instead of "<name> version <version>".
	cmd.SetVersionTemplate(
		`{{with .DisplayName}}{{printf "%s " .}}{{end}}{{printf "v%s" .Version}}` + "\n")

	// Normalize: strip leading "v" so the template's "v%s" doesn't double it.
	cfg.Version = strings.TrimPrefix(cfg.Version, "v")

	// SectionOrder from Config or parity defaults — for documentation / cross-lang parity.
	// Go's section order is enforced by fang (COMMANDS before FLAGS); this field
	// is validated but not re-applied since fang owns the template.
	_ = cfg.Help.SectionOrder // consumed by TS/Python; Go relies on fang defaults
	_ = parity.Values.Help.SectionOrder

	// Built-in command groups: default "COMMANDS" (empty ID) + "MANAGEMENT" (hidden).
	cmd.AddGroup(
		&cobra.Group{ID: "management", Title: "MANAGEMENT"},
	)
	// Custom groups from config.
	for _, g := range cfg.Help.Groups {
		cmd.AddGroup(&cobra.Group{ID: g.ID, Title: g.Title})
	}

	// Hide the default help command; -h/--help flag remains.
	cmd.SetHelpCommand(&cobra.Command{Hidden: true})

	// Eagerly register -h/--help so it is available before Execute().
	cmd.InitDefaultHelpFlag()

	// Completion subcommand: let cobra register it (not disabled),
	// but place it in the management group so it's hidden from default --help.

	// --help-all: show all groups including hidden ones.
	// Stored on Root; checked in Execute before fang runs.
	cmd.Flags().Bool("help-all", false, "Show all commands including management")
	cmd.Flags().Lookup("help-all").NoOptDefVal = "true"

	// Per-group help flags: --help-<id> for each registered group.
	groupTitles := map[string]string{
		"management": "MANAGEMENT",
	}
	for _, g := range cfg.Help.Groups {
		groupTitles[g.ID] = g.Title
	}
	for id := range groupTitles {
		flagName := "help-" + id
		cmd.Flags().Bool(flagName, false, "Show only "+id+" commands")
		cmd.Flags().Lookup(flagName).NoOptDefVal = "true"
	}

	// Hidden "help" subcommand: accepts group ID or "all".
	helpSub := &cobra.Command{
		Use:    "help [group]",
		Short:  "Show help for a command group",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
	}
	cmd.AddCommand(helpSub)

	// Global persistent flags bound to viper.
	pf := cmd.PersistentFlags()
	if !cfg.Disable.Quiet {
		pf.Bool("quiet", false, "Suppress non-essential output")
		_ = v.BindPFlag("quiet", pf.Lookup("quiet"))
	}
	// -V / --verbose: stackable count flag (e.g. -VV = 2).
	// Stored on Root; log/log.WithVerbose reads it.
	pf.CountP("verbose", "V", "Increase log verbosity (-V=debug, -VV=trace)")
	_ = v.BindPFlag("verbose", pf.Lookup("verbose"))

	if !cfg.Disable.NoColor {
		pf.Bool("no-color", false, "Disable ANSI color")
		_ = v.BindPFlag("no-color", pf.Lookup("no-color"))
	}

	// hiddenDefault tracks root persistent flags that are kit-owned
	// plumbing not part of the cross-language parity FLAGS contract.
	// They are marked Hidden=true here and revealed by --help-all.
	var hiddenDefault []string
	hideFlag := func(name string) {
		if f := pf.Lookup(name); f != nil {
			f.Hidden = true
			hiddenDefault = append(hiddenDefault, name)
		}
	}

	if !cfg.Disable.Chdir {
		pf.StringP("chdir", "C", "", "Change directory before running (path or tool-specific target)")
		_ = v.BindPFlag("chdir", pf.Lookup("chdir"))
		hideFlag("chdir")
	}

	if !cfg.Disable.Config {
		// StringArrayP (not StringSliceP) so values are NOT split on commas:
		// -c 'tags=["a","b"]' must reach the parser as one token.
		pf.StringArrayP("config", "c", nil,
			"Layer extra config (repeatable). "+
				"key=value overrides a single value (dotted keys for nesting; "+
				"value parses as YAML, falls back to literal). "+
				"A bare path loads an additional config file after the "+
				"discovered ones. -c tokens win over file layers.")
		_ = v.BindPFlag("config", pf.Lookup("config"))
		hideFlag("config")
	}

	if !cfg.Disable.Format {
		output.RegisterFlags(cmd, v)
		// --format itself is part of the parity contract; the rest of
		// the output flag suite is implementation detail.
		for _, name := range []string{
			"format-opt", "format-help", "cols", "columns", "template", "output",
		} {
			hideFlag(name)
		}
	}
	if !cfg.Disable.Hints {
		output.RegisterHintFlags(cmd, v)
	}
	if !cfg.Disable.Progress {
		pf.String("progress-format", "human",
			"Progress output format (human or json). "+
				"Defaults to json when --format=json unless explicitly set.")
		_ = v.BindPFlag("progress-format", pf.Lookup("progress-format"))
		hideFlag("progress-format")
	}

	if !cfg.Disable.DryRun {
		// Global --dry-run: bound to viper key kit.dry_run. Per-command
		// opt-in is enforced separately (see SupportsDryRun); this just
		// makes the flag known to cobra and viper. The cli wraps the
		// root context with sideeffect.WithDryRun before dispatch via
		// the PersistentPreRunE chain below.
		pf.Bool(globalDryRunFlag, false,
			"Preview side effects without applying them (must be supported by the command).")
		_ = v.BindPFlag(globalDryRunViperKey, pf.Lookup(globalDryRunFlag))
		hideFlag(globalDryRunFlag)
	}

	// Delegation-safety globals (§8.6). Always registered: kit owns
	// the contract end-to-end. Adopters can override defaults via
	// Config.Globals if needed, but the names are reserved.
	pf.String(confirmFlag, "",
		"Confirmation policy for destructive commands (auto|yes|no|prompt). "+
			"Default: prompt on a TTY, no otherwise.")
	_ = v.BindPFlag(confirmFlag, pf.Lookup(confirmFlag))
	hideFlag(confirmFlag)
	pf.Int(maxOpsFlag, 0,
		"Cap mutating operations per invocation. 0 = unlimited.")
	_ = v.BindPFlag(maxOpsFlag, pf.Lookup(maxOpsFlag))
	hideFlag(maxOpsFlag)
	pf.String(policyFlag, "",
		"Named delegation policy (loaded from $XDG_CONFIG_HOME/<tool>/policies/<name>.yaml).")
	_ = v.BindPFlag(policyFlag, pf.Lookup(policyFlag))
	hideFlag(policyFlag)

	// --api-version (§13). Capability negotiation: when set, hides
	// commands annotated kit/since:<ver> newer than requested and
	// refuses flags annotated kit/flag-since:<ver> newer than requested.
	// Refused at parse time when below kit/min-api-version on the root.
	pf.String(apiVersionFlag, "",
		"Request a specific CLI schema version (MAJOR.MINOR) for compatibility-mode filtering.")
	_ = v.BindPFlag(apiVersionFlag, pf.Lookup(apiVersionFlag))
	hideFlag(apiVersionFlag)

	// Tool-specific extra persistent flags. When a pointer destination
	// is provided (StringVar/BoolVar/IntVar) the flag is bound to that
	// pointer in addition to viper; otherwise the flag falls back to a
	// string-only registration that lives entirely in viper.
	for _, g := range cfg.Globals {
		registerGlobalFlag(pf, g)
		_ = v.BindPFlag(g.Name, pf.Lookup(g.Name))
	}

	var theme Theme
	if cfg.Palette.Command != nil {
		theme = themeFromPalette(cfg.Palette)
	} else {
		theme = buildTheme(cfg.Accent)
	}

	// Built-in "management" is always hidden; custom groups opt in via Hidden.
	hidden := map[string]bool{"management": true}
	for _, g := range cfg.Help.Groups {
		if g.Hidden {
			hidden[g.ID] = true
		}
	}

	r := &Root{
		Cmd:                cmd,
		Viper:              v,
		Config:             cfg,
		Theme:              theme,
		Hints:              output.NewHintSet(),
		Streams:            NewStreamWriter(),
		Auth:               NoAuth{},
		aliases:            make(map[string]string),
		hiddenGroups:       hidden,
		groupTitles:        groupTitles,
		hiddenDefaultFlags: hiddenDefault,
	}

	for _, o := range opts {
		o(r)
	}
	// Snapshot kit-shipped subcommands now, BEFORE adopters mount
	// their own commands (which happen AFTER cli.New returns).
	// Subsequent late-mount factories (e.g. RegisterSpecCommand) call
	// Root.MarkReserved to keep the set current.
	r.reservedSnapshot()
	output.SetDefaultTableStyle(r.TableStyle())

	// Compose the PersistentPreRunE chain. Hooks run in registration
	// order; the first error short-circuits the chain. Order:
	//   1. chdir (so XDG paths / peer registry see the resolved cwd)
	//   2. identity init failure (deferred so --help still works)
	//   3. peer init failure (deferred for the same reason)
	//   4. cfg.Hooks.PrePersistentRunE (adopter-supplied)
	var hooks []func(cmd *cobra.Command, args []string) error

	if !cfg.Disable.Chdir {
		hooks = append(hooks, func(_ *cobra.Command, _ []string) error {
			target := v.GetString("chdir")
			if target == "" {
				return nil
			}
			dir, err := resolveChdir(target, cfg.ChdirResolver)
			if err != nil {
				return err
			}
			return os.Chdir(dir)
		})
	}

	if err := r.initIdentity(); err != nil {
		hooks = append(hooks, func(_ *cobra.Command, _ []string) error {
			return err
		})
	}
	if err := r.initPeers(); err != nil {
		hooks = append(hooks, func(_ *cobra.Command, _ []string) error {
			return err
		})
	}

	if !cfg.Disable.Progress {
		hooks = append(hooks, func(cmd *cobra.Command, _ []string) error {
			cmd.SetContext(progress.WithReporter(
				cmd.Context(),
				resolveProgressReporter(cmd, v, r),
			))
			return nil
		})
	}

	if !cfg.Disable.DryRun {
		hooks = append(hooks, r.installDryRunHook())
	}

	if cfg.Hooks.PrePersistentRunE != nil {
		hooks = append(hooks, cfg.Hooks.PrePersistentRunE)
	}

	if len(hooks) > 0 {
		r.Cmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
			for _, h := range hooks {
				if err := h(cmd, args); err != nil {
					return err
				}
			}
			return nil
		}
	}

	// Deferred: cobra parses after New(); OnInitialize reads the flag
	// from cobra's thread-safe store — no captured local, no PreRun conflict.
	cobra.OnInitialize(func() {
		r.verboseCount, _ = cmd.Flags().GetCount("verbose")
	})

	return r
}

// NewE is the explicit-error companion to cli.New. It applies the
// same construction logic and additionally runs Root.Validate at
// construction time when Config.EnforceValidate is true (the default
// at 0.1.0-alpha.0). The *ValidationError is returned to the caller
// — Config.ValidationFailureMode is ignored on this path, since the
// whole point of NewE is "I'll handle it." Adopters who embed kit
// inside a larger CLI (multi-tool harness, plugin host, server
// pre-boot validator) use NewE so they can route the failure into
// their own error envelope.
//
// Callers that just want main()-style behavior keep calling
// cli.New; the default ValidationFailureExit mode preserves the
// pre-12fcc shipped UX.
func NewE(cfg Config, opts ...func(*Root)) (*Root, *ValidationError) {
	r := New(cfg, opts...)
	if !r.Config.EnforceValidate {
		return r, nil
	}
	err := r.Validate()
	if err == nil {
		return r, nil
	}
	if ve, ok := err.(*ValidationError); ok {
		return r, ve
	}
	// Non-ValidationError (e.g. -c parse failure surfaced via
	// configArgsErr). Wrap it in a synthetic ValidationError so the
	// caller's contract holds.
	return r, &ValidationError{Invalid: []string{err.Error()}}
}

// dispatchValidationFailure routes a Validate failure through the
// Config.ValidationFailureMode. Returns (handled, returned) where
// handled=true means Execute should return immediately with
// `returned` as its error.
func (r *Root) dispatchValidationFailure(err error) (bool, error) {
	switch r.Config.ValidationFailureMode {
	case ValidationFailureError:
		return true, err
	case ValidationFailurePanic:
		panic(err)
	case ValidationFailureSilent:
		// Best-effort slog without pulling in a logger handle;
		// adopters who use this mode wire their own slog handler
		// upstream of cli.Execute.
		fmt.Fprintln(os.Stderr, "[warn] cli validation failure (silent mode):", err.Error())
		return false, nil
	default:
		// ValidationFailureExit (zero value) — preserve the
		// pre-12fcc shipped behavior.
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(int(ExitUsage))
		return true, nil // unreachable
	}
}

// Execute runs the root command through fang, which provides styled help,
// version output, error rendering, and man page generation.
func (r *Root) Execute(ctx context.Context) error {
	if r.Config.Help.ShowAliases {
		annotateAliases(r.Cmd)
	}

	// Eagerly register cobra's completion command and place it in the
	// management group so it's hidden from default --help.
	r.Cmd.InitDefaultCompletionCmd()
	for _, c := range r.Cmd.Commands() {
		if c.Name() == "completion" {
			c.GroupID = "management"
			break
		}
	}

	r.applyGroupVisibility()
	r.installLeafHelp()

	// Auto-register kit-managed flags (--dry-run on write/destructive
	// leaves) before validation/parsing. Idempotent + independent of
	// EnforceValidate: registration runs even when validation is off,
	// because the flag must exist for adopters that call IsDryRun in
	// RunE regardless of whether the validator is enforcing.
	r.AutoRegisterFlags()

	// Surface dry-run support state in `<tool> help <cmd>` so users
	// know which leaves accept --dry-run. Idempotent.
	r.applyDryRunHelpAddendum()

	// Emit a one-time deprecation warning when any leaf still uses
	// the legacy ADR-0019 kit/dry-run: supported annotation. The
	// annotation keeps working as a back-compat synonym; the
	// warning makes the supersession (ADR-0020) audible.
	r.warnLegacySupportsDryRun()

	// --api-version (§13). Detect the requested version pre-parse so
	// hidden commands are excluded from help and refused at dispatch.
	// Filtering is opt-in: empty value means "all commands present".
	if reqAPI := r.scanArgsForAPIVersion(); reqAPI != "" {
		if minErr := checkMinAPIVersion(r.Cmd, reqAPI); minErr != nil {
			fmt.Fprintln(os.Stderr, minErr.Error())
			os.Exit(2)
		}
		applyAPIVersionFilter(r.Cmd, reqAPI)
	}

	// Pre-flight: refuse to run a misconfigured tool when the adopter
	// has Config.EnforceValidate=true (the default at 0.1.0-alpha.0
	//). Adopters opt out via DisableValidate;
	// tests that exercise the validator directly call Root.Validate()
	// rather than going through Execute.
	if r.Config.EnforceValidate {
		if err := r.Validate(); err != nil {
			if handled, returned := r.dispatchValidationFailure(err); handled {
				return returned
			}
		}
	}

	// Signature validator: gated by Config.SignatureStrictness,
	// independent of EnforceValidate. silent (zero value) is a
	// no-op; warn logs each violation via slog and continues;
	// reject routes through ValidationFailureMode.
	if r.Config.SignatureStrictness != SignatureStrictnessSilent {
		if handled, returned := r.dispatchSignatureReport(); handled {
			return returned
		}
	}

	// Wrap every leaf RunE with the structured-error envelope middleware
	// so adopter errors are rendered to stderr in the requested format.
	r.WrapRunE()

	return fang.Execute(ctx, r.Cmd,
		fang.WithVersion(r.Config.Version),
		fang.WithColorSchemeFunc(brandColorScheme),
	)
}

// Validate walks the command tree and refuses leaf commands missing
// required annotations. The shipped check covers kit/side-effect and
// kit/idempotent on every runnable leaf (after auto-applying the
// verb-default kit/idempotent tag) and surfaces any -c/--config
// parse failure stashed by ConfigArgs.
//
// When Config.EnforceValidate is true the validator additionally
// runs the Layer-A checks :
//   - cmd.Short on every runnable leaf and group node;
//     cmd.Long on every runnable leaf
//   - kit/output-schema annotation, when present, parses as JSON
//   - reserved `status` subcommand registered on the root
//   - shape rules: depth-1 leaves require kit/top-level-verb;
//     depth >= 3 requires kit/hierarchical on intermediate nodes
//     (unless the depth-1 ancestor is reserved); MaxTopLevelVerbs
//     and MaxHierarchyDepth caps are enforced
//   - configurable gates per Config.Enforce* flags
//     (DryRunRationale, DestructiveToken, Guidance)
//   - PassthroughStrictness="reject" treats kit/passthrough as a
//     hard error
//
// Built-in kit commands (completion, the auto-registered help) are
// exempt, as are non-runnable shells (cobra prints help for these
// so they make no real-world side-effect). Returns *ValidationError
// when any check fails; nil otherwise.
func (r *Root) Validate() error {
	if r == nil {
		return nil
	}
	if r.Viper != nil {
		_, _, _ = r.ConfigArgs()
	}
	if r.configArgsErr != nil {
		return fmt.Errorf("invalid -c/--config flag: %w", r.configArgsErr)
	}

	// Auto-apply default kit/idempotent before checking. Adopter
	// annotations are preserved; only gaps are filled.
	applyDefaultIdempotency(r.Cmd)

	ve := &ValidationError{}
	r.collectShippedValidation(ve)
	if r.Config.EnforceValidate {
		r.collectLayerAValidation(ve)
	}
	if ve.HasIssues() {
		return ve
	}
	return nil
}

// collectShippedValidation runs the original side-effect +
// idempotency arms that pre-date the Layer-A validator. Always active when
// Validate() is invoked, regardless of EnforceValidate.
func (r *Root) collectShippedValidation(ve *ValidationError) {
	walk(r.Cmd, func(cmd *cobra.Command) {
		if !isLeaf(cmd) || isBuiltin(cmd) {
			return
		}
		if !cmd.Runnable() {
			return
		}
		s, ok := GetSideEffect(cmd)
		if !ok {
			ve.Missing = append(ve.Missing, cmd.CommandPath())
		} else if !validSideEffects[s] {
			ve.Invalid = append(ve.Invalid,
				fmt.Sprintf("%s=%q", cmd.CommandPath(), string(s)))
		}
		i, ok := GetIdempotency(cmd)
		if !ok {
			ve.MissingIdempotency = append(ve.MissingIdempotency, cmd.CommandPath())
		} else if !validIdempotency[i] {
			ve.InvalidIdempotency = append(ve.InvalidIdempotency,
				fmt.Sprintf("%s=%q", cmd.CommandPath(), string(i)))
		}
	})
}

// collectLayerAValidation runs the Layer-A checks. Only
// invoked when Config.EnforceValidate is true.
func (r *Root) collectLayerAValidation(ve *ValidationError) {
	r.checkReservedStatus(ve)
	r.checkShortLongOutputSchema(ve)
	r.checkShape(ve)
	r.checkConfigurableGates(ve)
}

// checkReservedStatus verifies the reserved `status` subcommand is
// registered on the root. Adopters who shadow the name with their
// own implementation still pass — the check is presence by name.
func (r *Root) checkReservedStatus(ve *ValidationError) {
	if r.Cmd == nil {
		return
	}
	for _, c := range r.Cmd.Commands() {
		if c.Name() == "status" {
			return
		}
	}
	ve.MissingStatusSubcommand = append(ve.MissingStatusSubcommand,
		r.Cmd.CommandPath())
}

// checkShortLongOutputSchema enforces hard tier H1, H2, H5: Short on
// every runnable leaf + group node, Long on every runnable leaf,
// kit/output-schema parses when declared.
func (r *Root) checkShortLongOutputSchema(ve *ValidationError) {
	walk(r.Cmd, func(cmd *cobra.Command) {
		if cmd == r.Cmd {
			// Root: Short already required by Config.Short; skip.
			return
		}
		if isBuiltin(cmd) {
			return
		}
		runnable := cmd.Runnable()
		isGroup := cmd.HasSubCommands() && !runnable
		if !runnable && !isGroup {
			return
		}
		if cmd.Short == "" {
			ve.MissingShort = append(ve.MissingShort, cmd.CommandPath())
		}
		if runnable && cmd.Long == "" {
			ve.MissingLong = append(ve.MissingLong, cmd.CommandPath())
		}
		if runnable {
			if raw, _, ok := GetOutputSchemaJSON(cmd); ok {
				if !json.Valid(raw) {
					ve.InvalidOutputSchema = append(ve.InvalidOutputSchema,
						fmt.Sprintf("%s: not valid JSON", cmd.CommandPath()))
				}
			}
		}
	})
}

// checkShape runs the §3 noun-verb pass: top-level verb annotation,
// MaxTopLevelVerbs cap, hierarchical depth annotations, and the
// MaxHierarchyDepth ceiling. Passthrough strictness=reject is also
// evaluated here so the single walk covers all shape concerns.
func (r *Root) checkShape(ve *ValidationError) {
	if r.Cmd == nil {
		return
	}
	maxTop := r.Config.MaxTopLevelVerbs
	if maxTop <= 0 {
		maxTop = defaultMaxTopLevelVerbs
	}
	maxDepth := r.Config.MaxHierarchyDepth
	if maxDepth <= 0 {
		maxDepth = defaultMaxHierarchyDepth
	}
	if maxDepth > hardMaxHierarchyDepth {
		maxDepth = hardMaxHierarchyDepth
	}
	passthroughReject := r.Config.PassthroughStrictness == PassthroughReject

	topLevelCount := 0
	walkDepth(r.Cmd, 0, func(cmd *cobra.Command, depth int) {
		if cmd == r.Cmd || isBuiltin(cmd) {
			return
		}
		runnable := cmd.Runnable()
		// Depth limit applies to every node beyond the cap.
		if depth > maxDepth {
			ve.HierarchyDepthExceeded = append(ve.HierarchyDepthExceeded,
				fmt.Sprintf("%s (depth=%d>%d)", cmd.CommandPath(), depth, maxDepth))
		}
		if passthroughReject && IsPassthrough(cmd) {
			ve.PassthroughRejected = append(ve.PassthroughRejected, cmd.CommandPath())
		}
		if !runnable {
			// Group / intermediate node. When it sits at depth >= 2
			// (i.e., it's a sub-noun grouping), require
			// kit/hierarchical unless its depth-1 ancestor is
			// reserved.
			if depth >= 2 && !r.IsReserved(topAncestorName(cmd, r.Cmd)) &&
				!IsHierarchical(cmd) {
				ve.UnannotatedDepthExceedance = append(ve.UnannotatedDepthExceedance,
					cmd.CommandPath())
			}
			return
		}
		switch depth {
		case 1:
			if !IsTopLevelVerb(cmd) {
				ve.UnannotatedTopLevelLeaf = append(ve.UnannotatedTopLevelLeaf,
					cmd.CommandPath())
			}
			topLevelCount++
		case 2:
			// canonical or reserved-group-verb; nothing to enforce.
		default:
			// depth >= 3
			if depth >= 3 && !r.depthThreeAncestorOK(cmd) {
				ve.UnannotatedDepthExceedance = append(ve.UnannotatedDepthExceedance,
					cmd.CommandPath())
			}
		}
	})
	if topLevelCount > maxTop {
		ve.TooManyTopLevelVerbs = append(ve.TooManyTopLevelVerbs,
			fmt.Sprintf("%s: %d top-level verbs > MaxTopLevelVerbs=%d",
				r.Cmd.CommandPath(), topLevelCount, maxTop))
	}
}

// depthThreeAncestorOK reports whether cmd at depth >= 3 has either
// (a) every intermediate ancestor annotated kit/hierarchical, or
// (b) a reserved depth-1 ancestor (kit toolspec, kit stage, …).
func (r *Root) depthThreeAncestorOK(cmd *cobra.Command) bool {
	// Walk up to depth-1 ancestor.
	chain := []*cobra.Command{}
	for c := cmd.Parent(); c != nil && c != r.Cmd; c = c.Parent() {
		chain = append(chain, c)
	}
	// chain is leaf→root: chain[len-1] is the depth-1 ancestor.
	if len(chain) == 0 {
		return true
	}
	top := chain[len(chain)-1]
	if r.IsReserved(top.Name()) {
		return true
	}
	for _, anc := range chain {
		if !IsHierarchical(anc) {
			return false
		}
	}
	return true
}

// topAncestorName returns the immediate child of root in cmd's
// ancestry chain (i.e., the depth-1 ancestor's Name). Empty when
// cmd is root or directly under root.
func topAncestorName(cmd, root *cobra.Command) string {
	if cmd == nil || cmd == root {
		return ""
	}
	last := cmd
	for p := cmd.Parent(); p != nil && p != root; p = p.Parent() {
		last = p
	}
	if last == cmd && cmd.Parent() != root {
		return ""
	}
	return last.Name()
}

// checkConfigurableGates runs the configurable tier (C1-C4) plus
// passthrough warn (informational warning only).
func (r *Root) checkConfigurableGates(ve *ValidationError) {
	if !r.Config.EnforceDryRunRationale &&
		!r.Config.EnforceDestructiveToken &&
		!r.Config.EnforceGuidance {
		return
	}
	walk(r.Cmd, func(cmd *cobra.Command) {
		if !isLeaf(cmd) || isBuiltin(cmd) || !cmd.Runnable() {
			return
		}
		se, _ := GetSideEffect(cmd)
		if r.Config.EnforceDryRunRationale {
			isOpt := cmd.Annotations != nil &&
				cmd.Annotations[dryRunAnnotation] == dryRunOptedOut
			if isOpt && (isWriteLike(se) || isDestructiveLike(se)) {
				if cmd.Annotations[kitDryRunRationale] == "" {
					ve.MissingDryRunRationale = append(ve.MissingDryRunRationale,
						cmd.CommandPath())
				}
			}
		}
		if r.Config.EnforceDestructiveToken && isDestructiveLike(se) {
			if !requiresDestructiveToken(cmd) {
				ve.MissingDestructiveToken = append(ve.MissingDestructiveToken,
					cmd.CommandPath())
			}
		}
		if r.Config.EnforceGuidance {
			if _, ok := GetExamples(cmd); !ok {
				ve.MissingExamples = append(ve.MissingExamples, cmd.CommandPath())
			}
			if se != SideEffectRead {
				if _, ok := GetNextSteps(cmd); !ok {
					ve.MissingNextSteps = append(ve.MissingNextSteps,
						cmd.CommandPath())
				}
			}
		}
	})
}

// walkDepth invokes fn on cmd and every descendant with the depth
// from root (root at depth 0). Depth-first.
func walkDepth(cmd *cobra.Command, depth int, fn func(*cobra.Command, int)) {
	fn(cmd, depth)
	for _, c := range cmd.Commands() {
		walkDepth(c, depth+1, fn)
	}
}

// isBuiltin returns true for kit-shipped commands the adopter didn't
// author (completion + its shell sub-leaves, the auto-registered
// help, the hidden __complete cobra helper). Match by name plus a
// parent-chain check so the cobra-generated completion shell leaves
// (bash, fish, zsh, powershell) are exempt under EnforceValidate.
func isBuiltin(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	switch cmd.Name() {
	case "completion", "help", "__complete", "__completeNoDesc":
		return true
	}
	// Completion shell sub-leaves: bash|fish|zsh|powershell under a
	// completion parent. Cobra registers these automatically; the
	// adopter doesn't author them, so they ride kit's exemption.
	if p := cmd.Parent(); p != nil && p.Name() == "completion" {
		return true
	}
	// Exempt any leaf the adopter marked kit/exempt-validation=true.
	// Used for the rare commands kit ships internally that can't
	// reasonably carry the full annotation set (e.g. hidden compat
	// shims that exist only to keep older scripts compiling).
	if cmd.Annotations != nil && cmd.Annotations["kit/exempt-validation"] == "true" {
		return true
	}
	return false
}

// isLeaf returns true when cmd has no subcommands.
func isLeaf(cmd *cobra.Command) bool { return !cmd.HasSubCommands() }

// walk invokes fn on cmd and every descendant, depth-first.
func walk(cmd *cobra.Command, fn func(*cobra.Command)) {
	fn(cmd)
	for _, c := range cmd.Commands() {
		walk(c, fn)
	}
}

// ApplyGroupVisibility hides commands in hidden groups unless --help-all is
// present in the args. When --help-all is detected, args are rewritten to
// --help so fang renders the full help.
//
// Per-group help: --help-<id> or "help <id>" renders only that group's
// commands. "help all" is equivalent to --help-all.
//
// Exported for callers that bypass r.Execute (e.g. to call fang directly
// with WithoutVersion or other custom options); they must invoke this
// before fang.Execute so help rendering sees the right Hidden flags.
func (r *Root) ApplyGroupVisibility() { r.applyGroupVisibility() }

func (r *Root) applyGroupVisibility() {
	args := r.resolveArgs()

	// Check for "help <id>" subcommand form.
	if len(args) >= 2 && args[0] == "help" {
		groupID := args[1]
		if groupID == "all" {
			r.Cmd.SetArgs([]string{"--help"})
			return
		}
		if _, ok := r.groupTitles[groupID]; !ok {
			// Wire up RunE to return an error for unknown group.
			helpCmd := r.findHelpSubcommand()
			if helpCmd != nil {
				helpCmd.RunE = func(cmd *cobra.Command, _ []string) error {
					return fmt.Errorf("unknown help group %q", groupID)
				}
			}
			return
		}
		r.installGroupHelp(groupID)
		return
	}

	// Check for --help-<id> flag form.
	for id := range r.groupTitles {
		flag := "--help-" + id
		for _, a := range args {
			if a == flag {
				r.installGroupHelp(id)
				return
			}
		}
	}

	// Check for --help-all.
	helpAll := false
	for _, a := range args {
		if a == "--help-all" {
			helpAll = true
			break
		}
	}
	if helpAll {
		// Reveal kit-owned plumbing flags (--config, --chdir, --dry-run,
		// etc.) that are hidden from default --help to keep the FLAGS
		// section aligned with the cross-language parity contract.
		for _, name := range r.hiddenDefaultFlags {
			if f := r.Cmd.PersistentFlags().Lookup(name); f != nil {
				f.Hidden = false
			}
		}
		cleaned := make([]string, 0, len(args))
		for _, a := range args {
			if a == "--help-all" {
				cleaned = append(cleaned, "--help")
			} else {
				cleaned = append(cleaned, a)
			}
		}
		r.Cmd.SetArgs(cleaned)
		return
	}
	// Default: hide commands in hidden groups.
	for _, c := range r.Cmd.Commands() {
		if r.hiddenGroups[c.GroupID] {
			c.Hidden = true
		}
	}
}

// findHelpSubcommand returns the hidden "help" subcommand.
func (r *Root) findHelpSubcommand() *cobra.Command {
	for _, c := range r.Cmd.Commands() {
		if c.Name() == "help" {
			return c
		}
	}
	return nil
}

// installGroupHelp rewrites the command tree so only the target group's
// commands are visible, then triggers --help rendering.
func (r *Root) installGroupHelp(groupID string) {
	// Hide all commands not in the target group.
	for _, c := range r.Cmd.Commands() {
		if c.GroupID != groupID {
			c.Hidden = true
		}
	}

	r.Cmd.SetArgs([]string{"--help"})
}

// SetArgs stores args for pre-parse inspection and passes them to cobra.
func (r *Root) SetArgs(args []string) {
	r.overrideArgs = args
	r.Cmd.SetArgs(args)
}

// resolveArgs returns the args that will be used by cobra's Execute.
// If SetArgs was called, those are returned; otherwise os.Args[1:].
func (r *Root) resolveArgs() []string {
	if r.overrideArgs != nil {
		return r.overrideArgs
	}
	if len(os.Args) > 1 {
		return os.Args[1:]
	}
	return nil
}

// annotateAliases appends "(aliases: x, y)" to Short for commands with aliases.
func annotateAliases(root *cobra.Command) {
	for _, c := range root.Commands() {
		if len(c.Aliases) > 0 {
			c.Short += " (aliases: " + strings.Join(c.Aliases, ", ") + ")"
		}
		annotateAliases(c)
	}
}

// ConfigArgs returns the parsed -c/--config tokens, splitting bare paths
// (no '=' character) from key=value override pairs. Returns zero values when
// no -c flags were given or when the flag is disabled.
//
// On parse failure (e.g. bare -c <dir> with no Config.ProjectMarker, or a
// directory whose marker file is missing) the error is returned directly AND
// stashed on Root so Root.Validate() can surface it during the EnforceValidate
// pre-flight. This was previously swallowed silently — adopters saw an empty
// override map and a successful Load, but their -c flag had been dropped.
//
// When Config.ProjectMarker is set, a bare-directory token resolves to
// <dir>/<ProjectMarker>. See [config.WithProjectMarker] for the contract.
//
// Adopters wire both halves into their config-loading site:
//
//	cfg := MyConfig{}
//	paths, overrides, err := r.ConfigArgs()
//	if err != nil { return err }
//	_ = config.Load(&cfg, config.Options{
//	    UserConfigPath:    "...",
//	    ExtraConfigPaths:  paths,
//	    Overrides:         overrides,
//	})
func (r *Root) ConfigArgs() (paths []string, overrides map[string]any, err error) {
	if r == nil || r.Viper == nil {
		return nil, nil, nil
	}
	tokens := r.Viper.GetStringSlice("config")
	if r.Cmd != nil {
		if flag := r.Cmd.PersistentFlags().Lookup("config"); flag != nil {
			if values, getErr := r.Cmd.PersistentFlags().GetStringArray("config"); getErr == nil && len(values) > 0 {
				tokens = values
			}
		}
	}
	tokens = compactConfigArgs(tokens)
	if len(tokens) == 0 {
		return nil, nil, nil
	}
	var opts []configoverrides.ParseOption
	if r.Config.ProjectMarker != "" {
		opts = append(opts, configoverrides.WithProjectMarker(r.Config.ProjectMarker))
	}
	paths, overrides, err = configoverrides.ParseConfigArgs(tokens, opts...)
	if err != nil {
		// Stash for Validate() so EnforceValidate-mode adopters get
		// a clean usage failure instead of a silently-dropped flag.
		// Direct callers also see the error in the return value.
		r.configArgsErr = err
		return nil, nil, err
	}
	r.configArgsErr = nil
	return paths, overrides, nil
}

func compactConfigArgs(tokens []string) []string {
	if len(tokens) == 0 {
		return nil
	}
	out := tokens[:0]
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" || token == "[]" {
			continue
		}
		out = append(out, token)
	}
	return out
}

// ConfigOverrides is a convenience accessor returning only the override map
// half of ConfigArgs. Bare-path -c tokens are dropped from the result; use
// ConfigArgs when both halves are needed.
//
// Parse errors are NOT returned by this accessor — they're stashed on Root
// (same as ConfigArgs) so Root.Validate() can surface them in the
// EnforceValidate pre-flight. Callers that want the error directly should
// call ConfigArgs.
func (r *Root) ConfigOverrides() map[string]any {
	_, overrides, _ := r.ConfigArgs()
	return overrides
}

// registerGlobalFlag installs a tool-specific persistent flag onto pf,
// honoring optional pointer destinations on g. When a pointer is set the
// flag's parsed value is written back to that pointer in addition to the
// viper binding installed by the caller. When all pointers are nil the
// flag falls back to a string-only registration to preserve backward
// compatibility with adopters that only use viper.
func registerGlobalFlag(pf *pflag.FlagSet, g Flag) {
	switch {
	case g.StringVar != nil:
		if g.Short != "" {
			pf.StringVarP(g.StringVar, g.Name, g.Short, g.Default, g.Usage)
		} else {
			pf.StringVar(g.StringVar, g.Name, g.Default, g.Usage)
		}
	case g.BoolVar != nil:
		def, _ := strconv.ParseBool(g.Default)
		if g.Short != "" {
			pf.BoolVarP(g.BoolVar, g.Name, g.Short, def, g.Usage)
		} else {
			pf.BoolVar(g.BoolVar, g.Name, def, g.Usage)
		}
	case g.IntVar != nil:
		def, _ := strconv.Atoi(g.Default)
		if g.Short != "" {
			pf.IntVarP(g.IntVar, g.Name, g.Short, def, g.Usage)
		} else {
			pf.IntVar(g.IntVar, g.Name, def, g.Usage)
		}
	default:
		if g.Short != "" {
			pf.StringP(g.Name, g.Short, g.Default, g.Usage)
		} else {
			pf.String(g.Name, g.Default, g.Usage)
		}
	}
}

// resolveChdir maps a -C/--chdir target to a concrete directory.
// If target is an existing directory, returns target. Otherwise
// delegates to resolver (tool-specific lookup). nil resolver +
// non-dir target → error mentioning the quoted target.
func resolveChdir(target string, resolver func(string) (string, error)) (string, error) {
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		return target, nil
	}
	if resolver != nil {
		return resolver(target)
	}
	return "", fmt.Errorf("cannot chdir to %q: not a directory", target)
}

// brandColorScheme returns a fang ColorScheme with hop.top brand accents.
func brandColorScheme(c lipgloss.LightDarkFunc) fang.ColorScheme {
	cs := fang.DefaultColorScheme(c)
	cs.Title = lipgloss.Color("#FFFFFF")
	cs.Command = Neon.Command
	cs.Flag = Neon.Flag
	cs.Program = Neon.Command
	cs.Argument = lipgloss.Color("#B5E89B")
	cs.DimmedArgument = lipgloss.Color("#8ABF6E")
	return cs
}
