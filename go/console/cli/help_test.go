package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
)

// leafHelp builds a root with a leaf subcommand carrying a local --thing flag,
// runs `<root> sub --help`, returns the stripped output split into lines.
//
// The leaf has its own --thing flag plus a global registered via
// cfg.Globals. Built-in persistents (--format, --no-color, …) come from
// kit's defaults.
func leafHelp(t *testing.T, args ...string) []string {
	t.Helper()

	r := cli.New(cli.Config{
		Name:    "mytool",
		Version: "1.2.3",
		Short:   "A test tool",
		Globals: []cli.Flag{{
			Name:  "global-thing",
			Usage: "Tool-wide global flag",
		}},
		DisableValidate: true,
	})

	leaf := &cobra.Command{
		Use:   "sub",
		Short: "A subcommand",
		Run:   func(cmd *cobra.Command, args []string) {},
	}
	leaf.Flags().Bool("thing", false, "Subcommand-only flag")
	r.Cmd.AddCommand(leaf)

	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	full := append([]string{"sub"}, args...)
	r.SetArgs(append(full, "--help"))
	require.NoError(t, r.Execute(t.Context()))
	return strings.Split(stripANSI(buf.String()), "\n")
}

// findSection returns the line index whose trimmed content equals header,
// or -1 if not found.
func findSection(lines []string, header string) int {
	for i, l := range lines {
		if strings.TrimSpace(l) == header {
			return i
		}
	}
	return -1
}

// linesBetween returns the lines after `start` up to (but excluding) the
// next non-empty section header at indentation depth 0 — i.e. the body of
// the section starting at `start`.
func linesBetween(lines []string, start, end int) []string {
	if start < 0 || end <= start || end > len(lines) {
		return nil
	}
	return lines[start+1 : end]
}

func TestLeafHelp_HasGlobalFlagsSection(t *testing.T) {
	lines := leafHelp(t)
	idx := findSection(lines, "GLOBAL FLAGS")
	assert.GreaterOrEqual(t, idx, 0,
		"leaf help must contain a GLOBAL FLAGS section")
}

func TestLeafHelp_LocalFlagUnderFlags(t *testing.T) {
	lines := leafHelp(t)
	flagsIdx := findSection(lines, "FLAGS")
	globalIdx := findSection(lines, "GLOBAL FLAGS")
	require.GreaterOrEqual(t, flagsIdx, 0, "FLAGS section missing")
	require.GreaterOrEqual(t, globalIdx, 0, "GLOBAL FLAGS section missing")
	require.Less(t, flagsIdx, globalIdx, "FLAGS must come before GLOBAL FLAGS")

	body := linesBetween(lines, flagsIdx, globalIdx)
	found := false
	for _, l := range body {
		if strings.Contains(l, "--thing") && !strings.Contains(l, "global-thing") {
			found = true
			break
		}
	}
	assert.True(t, found,
		"--thing must appear under FLAGS section, body was:\n%s",
		strings.Join(body, "\n"))
}

func TestLeafHelp_GlobalFlagsUnderGlobalFlags(t *testing.T) {
	lines := leafHelp(t)
	globalIdx := findSection(lines, "GLOBAL FLAGS")
	require.GreaterOrEqual(t, globalIdx, 0, "GLOBAL FLAGS section missing")
	body := lines[globalIdx+1:]

	// Inherited persistents that must surface under GLOBAL FLAGS.
	for _, want := range []string{
		"--format", "--no-color", "--no-hints", "--quiet",
		"-V --verbose", "-C --chdir", "--global-thing",
	} {
		found := false
		for _, l := range body {
			if strings.Contains(l, want) {
				found = true
				break
			}
		}
		assert.True(t, found,
			"%q must appear under GLOBAL FLAGS, body was:\n%s",
			want, strings.Join(body, "\n"))
	}
}

