package cli_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
)

// ── Compact GLOBAL FLAGS ─────────────────────────────────────────────────
//
// Default help lists the tool's own globals plus kit's core set; kit's
// plumbing globals collapse into one hint line naming --help-all, which
// expands the full list.

// kitCore are the kit globals default help keeps visible.
var kitCore = []string{"--format", "-o --output", "--quiet", "-V --verbose", "--no-color", "--no-hints", "--offline"}

// kitCollapsed are kit globals default help folds behind --help-all.
var kitCollapsed = []string{
	"-C --chdir", "-c --config", "--dry-run", "--policy", "--max-ops",
	"--api-version", "--confirm", "--cols",
	"--template", "--format-opt", "--autocorrect", "--progress-format",
	"--columns", "--format-help",
}

// globalsHelp runs args against a fresh root carrying a --global-thing
// tool global, a root-local --comments flag and a `sub` leaf, and
// returns the stripped output.
func globalsHelp(t *testing.T, help cli.HelpConfig, args ...string) string {
	t.Helper()
	r := cli.New(cli.Config{
		Name:            "mytool",
		Version:         "1.2.3",
		Short:           "A test tool",
		Globals:         []cli.Flag{{Name: "global-thing", Usage: "Tool-wide global flag"}},
		Help:            help,
		DisableValidate: true,
	})
	r.Cmd.Flags().Bool("comments", false, "Include top comments")
	r.Cmd.AddCommand(&cobra.Command{
		Use:   "sub",
		Short: "A subcommand",
		Run:   func(cmd *cobra.Command, args []string) {},
	})
	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.SetArgs(args)
	require.NoError(t, r.Execute(t.Context()))
	return stripANSI(buf.String())
}

// sectionBody returns the text after header up to the next header in
// headers order (or end of output).
func sectionBody(t *testing.T, out, header, next string) string {
	t.Helper()
	lines := strings.Split(out, "\n")
	start := findSection(lines, header)
	require.GreaterOrEqual(t, start, 0, "%s section missing:\n%s", header, out)
	end := len(lines)
	if next != "" {
		if i := findSection(lines, next); i > start {
			end = i
		}
	}
	return strings.Join(lines[start+1:end], "\n")
}

// moreHint is the line collapsed help ends GLOBAL FLAGS with.
func moreHint(n int, path string) string {
	noun := "flags"
	if n == 1 {
		noun = "flag"
	}
	return fmt.Sprintf("+%d more global %s — run `%s --help-all` to list them", n, noun, path)
}

func TestGlobalFlags_DefaultShowsCoreAndToolGlobals(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		path string
	}{
		{"root", []string{"--help"}, "mytool"},
		{"leaf", []string{"sub", "--help"}, "mytool sub"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := globalsHelp(t, cli.HelpConfig{}, tc.args...)
			body := sectionBody(t, out, "GLOBAL FLAGS", "")
			for _, want := range append(append([]string{}, kitCore...), "--global-thing") {
				assert.Contains(t, body, want, "core/tool global %q must show by default", want)
			}
			for _, banned := range kitCollapsed {
				assert.NotContains(t, body, banned, "plumbing global %q must collapse by default", banned)
			}
			assert.Contains(t, body, moreHint(len(kitCollapsed), tc.path),
				"collapsed globals must be counted in one hint naming --help-all")
		})
	}
}

func TestGlobalFlags_HelpAllExpandsEverything(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"root", []string{"--help-all"}},
		{"leaf", []string{"sub", "--help-all"}},
		{"help all", []string{"help", "all"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := globalsHelp(t, cli.HelpConfig{}, tc.args...)
			body := sectionBody(t, out, "GLOBAL FLAGS", "")
			for _, want := range append(append(append([]string{}, kitCore...), kitCollapsed...), "--global-thing") {
				assert.Contains(t, body, want, "--help-all must list %q", want)
			}
			assert.NotContains(t, body, "more global", "no hint once everything is listed")
		})
	}
}

