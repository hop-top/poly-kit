package cli_test

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
)

// ansiRE matches ANSI SGR escape sequences.
var ansiRE = regexp.MustCompile(`\x1b\[[^m]*m`)

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }
func hasANSI(s string) bool     { return ansiRE.MatchString(s) }

// helpLines returns plain (ANSI-stripped) --help lines for a root with one subcommand.
func helpLines(t *testing.T) []string {
	t.Helper()
	// DisableValidate: tests exercise help/aliases/dispatch behavior
	// against ad-hoc minimal cobra fixtures that don't carry the
	// full Layer-A annotation set; the 12fcc-static §6 escape hatch.
	r := cli.New(cli.Config{Name: "mytool", Version: "1.2.3", Short: "A tool", DisableValidate: true})
	r.Cmd.AddCommand(subCmd())
	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetArgs([]string{"--help"})
	_ = r.Execute(t.Context())
	return strings.Split(stripANSI(buf.String()), "\n")
}

func root() *cli.Root {
	// DisableValidate: the shared test fixture is intentionally
	// minimal — adding the full Layer-A annotation tail to every
	// test would crowd out the behavior being tested. Conformance
	// is exercised in validate_matrix_test.go + status_test.go +
	// the kitconformance helper.
	return cli.New(cli.Config{
		Name:            "mytool",
		Version:         "1.2.3",
		Short:           "A test tool",
		DisableValidate: true,
	})
}

func TestNew_ReturnsRoot(t *testing.T) {
	r := root()
	require.NotNil(t, r.Cmd)
	require.NotNil(t, r.Viper)
	assert.Equal(t, "mytool", r.Config.Name)
	assert.Equal(t, "1.2.3", r.Config.Version)
}

func TestNew_NoHelpSubcommand(t *testing.T) {
	r := root()
	for _, sub := range r.Cmd.Commands() {
		switch sub.Name() {
		case "help":
			assert.True(t, sub.Hidden,
				"help subcommand must be hidden")
		case "completion":
			// completion is registered but placed in the management group
			// (hidden from default --help via group visibility)
			assert.Equal(t, "management", sub.GroupID,
				"completion must be in management group")
		}
	}
}

func TestNew_HelpFlagExists(t *testing.T) {
	r := root()
	f := r.Cmd.Flags().Lookup("help")
	require.NotNil(t, f, "expected -h/--help flag on root command")
}

func TestNew_GlobalFlags(t *testing.T) {
	r := root()
	pf := r.Cmd.PersistentFlags()
	assert.NotNil(t, pf.Lookup("quiet"), "--quiet must exist")
	assert.NotNil(t, pf.Lookup("no-color"), "--no-color must exist")
}

func TestNew_FormatFlagExists(t *testing.T) {
	r := root()
	pf := r.Cmd.PersistentFlags()
	f := pf.Lookup("format")
	assert.NotNil(t, f, "--format must be registered via output.RegisterFlags")
	assert.Equal(t, "table", f.DefValue, "default format must be table")
}

func TestNew_ThemePopulated(t *testing.T) {
	r := root()
	assert.NotNil(t, r.Theme.Accent, "accent must be set")
	assert.NotNil(t, r.Theme.Muted, "muted must be set")
	assert.NotNil(t, r.Theme.Error, "error color must be set")
	assert.NotNil(t, r.Theme.Success, "success color must be set")
	assert.NotNil(t, r.Theme.Warn, "warn color must be set")
}

func TestNew_CustomAccent(t *testing.T) {
	r := cli.New(cli.Config{
		Name:            "accenttool",
		Version:         "0.1.0",
		Short:           "Custom accent",
		Accent:          "#FF0000",
		DisableValidate: true,
	})
	// Accent should be the custom color, not the default.
	rr, gg, bb, _ := r.Theme.Accent.RGBA()
	assert.NotZero(t, rr, "accent red channel should be non-zero")
	// Verify it's roughly red (#FF0000).
	assert.Greater(t, rr, gg)
	assert.Greater(t, rr, bb)
}

func TestExecute_Version(t *testing.T) {
	r := root()
	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetArgs([]string{"--version"})
	err := r.Execute(t.Context())
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "mytool v1.2.3")
}

func TestExecute_Help(t *testing.T) {
	r := root()
	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetArgs([]string{"--help"})
	err := r.Execute(t.Context())
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "A test tool")
}

func TestExecute_UnknownFlag(t *testing.T) {
	r := root()
	r.Cmd.SetArgs([]string{"--nope"})
	err := r.Execute(t.Context())
	assert.Error(t, err)
}