func TestLeafHelp_GlobalFlagsNotInFlagsSection(t *testing.T) {
	lines := leafHelp(t)
	flagsIdx := findSection(lines, "FLAGS")
	globalIdx := findSection(lines, "GLOBAL FLAGS")
	require.GreaterOrEqual(t, flagsIdx, 0)
	require.GreaterOrEqual(t, globalIdx, 0)

	body := linesBetween(lines, flagsIdx, globalIdx)
	// The persistent --format and --quiet must NOT show up under FLAGS.
	for _, banned := range []string{"--format", "--quiet", "--no-color", "--global-thing"} {
		for _, l := range body {
			assert.NotContains(t, l, banned,
				"%q must not appear under FLAGS (it is inherited)", banned)
		}
	}
}

func TestLeafHelp_LocalFlagNotInGlobalFlags(t *testing.T) {
	lines := leafHelp(t)
	globalIdx := findSection(lines, "GLOBAL FLAGS")
	require.GreaterOrEqual(t, globalIdx, 0)
	body := lines[globalIdx+1:]
	for _, l := range body {
		// --thing is local to the leaf and must not appear under GLOBAL FLAGS.
		// (--global-thing IS allowed and is filtered separately above.)
		if strings.Contains(l, "--thing") && !strings.Contains(l, "--global-thing") {
			t.Fatalf("--thing must not appear under GLOBAL FLAGS, line: %q", l)
		}
	}
}

func TestRootHelp_NoGlobalFlagsSection(t *testing.T) {
	// Root help renders all flags under FLAGS; GLOBAL FLAGS is leaf-only.
	lines := helpLines(t)
	assert.Equal(t, -1, findSection(lines, "GLOBAL FLAGS"),
		"root --help must not show a GLOBAL FLAGS section")
	assert.GreaterOrEqual(t, findSection(lines, "FLAGS"), 0,
		"root --help must still render FLAGS")
}

func TestNestedLeafHelp_HasGlobalFlagsSection(t *testing.T) {
	// Deep nesting: root > parent > child. Child help must still split
	// inherited persistents into GLOBAL FLAGS.
	r := cli.New(cli.Config{Name: "mytool", Version: "0.1.0", Short: "A tool", DisableValidate: true})
	parent := &cobra.Command{Use: "parent", Short: "Parent group"}
	parent.PersistentFlags().String("parent-only", "", "Parent persistent flag")
	child := &cobra.Command{
		Use:   "child",
		Short: "Child leaf",
		Run:   func(cmd *cobra.Command, args []string) {},
	}
	child.Flags().Bool("child-thing", false, "Child-only flag")
	parent.AddCommand(child)
	r.Cmd.AddCommand(parent)

	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.SetArgs([]string{"parent", "child", "--help"})
	require.NoError(t, r.Execute(t.Context()))

	lines := strings.Split(stripANSI(buf.String()), "\n")
	flagsIdx := findSection(lines, "FLAGS")
	globalIdx := findSection(lines, "GLOBAL FLAGS")
	require.GreaterOrEqual(t, flagsIdx, 0, "FLAGS section missing")
	require.GreaterOrEqual(t, globalIdx, 0, "GLOBAL FLAGS section missing")

	flagsBody := linesBetween(lines, flagsIdx, globalIdx)
	globalBody := lines[globalIdx+1:]

	// Child's local flag under FLAGS.
	foundLocal := false
	for _, l := range flagsBody {
		if strings.Contains(l, "--child-thing") {
			foundLocal = true
			break
		}
	}
	assert.True(t, foundLocal, "--child-thing must appear under FLAGS")

	// Parent's persistent under GLOBAL FLAGS.
	foundParent := false
	for _, l := range globalBody {
		if strings.Contains(l, "--parent-only") {
			foundParent = true
			break
		}
	}
	assert.True(t, foundParent, "--parent-only must appear under GLOBAL FLAGS")
}