func TestGlobalFlags_HelpAllDoesNotLeakIntoNextExecute(t *testing.T) {
	r := cli.New(cli.Config{Name: "mytool", Version: "1.2.3", Short: "A test tool", DisableValidate: true})
	r.Cmd.AddCommand(&cobra.Command{Use: "sub", Short: "A subcommand", Run: func(*cobra.Command, []string) {}})
	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)

	r.SetArgs([]string{"sub", "--help-all"})
	require.NoError(t, r.Execute(t.Context()))
	require.Contains(t, stripANSI(buf.String()), "--dry-run")

	buf.Reset()
	r.SetArgs([]string{"sub", "--help"})
	require.NoError(t, r.Execute(t.Context()))
	body := sectionBody(t, stripANSI(buf.String()), "GLOBAL FLAGS", "")
	assert.NotContains(t, body, "--dry-run", "a later plain --help must collapse again")
	assert.Contains(t, body, "more global flags")
}

func TestGlobalFlags_ShowAndCollapseOverrides(t *testing.T) {
	out := globalsHelp(t, cli.HelpConfig{
		ShowGlobals:     []string{"dry-run"},
		CollapseGlobals: []string{"global-thing", "quiet"},
	}, "sub", "--help")
	body := sectionBody(t, out, "GLOBAL FLAGS", "")
	assert.Contains(t, body, "--dry-run", "ShowGlobals promotes a kit global")
	assert.NotContains(t, body, "--global-thing", "CollapseGlobals demotes a tool global")
	assert.NotContains(t, body, "--quiet", "CollapseGlobals demotes a kit core global")
	// Kit plumbing, minus dry-run, plus global-thing and quiet.
	assert.Contains(t, body, moreHint(len(kitCollapsed)-1+2, "mytool sub"))
}

func TestGlobalFlags_NoHintWhenNothingCollapsed(t *testing.T) {
	var all []string
	for _, f := range kitCollapsed {
		all = append(all, strings.TrimPrefix(f[strings.LastIndex(f, " ")+1:], "--"))
	}
	out := globalsHelp(t, cli.HelpConfig{ShowGlobals: all}, "sub", "--help")
	assert.NotContains(t, sectionBody(t, out, "GLOBAL FLAGS", ""), "more global")
}

// ── Collapsed --help-<group> row ─────────────────────────────────────────

func TestGroupHelpFlags_CollapseToOneRow(t *testing.T) {
	out := globalsHelp(t, cli.HelpConfig{Groups: []cli.GroupConfig{
		{ID: "capture", Title: "CAPTURE"},
		{ID: "compose", Title: "COMPOSE"},
	}}, "--help")
	flags := sectionBody(t, out, "FLAGS", "GLOBAL FLAGS")
	assert.Contains(t, flags, "--help-<group>")
	assert.Contains(t, flags, "capture, compose, management",
		"the one row names every group, sorted")
	for _, banned := range []string{"--help-capture", "--help-compose", "--help-management"} {
		assert.NotContains(t, flags, banned, "per-group row %q folds into --help-<group>", banned)
	}
	assert.Contains(t, flags, "--help-all")
	assert.Contains(t, flags, "--comments")
}

func TestGroupHelpFlags_SingleGroupKeepsItsRow(t *testing.T) {
	out := globalsHelp(t, cli.HelpConfig{}, "--help")
	flags := sectionBody(t, out, "FLAGS", "GLOBAL FLAGS")
	assert.Contains(t, flags, "--help-management")
	assert.NotContains(t, flags, "--help-<group>")
}

func TestGroupHelpFlags_PerGroupFlagStillWorks(t *testing.T) {
	r := cli.New(cli.Config{
		Name: "mytool", Version: "1.2.3", Short: "A test tool", DisableValidate: true,
		Help: cli.HelpConfig{Groups: []cli.GroupConfig{{ID: "capture", Title: "CAPTURE"}}},
	})
	r.Cmd.AddCommand(&cobra.Command{Use: "grab", Short: "Grab it", GroupID: "capture", Run: func(*cobra.Command, []string) {}})
	r.Cmd.AddCommand(&cobra.Command{Use: "other", Short: "Other thing", Run: func(*cobra.Command, []string) {}})
	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)

	// Twice on one Root: the render-time row must not be re-registered.
	for range 2 {
		buf.Reset()
		r.SetArgs([]string{"--help-capture"})
		require.NoError(t, r.Execute(t.Context()))
		out := stripANSI(buf.String())
		assert.Contains(t, out, "grab")
		assert.NotContains(t, out, "other")
		assert.Contains(t, out, "--help-<group>")
	}
}