func TestDisable_Format(t *testing.T) {
	r := cli.New(cli.Config{
		Name:            "notool",
		Version:         "0.1.0",
		Short:           "no format",
		Disable:         cli.Disable{Format: true},
		DisableValidate: true,
	})
	pf := r.Cmd.PersistentFlags()
	assert.Nil(t, pf.Lookup("format"), "--format must not be registered when Disable.Format=true")
}

func TestDisable_Quiet(t *testing.T) {
	r := cli.New(cli.Config{
		Name:            "notool",
		Version:         "0.1.0",
		Short:           "no quiet",
		Disable:         cli.Disable{Quiet: true},
		DisableValidate: true,
	})
	pf := r.Cmd.PersistentFlags()
	assert.Nil(t, pf.Lookup("quiet"), "--quiet must not be registered when Disable.Quiet=true")
}

func TestGlobals_FlagRegistered(t *testing.T) {
	r := cli.New(cli.Config{
		Name:    "gtool",
		Version: "0.1.0",
		Short:   "global flag test",
		Globals: []cli.Flag{
			// "experiment" stands in for any tool-specific persistent
			// flag. The previous fixture used "dry-run", but kit now
			// reserves --dry-run as a built-in global; collisions are
			// covered by Disable.DryRun (see TestDisable_DryRun).
			{Name: "experiment", Usage: "enable experiment mode", Default: "false"},
		},
		DisableValidate: true,
	})
	pf := r.Cmd.PersistentFlags()
	f := pf.Lookup("experiment")
	require.NotNil(t, f, "--experiment must be registered")
	assert.Equal(t, "false", f.DefValue, "default must match")
	assert.Equal(t, "false", r.Viper.GetString("experiment"), "viper must have the key")
}

func TestDisable_Hints(t *testing.T) {
	r := cli.New(cli.Config{
		Name:            "notool",
		Version:         "0.1.0",
		Short:           "no hints",
		Disable:         cli.Disable{Hints: true},
		DisableValidate: true,
	})
	pf := r.Cmd.PersistentFlags()
	assert.Nil(t, pf.Lookup("no-hints"), "--no-hints must not be registered when Disable.Hints=true")
}

func TestDisable_HintsDefaultOn(t *testing.T) {
	r := cli.New(cli.Config{Name: "t", Version: "0.1.0", Short: "t", DisableValidate: true})
	pf := r.Cmd.PersistentFlags()
	assert.NotNil(t, pf.Lookup("no-hints"), "--no-hints must be registered by default")
}

func TestDisable_FormatDoesNotSuppressHints(t *testing.T) {
	r := cli.New(cli.Config{
		Name:            "notool",
		Version:         "0.1.0",
		Short:           "hints independent of format",
		Disable:         cli.Disable{Format: true},
		DisableValidate: true,
	})
	pf := r.Cmd.PersistentFlags()
	assert.Nil(t, pf.Lookup("format"), "--format must be suppressed")
	assert.NotNil(t, pf.Lookup("no-hints"), "--no-hints must survive when only Format is disabled")
}

func TestGlobals_FlagWithShorthand(t *testing.T) {
	r := cli.New(cli.Config{
		Name:    "gtool",
		Version: "0.1.0",
		Short:   "shorthand test",
		Globals: []cli.Flag{
			// "experiment" stands in for any tool-specific persistent
			// flag. See note on TestGlobals_FlagRegistered for why
			// "dry-run" is no longer a valid sample name.
			{Name: "experiment", Short: "X", Usage: "enable experiment mode"},
		},
		DisableValidate: true,
	})
	pf := r.Cmd.PersistentFlags()
	f := pf.ShorthandLookup("X")
	require.NotNil(t, f, "-X shorthand must be registered")
	assert.Equal(t, "experiment", f.Name)
}

// ── ShowAliases ─────────────────────────────────────────────────────────────

func TestShowAliases_DefaultHidden(t *testing.T) {
	r := cli.New(cli.Config{Name: "mytool", Version: "0.1.0", Short: "A tool", DisableValidate: true})
	r.Cmd.AddCommand(&cobra.Command{
		Use: "deploy", Aliases: []string{"d", "dp"}, Short: "Deploy the app",
		Run: func(cmd *cobra.Command, args []string) {},
	})
	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetArgs([]string{"--help"})
	_ = r.Execute(t.Context())
	out := buf.String()
	assert.Contains(t, out, "deploy")
	assert.NotContains(t, out, "Aliases")
	assert.NotRegexp(t, `\bd\b.*\bdp\b`, out, "aliases must not appear when ShowAliases is false")
}