// splitGlobalsHelp builds a single-command binary — a root with its own
// local flags and no subcommand worth speaking of — and returns its
// root --help, split into stripped lines. splitGlobals selects the
// opt-in; extraArgs are appended before --help.
func splitGlobalsHelp(t *testing.T, splitGlobals bool, extraArgs ...string) []string {
	t.Helper()

	r := cli.New(cli.Config{
		Name:    "sidecar",
		Version: "1.2.3",
		Short:   "A single-command tool",
		Globals: []cli.Flag{{
			Name:  "global-thing",
			Usage: "Tool-wide global flag",
		}},
		Help:            cli.HelpConfig{SplitGlobals: splitGlobals},
		DisableValidate: true,
	})
	// Root-local flags: what a single-command binary actually does.
	r.Cmd.Flags().Bool("comments", false, "Include top comments")
	r.Cmd.Flags().Bool("timestamps", false, "Include timestamps in transcript")
	r.Cmd.Run = func(cmd *cobra.Command, args []string) {}

	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.SetArgs(append(append([]string{}, extraArgs...), "--help"))
	require.NoError(t, r.Execute(t.Context()))
	return strings.Split(stripANSI(buf.String()), "\n")
}

func TestRootHelp_SplitGlobals_HasGlobalFlagsSection(t *testing.T) {
	lines := splitGlobalsHelp(t, true)
	assert.GreaterOrEqual(t, findSection(lines, "GLOBAL FLAGS"), 0,
		"root --help with SplitGlobals must contain a GLOBAL FLAGS section, got:\n%s",
		strings.Join(lines, "\n"))
}

func TestRootHelp_SplitGlobalsOff_NoGlobalFlagsSection(t *testing.T) {
	// Same CLI, opt-in withheld: today's flat FLAGS block, unchanged.
	lines := splitGlobalsHelp(t, false)
	assert.Equal(t, -1, findSection(lines, "GLOBAL FLAGS"),
		"root --help without SplitGlobals must not show a GLOBAL FLAGS section")
	assert.GreaterOrEqual(t, findSection(lines, "FLAGS"), 0,
		"root --help must still render FLAGS")
}

func TestRootHelp_SplitGlobals_LocalFlagsStayUnderFlags(t *testing.T) {
	lines := splitGlobalsHelp(t, true)
	flagsIdx := findSection(lines, "FLAGS")
	globalIdx := findSection(lines, "GLOBAL FLAGS")
	require.GreaterOrEqual(t, flagsIdx, 0, "FLAGS section missing")
	require.GreaterOrEqual(t, globalIdx, 0, "GLOBAL FLAGS section missing")
	require.Less(t, flagsIdx, globalIdx, "FLAGS must come before GLOBAL FLAGS")

	body := linesBetween(lines, flagsIdx, globalIdx)
	joined := strings.Join(body, "\n")
	// The tool's own flags, plus the root-local help/version family,
	// are what a root's FLAGS section is for.
	for _, want := range []string{
		"--comments", "--timestamps", "-h --help", "-v --version",
		"--help-all", "--help-management",
	} {
		assert.Contains(t, joined, want,
			"%q must stay under FLAGS on the root, body was:\n%s", want, joined)
	}
}

func TestRootHelp_SplitGlobals_GlobalsMoveOutOfFlags(t *testing.T) {
	lines := splitGlobalsHelp(t, true)
	flagsIdx := findSection(lines, "FLAGS")
	globalIdx := findSection(lines, "GLOBAL FLAGS")
	require.GreaterOrEqual(t, flagsIdx, 0)
	require.GreaterOrEqual(t, globalIdx, 0)

	flagsBody := strings.Join(linesBetween(lines, flagsIdx, globalIdx), "\n")
	globalBody := strings.Join(lines[globalIdx+1:], "\n")

	for _, want := range []string{
		"--format", "--no-color", "--no-hints", "--quiet",
		"-V --verbose", "-C --chdir", "--global-thing",
	} {
		assert.Contains(t, globalBody, want,
			"%q must appear under GLOBAL FLAGS, body was:\n%s", want, globalBody)
		assert.NotContains(t, flagsBody, want,
			"%q must not remain under FLAGS once split, body was:\n%s", want, flagsBody)
	}

	// Root-local flags must not leak into the globals section.
	for _, banned := range []string{"--comments", "--timestamps", "--help-all"} {
		assert.NotContains(t, globalBody, banned,
			"%q is root-local and must not appear under GLOBAL FLAGS", banned)
	}
}