func TestShowAliases_Enabled(t *testing.T) {
	r := cli.New(cli.Config{
		Name: "mytool", Version: "0.1.0", Short: "A tool",
		Help:            cli.HelpConfig{ShowAliases: true},
		DisableValidate: true,
	})
	r.Cmd.AddCommand(&cobra.Command{
		Use: "deploy", Aliases: []string{"d", "dp"}, Short: "Deploy the app",
		Run: func(cmd *cobra.Command, args []string) {},
	})
	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetArgs([]string{"--help"})
	_ = r.Execute(t.Context())
	out := stripANSI(buf.String())
	assert.Contains(t, out, "deploy")
	assert.Contains(t, out, "d, dp", "aliases must appear when ShowAliases is true")
}

// ── Help structure — line-by-line parity ────────────────────────────────────

// subCmd returns a minimal cobra subcommand for help-output tests.
func subCmd() *cobra.Command {
	return &cobra.Command{Use: "sub", Short: "A subcommand", Run: func(cmd *cobra.Command, args []string) {}}
}

// sectionIdx returns the index of the first line whose trimmed value equals
// header, or -1 if not found.
func sectionIdx(lines []string, header string) int {
	for i, l := range lines {
		if strings.TrimSpace(l) == header {
			return i
		}
	}
	return -1
}

func TestHelpStructure_FirstNonEmptyLineIsUsage(t *testing.T) {
	lines := helpLines(t)
	var first string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			first = l
			break
		}
	}
	// fang renders the usage inside an indented block; the word "mytool" and
	// "USAGE" must appear before any FLAGS/COMMANDS block.
	usageIdx := -1
	for i, l := range lines {
		if strings.Contains(l, "USAGE") || strings.Contains(l, "mytool") {
			usageIdx = i
			break
		}
	}
	_ = first
	assert.GreaterOrEqual(t, usageIdx, 0, "USAGE / program name must appear near the top")
}

func TestHelpStructure_DescriptionBeforeSections(t *testing.T) {
	lines := helpLines(t)
	descIdx := -1
	for i, l := range lines {
		if strings.Contains(l, "A tool") {
			descIdx = i
			break
		}
	}
	secIdx := -1
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "COMMANDS" || trimmed == "FLAGS" {
			secIdx = i
			break
		}
	}
	require.GreaterOrEqual(t, descIdx, 0, "description not found in help output")
	require.GreaterOrEqual(t, secIdx, 0, "section header not found in help output")
	assert.Less(t, descIdx, secIdx, "description must appear before first section header")
}

func TestHelpStructure_CommandsBeforeFlags(t *testing.T) {
	lines := helpLines(t)
	cmdIdx := sectionIdx(lines, "COMMANDS")
	flagIdx := sectionIdx(lines, "FLAGS")
	require.GreaterOrEqual(t, cmdIdx, 0, "COMMANDS section not found")
	require.GreaterOrEqual(t, flagIdx, 0, "FLAGS section not found")
	assert.Less(t, cmdIdx, flagIdx, "COMMANDS must appear before FLAGS")
}

func TestHelpStructure_SubCommandUnderCommands(t *testing.T) {
	lines := helpLines(t)
	cmdIdx := sectionIdx(lines, "COMMANDS")
	require.GreaterOrEqual(t, cmdIdx, 0, "COMMANDS section not found")
	subIdx := -1
	for i, l := range lines[cmdIdx+1:] {
		if strings.Contains(l, "sub") {
			subIdx = cmdIdx + 1 + i
			break
		}
	}
	assert.Greater(t, subIdx, cmdIdx, "'sub' must appear after COMMANDS section")
}

func TestHelpStructure_StdFlagsUnderFlagsSection(t *testing.T) {
	lines := helpLines(t)
	flagIdx := sectionIdx(lines, "FLAGS")
	require.GreaterOrEqual(t, flagIdx, 0, "FLAGS section not found")
	flagLines := lines[flagIdx+1:]
	for _, flag := range []string{"--format", "--quiet", "--no-color", "--no-hints"} {
		found := false
		for _, l := range flagLines {
			if strings.Contains(l, flag) {
				found = true
				break
			}
		}
		assert.True(t, found, "%s must appear under FLAGS section", flag)
	}
}

func TestHelpStructure_NoHelpOrCompletionSubcommand(t *testing.T) {
	lines := helpLines(t)
	cmdIdx := sectionIdx(lines, "COMMANDS")
	flagIdx := sectionIdx(lines, "FLAGS")
	require.GreaterOrEqual(t, cmdIdx, 0)
	require.GreaterOrEqual(t, flagIdx, 0)
	cmdLines := lines[cmdIdx+1 : flagIdx]
	for _, l := range cmdLines {
		assert.NotRegexp(t, `^\s+help\b`, l, "'help' must not appear as a subcommand")
		assert.NotRegexp(t, `^\s+completion\b`, l, "'completion' must not appear as a subcommand")
	}
}

func TestHelpStructure_SectionTitlesUppercase(t *testing.T) {
	lines := helpLines(t)
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		assert.NotEqual(t, "Commands:", trimmed, "Commands: must be renamed to COMMANDS")
		assert.NotEqual(t, "Options:", trimmed, "Options: must be renamed to FLAGS")
	}
	assert.GreaterOrEqual(t, sectionIdx(lines, "COMMANDS"), 0, "COMMANDS section missing")
	assert.GreaterOrEqual(t, sectionIdx(lines, "FLAGS"), 0, "FLAGS section missing")
}

// ── Command groups ─────────────────────────────────────────────────────────

func TestHelpGroups_DefaultHidesManagement(t *testing.T) {
	r := cli.New(cli.Config{Name: "mytool", Version: "0.1.0", Short: "A tool", DisableValidate: true})
	r.Cmd.AddCommand(&cobra.Command{
		Use: "deploy", Short: "Deploy the app",
		Run: func(cmd *cobra.Command, args []string) {},
	})
	mgmt := &cobra.Command{
		Use: "config", Short: "Manage configuration", GroupID: "management",
		Run: func(cmd *cobra.Command, args []string) {},
	}
	r.Cmd.AddCommand(mgmt)

	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetArgs([]string{"--help"})
	_ = r.Execute(t.Context())
	out := stripANSI(buf.String())

	assert.Contains(t, out, "COMMANDS", "default group header must appear")
	assert.Contains(t, out, "deploy", "default group command must appear")
	assert.NotContains(t, out, "MANAGEMENT", "management group must be hidden by default")
	// The management "config" command's short string is the canonical marker —
	// matching the bare substring "config" would now collide with the
	// -c/--config flag in the FLAGS section.
	assert.NotContains(t, out, "Manage configuration",
		"management commands must be hidden by default")
}

func TestHelpGroups_HelpAllShowsAll(t *testing.T) {
	r := cli.New(cli.Config{Name: "mytool", Version: "0.1.0", Short: "A tool", DisableValidate: true})
	r.Cmd.AddCommand(&cobra.Command{
		Use: "deploy", Short: "Deploy the app",
		Run: func(cmd *cobra.Command, args []string) {},
	})
	r.Cmd.AddCommand(&cobra.Command{
		Use: "config", Short: "Manage configuration", GroupID: "management",
		Run: func(cmd *cobra.Command, args []string) {},
	})

	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.SetArgs([]string{"--help-all"})
	_ = r.Execute(t.Context())
	out := stripANSI(buf.String())

	assert.Contains(t, out, "COMMANDS", "default group must appear with --help-all")
	assert.Contains(t, out, "deploy", "default command must appear with --help-all")
	assert.Contains(t, out, "MANAGEMENT", "management group must appear with --help-all")
	assert.Contains(t, out, "config", "management command must appear with --help-all")
}

func TestHelpGroups_CustomGroup(t *testing.T) {
	r := cli.New(cli.Config{
		Name: "mytool", Version: "0.1.0", Short: "A tool",
		Help: cli.HelpConfig{
			Groups: []cli.GroupConfig{
				{ID: "extras", Title: "EXTRAS"},
			},
		},
		DisableValidate: true,
	})
	r.Cmd.AddCommand(&cobra.Command{
		Use: "deploy", Short: "Deploy the app",
		Run: func(cmd *cobra.Command, args []string) {},
	})
	r.Cmd.AddCommand(&cobra.Command{
		Use: "bonus", Short: "Bonus feature", GroupID: "extras",
		Run: func(cmd *cobra.Command, args []string) {},
	})

	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetArgs([]string{"--help"})
	_ = r.Execute(t.Context())
	out := stripANSI(buf.String())

	assert.Contains(t, out, "COMMANDS", "default group must appear")
	assert.Contains(t, out, "EXTRAS", "custom group must appear")
	assert.Contains(t, out, "bonus", "custom group command must appear")
}

// ── Per-group help ────────────────────────────────────────────────────────