func TestRootHelp_SplitGlobals_HiddenDefaultsSurface(t *testing.T) {
	// Kit-owned plumbing flags are Hidden=true so default --help's FLAGS
	// matches the parity contract; the GLOBAL FLAGS section is where
	// they are meant to show, exactly as on a leaf.
	lines := splitGlobalsHelp(t, true)
	globalIdx := findSection(lines, "GLOBAL FLAGS")
	require.GreaterOrEqual(t, globalIdx, 0)
	body := strings.Join(lines[globalIdx+1:], "\n")
	for _, want := range []string{
		"-C --chdir", "-c --config", "--dry-run", "--policy",
		"--max-ops", "--api-version", "--confirm",
	} {
		assert.Contains(t, body, want,
			"hidden-default %q must surface under GLOBAL FLAGS, body was:\n%s", want, body)
	}
}

func TestRootHelp_SplitGlobals_HelpAllStillSplits(t *testing.T) {
	// --help-all unhides the plumbing flags and re-dispatches --help;
	// the split must survive that rewrite rather than falling back to
	// one flat block.
	lines := splitGlobalsHelp(t, true, "--help-all")
	flagsIdx := findSection(lines, "FLAGS")
	globalIdx := findSection(lines, "GLOBAL FLAGS")
	require.GreaterOrEqual(t, globalIdx, 0,
		"--help-all must keep the GLOBAL FLAGS section, got:\n%s",
		strings.Join(lines, "\n"))
	flagsBody := strings.Join(linesBetween(lines, flagsIdx, globalIdx), "\n")
	assert.Contains(t, flagsBody, "--comments", "root-local flag stays under FLAGS")
	assert.NotContains(t, flagsBody, "--dry-run",
		"revealed plumbing flag must move to GLOBAL FLAGS, not stay in FLAGS")
	assert.Contains(t, strings.Join(lines[globalIdx+1:], "\n"), "--dry-run")
}

func TestRootHelp_SplitGlobals_WithSubcommands(t *testing.T) {
	// A plugin may carry a subcommand (foo-youtube has `status`).
	// The root splits and the subcommand keeps splitting: same shape at
	// every depth, and the subcommand's own flag stays local to it.
	r := cli.New(cli.Config{
		Name:            "sidecar",
		Version:         "1.2.3",
		Short:           "A single-command tool",
		Help:            cli.HelpConfig{SplitGlobals: true},
		DisableValidate: true,
	})
	r.Cmd.Flags().Bool("comments", false, "Include top comments")
	status := &cobra.Command{
		Use:   "status",
		Short: "Show status",
		Run:   func(cmd *cobra.Command, args []string) {},
	}
	status.Flags().Bool("watch", false, "Status-only flag")
	r.Cmd.AddCommand(status)

	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.SetArgs([]string{"--help"})
	require.NoError(t, r.Execute(t.Context()))
	rootLines := strings.Split(stripANSI(buf.String()), "\n")

	rootFlags := findSection(rootLines, "FLAGS")
	rootGlobal := findSection(rootLines, "GLOBAL FLAGS")
	require.GreaterOrEqual(t, rootGlobal, 0, "root must split with SplitGlobals")
	rootFlagsBody := strings.Join(linesBetween(rootLines, rootFlags, rootGlobal), "\n")
	assert.Contains(t, rootFlagsBody, "--comments")
	assert.NotContains(t, rootFlagsBody, "--format")
	// A subcommand's own flag belongs to the subcommand, never to the
	// root's FLAGS section.
	assert.NotContains(t, rootFlagsBody, "--watch")

	buf.Reset()
	r.SetArgs([]string{"status", "--help"})
	require.NoError(t, r.Execute(t.Context()))
	subLines := strings.Split(stripANSI(buf.String()), "\n")
	subFlags := findSection(subLines, "FLAGS")
	subGlobal := findSection(subLines, "GLOBAL FLAGS")
	require.GreaterOrEqual(t, subGlobal, 0, "subcommand keeps its split")
	subFlagsBody := strings.Join(linesBetween(subLines, subFlags, subGlobal), "\n")
	assert.Contains(t, subFlagsBody, "--watch")
	assert.NotContains(t, subFlagsBody, "--format")
	assert.Contains(t, strings.Join(subLines[subGlobal+1:], "\n"), "--format")
}