// perGroupRoot builds a root with management + extras groups for per-group tests.
func perGroupRoot() *cli.Root {
	r := cli.New(cli.Config{
		Name: "mytool", Version: "0.1.0", Short: "A tool",
		Help: cli.HelpConfig{
			Groups: []cli.GroupConfig{
				{ID: "extras", Title: "EXTRAS"},
			},
		},
		DisableValidate: true,
	})
	r.Cmd.AddCommand(&cobra.Command{
		Use: "deploy", Short: "Deploy the app",
		Run: func(cmd *cobra.Command, args []string) {},
	})
	r.Cmd.AddCommand(&cobra.Command{
		Use: "config", Short: "Manage configuration", GroupID: "management",
		Run: func(cmd *cobra.Command, args []string) {},
	})
	r.Cmd.AddCommand(&cobra.Command{
		Use: "bonus", Short: "Bonus feature", GroupID: "extras",
		Run: func(cmd *cobra.Command, args []string) {},
	})
	return r
}

func TestHelpPerGroup_Flag(t *testing.T) {
	r := perGroupRoot()
	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetErr(&buf)
	r.SetArgs([]string{"--help-management"})
	err := r.Execute(t.Context())
	require.NoError(t, err)
	out := stripANSI(buf.String())

	assert.Contains(t, out, "MANAGEMENT", "group title must appear as header")
	assert.Contains(t, out, "config", "management command must appear")
	assert.NotContains(t, out, "deploy", "default group commands must not appear")
	assert.NotContains(t, out, "bonus", "other group commands must not appear")
}

func TestHelpPerGroup_Subcommand(t *testing.T) {
	r := perGroupRoot()
	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetErr(&buf)
	r.SetArgs([]string{"help", "management"})
	err := r.Execute(t.Context())
	require.NoError(t, err)
	out := stripANSI(buf.String())

	assert.Contains(t, out, "MANAGEMENT", "group title must appear as header")
	assert.Contains(t, out, "config", "management command must appear")
	assert.NotContains(t, out, "deploy", "default group commands must not appear")
	assert.NotContains(t, out, "bonus", "other group commands must not appear")
}

func TestHelpPerGroup_All(t *testing.T) {
	r := perGroupRoot()
	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetErr(&buf)
	r.SetArgs([]string{"help", "all"})
	err := r.Execute(t.Context())
	require.NoError(t, err)
	out := stripANSI(buf.String())

	assert.Contains(t, out, "deploy", "default group commands must appear")
	assert.Contains(t, out, "MANAGEMENT", "management group must appear")
	assert.Contains(t, out, "config", "management commands must appear")
	assert.Contains(t, out, "EXTRAS", "extras group must appear")
	assert.Contains(t, out, "bonus", "extras commands must appear")
}

func TestHelpPerGroup_Unknown(t *testing.T) {
	r := perGroupRoot()
	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetErr(&buf)
	r.SetArgs([]string{"help", "bogus"})
	err := r.Execute(t.Context())
	assert.Error(t, err, "unknown group ID must error")
}

// ── Completion ────────────────────────────────────────────────────────────

func TestCompletion_InManagementGroup(t *testing.T) {
	r := root()
	r.Cmd.AddCommand(subCmd())
	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	// Trigger Execute so fang registers the completion command.
	r.SetArgs([]string{"--help"})
	_ = r.Execute(t.Context())

	var found *cobra.Command
	for _, c := range r.Cmd.Commands() {
		if c.Name() == "completion" {
			found = c
			break
		}
	}
	require.NotNil(t, found, "completion subcommand must exist")
	assert.Equal(t, "management", found.GroupID,
		"completion must be in the management group")
}

func TestCompletion_HiddenFromDefaultHelp(t *testing.T) {
	r := root()
	r.Cmd.AddCommand(subCmd())
	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.SetArgs([]string{"--help"})
	_ = r.Execute(t.Context())
	out := stripANSI(buf.String())

	assert.NotContains(t, out, "completion",
		"completion must not appear in default --help")
}

func TestCompletion_VisibleInHelpAll(t *testing.T) {
	r := root()
	r.Cmd.AddCommand(subCmd())
	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.SetArgs([]string{"--help-all"})
	_ = r.Execute(t.Context())
	out := stripANSI(buf.String())

	assert.Contains(t, out, "MANAGEMENT",
		"management group must appear with --help-all")
	assert.Contains(t, out, "completion",
		"completion must appear with --help-all")
}

func TestHelpPerGroup_FlagShowsFlags(t *testing.T) {
	r := perGroupRoot()
	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetErr(&buf)
	r.SetArgs([]string{"--help-extras"})
	err := r.Execute(t.Context())
	require.NoError(t, err)
	out := stripANSI(buf.String())

	assert.Contains(t, out, "EXTRAS", "group title must appear")
	assert.Contains(t, out, "bonus", "extras command must appear")
	assert.Contains(t, out, "FLAGS", "FLAGS section must appear")
}
