package cli_test

import (
	"bytes"
	"io"
	"runtime"
	"strings"
	"testing"

	"github.com/creack/pty"
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

func TestRootHelp_HasGlobalFlagsSection(t *testing.T) {
	// A hierarchical CLI that configures no help options at all: the
	// root splits like every other command, no opt-in required.
	lines := helpLines(t)
	flagsIdx := findSection(lines, "FLAGS")
	globalIdx := findSection(lines, "GLOBAL FLAGS")
	require.GreaterOrEqual(t, flagsIdx, 0,
		"root --help must still render FLAGS, got:\n%s", strings.Join(lines, "\n"))
	require.GreaterOrEqual(t, globalIdx, 0,
		"root --help must show a GLOBAL FLAGS section by default, got:\n%s",
		strings.Join(lines, "\n"))
	require.Less(t, flagsIdx, globalIdx, "FLAGS must come before GLOBAL FLAGS")

	flagsBody := strings.Join(linesBetween(lines, flagsIdx, globalIdx), "\n")
	globalBody := strings.Join(lines[globalIdx+1:], "\n")
	// Kit's persistent globals move out of the root's FLAGS block.
	for _, want := range []string{"--format", "--no-color", "--quiet", "-V --verbose"} {
		assert.Contains(t, globalBody, want,
			"%q must appear under GLOBAL FLAGS, body was:\n%s", want, globalBody)
		assert.NotContains(t, flagsBody, want,
			"%q must not remain under FLAGS, body was:\n%s", want, flagsBody)
	}
	// The root's own non-persistent flags stay put.
	assert.Contains(t, flagsBody, "-h --help")
	assert.Contains(t, flagsBody, "-v --version")
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

// rootSplitHelp builds a single-command binary — a root with its own
// local flags and no subcommand worth speaking of — and returns its
// root --help, split into stripped lines. extraArgs are appended
// before --help.
func rootSplitHelp(t *testing.T, extraArgs ...string) []string {
	t.Helper()

	r := cli.New(cli.Config{
		Name:    "sidecar",
		Version: "1.2.3",
		Short:   "A single-command tool",
		Globals: []cli.Flag{{
			Name:  "global-thing",
			Usage: "Tool-wide global flag",
		}},
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

func TestRootSplit_HasGlobalFlagsSection(t *testing.T) {
	lines := rootSplitHelp(t)
	assert.GreaterOrEqual(t, findSection(lines, "GLOBAL FLAGS"), 0,
		"root --help must contain a GLOBAL FLAGS section, got:\n%s",
		strings.Join(lines, "\n"))
}

func TestRootSplit_LocalFlagsStayUnderFlags(t *testing.T) {
	lines := rootSplitHelp(t)
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

func TestRootSplit_GlobalsMoveOutOfFlags(t *testing.T) {
	lines := rootSplitHelp(t)
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

func TestRootSplit_HiddenDefaultsSurface(t *testing.T) {
	// Kit-owned plumbing flags are Hidden=true so default --help's FLAGS
	// matches the parity contract; the GLOBAL FLAGS section is where
	// they are meant to show, exactly as on a leaf.
	lines := rootSplitHelp(t)
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

func TestRootSplit_HelpAllStillSplits(t *testing.T) {
	// --help-all unhides the plumbing flags and re-dispatches --help;
	// the split must survive that rewrite rather than falling back to
	// one flat block.
	lines := rootSplitHelp(t, "--help-all")
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

func TestRootSplit_WithSubcommands(t *testing.T) {
	// A plugin may carry a subcommand (foo-youtube has `status`).
	// The root splits and the subcommand keeps splitting: same shape at
	// every depth, and the subcommand's own flag stays local to it.
	r := cli.New(cli.Config{
		Name:            "sidecar",
		Version:         "1.2.3",
		Short:           "A single-command tool",
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
	require.GreaterOrEqual(t, rootGlobal, 0, "root must split by default")
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

// ── GLOBAL FLAGS color ───────────────────────────────────────────────────
//
// The GLOBAL FLAGS heading is the one styled thing kit renders itself;
// everything else in help comes from fang, which writes through a
// colorprofile writer that strips escapes when color is off. These tests
// pin that kit's own section goes through the same writer.
//
// Coverage deliberately targets a LEAF command. TestParityHelpNoColor
// (parity_test.go) only ever invokes ROOT help, and the root had no
// GLOBAL FLAGS section until SplitGlobals — so the single place this
// escape is emitted went uninspected under --no-color. A root-only
// assertion would leave that same gap open.

// globalFlagsHeading returns the raw (unstripped) GLOBAL FLAGS heading
// line from the given help output, or "" when the section is absent.
func globalFlagsHeading(out string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(stripANSI(l), "GLOBAL FLAGS") {
			return l
		}
	}
	return ""
}

// rawLeafHelp runs `<root> sub --help` into a plain buffer and returns the
// unstripped output. A bytes.Buffer is not a terminal, so the colorprofile
// writer resolves to NoTTY and every escape must already be gone.
func rawLeafHelp(t *testing.T, args ...string) string {
	t.Helper()

	r := cli.New(cli.Config{
		Name:            "mytool",
		Version:         "1.2.3",
		Short:           "A test tool",
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
	r.SetArgs(append(append([]string{"sub"}, args...), "--help"))
	require.NoError(t, r.Execute(t.Context()))
	return buf.String()
}

// TestLeafHelp_GlobalFlagsNoANSI_NonTTY pins the non-terminal path: piped
// leaf help must be escape-free with or without --no-color. This is the
// shape the parity harness sees — it captures into a buffer too.
func TestLeafHelp_GlobalFlagsNoANSI_NonTTY(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"plain", nil},
		{"no-color", []string{"--no-color"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := rawLeafHelp(t, tc.args...)

			heading := globalFlagsHeading(out)
			require.NotEmpty(t, heading,
				"leaf help must contain a GLOBAL FLAGS section")
			assert.False(t, hasANSI(heading),
				"GLOBAL FLAGS heading must not carry ANSI escapes off a terminal\ngot: %q", heading)
			assert.False(t, hasANSI(out),
				"leaf help must not contain ANSI escapes off a terminal\ngot: %q", out)
		})
	}
}

// ttyLeafHelp runs `<root> sub --help` with output on a real pseudo-terminal
// and returns everything written to it.
//
// A pty, not a buffer: the colorprofile writer strips on any non-terminal
// regardless of --no-color, so a buffer cannot tell "the flag was honored"
// from "there was no terminal to color". Only a real terminal writer
// distinguishes the two — the same reason the styled-table e2e tests
// allocate one.
func ttyLeafHelp(t *testing.T, args ...string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("creack/pty is unix-only")
	}

	master, slave, err := pty.Open()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = master.Close()
		_ = slave.Close()
	})

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, master)
		done <- buf.String()
	}()

	r := cli.New(cli.Config{
		Name:            "mytool",
		Version:         "1.2.3",
		Short:           "A test tool",
		DisableValidate: true,
	})
	leaf := &cobra.Command{
		Use:   "sub",
		Short: "A subcommand",
		Run:   func(cmd *cobra.Command, args []string) {},
	}
	leaf.Flags().Bool("thing", false, "Subcommand-only flag")
	r.Cmd.AddCommand(leaf)

	r.Cmd.SetOut(slave)
	r.SetArgs(append(append([]string{"sub"}, args...), "--help"))
	require.NoError(t, r.Execute(t.Context()))
	require.NoError(t, slave.Close())

	return <-done
}

// TestLeafHelp_GlobalFlagsHeadingStyledOnTTY is the control: on a real
// terminal, with color on, the heading keeps its bold. Without this the
// fix could "pass" by never styling anything.
func TestLeafHelp_GlobalFlagsHeadingStyledOnTTY(t *testing.T) {
	t.Setenv("CLICOLOR_FORCE", "1")
	t.Setenv("NO_COLOR", "")

	heading := globalFlagsHeading(ttyLeafHelp(t))
	require.NotEmpty(t, heading, "leaf help must contain a GLOBAL FLAGS section")
	assert.True(t, hasANSI(heading),
		"GLOBAL FLAGS heading must stay styled on a color terminal\ngot: %q", heading)
}

// TestLeafHelp_GlobalFlagsHeadingPlainOnTTY_NoColorFlag is the regression
// proper: --no-color on a color-capable terminal must strip the heading.
// The env profile cannot see the flag, so this is what proves the clamp in
// helpColorWriter rather than the ambient environment.
func TestLeafHelp_GlobalFlagsHeadingPlainOnTTY_NoColorFlag(t *testing.T) {
	t.Setenv("CLICOLOR_FORCE", "1")
	t.Setenv("NO_COLOR", "")

	heading := globalFlagsHeading(ttyLeafHelp(t, "--no-color"))
	require.NotEmpty(t, heading, "leaf help must contain a GLOBAL FLAGS section")
	assert.False(t, hasANSI(heading),
		"--no-color must strip the GLOBAL FLAGS heading's escapes\ngot: %q", heading)
}

// TestLeafHelp_GlobalFlagsHeadingMatchesFangUnderNoColorEnv pins the
// environment convention as an equivalence rather than an absolute.
//
// NO_COLOR on a terminal resolves to colorprofile.ASCII, which strips
// colors but keeps attributes — only NoTTY strips bold. So the heading
// legitimately stays bold here, exactly as fang's own USAGE and FLAGS
// titles do. Asserting "no escapes" would demand behavior the shared
// writer does not provide and would have this section diverge from the
// rest of help; the contract worth pinning is that it does not.
func TestLeafHelp_GlobalFlagsHeadingMatchesFangUnderNoColorEnv(t *testing.T) {
	t.Setenv("CLICOLOR_FORCE", "")
	t.Setenv("NO_COLOR", "1")

	out := ttyLeafHelp(t)
	heading := globalFlagsHeading(out)
	require.NotEmpty(t, heading, "leaf help must contain a GLOBAL FLAGS section")

	var fangTitle string
	for _, l := range strings.Split(out, "\n") {
		if strings.TrimSpace(stripANSI(l)) == "FLAGS" {
			fangTitle = l
			break
		}
	}
	require.NotEmpty(t, fangTitle, "leaf help must contain a FLAGS section")

	assert.Equal(t, hasANSI(fangTitle), hasANSI(heading),
		"GLOBAL FLAGS heading must style like fang's own titles under NO_COLOR\nfang: %q\nours: %q",
		fangTitle, heading)

	// Whatever the attributes, no color may survive NO_COLOR.
	assert.NotRegexp(t, `\x1b\[[0-9;]*(3[0-79]|4[0-79]|9[0-7]|10[0-7]|38;|48;)`, heading,
		"NO_COLOR must leave no color in the GLOBAL FLAGS heading\ngot: %q", heading)
}

// rawSplitGlobalsHelp mirrors splitGlobalsHelp but returns the unstripped
// output — that helper strips ANSI, which would make an escape assertion
// vacuous.
func rawSplitGlobalsHelp(t *testing.T, extraArgs ...string) string {
	t.Helper()

	r := cli.New(cli.Config{
		Name:            "sidecar",
		Version:         "1.2.3",
		Short:           "A single-command tool",
		DisableValidate: true,
	})
	r.Cmd.Flags().Bool("comments", false, "Include top comments")
	r.Cmd.Run = func(cmd *cobra.Command, args []string) {}

	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.SetArgs(append(append([]string{}, extraArgs...), "--help"))
	require.NoError(t, r.Execute(t.Context()))
	return buf.String()
}

// TestRootHelp_SplitGlobals_NoANSIUnderNoColor extends the same guarantee
// to the root's own split section, the surface TestParityHelpNoColor
// exercises once SplitGlobals is on.
func TestRootHelp_SplitGlobals_NoANSIUnderNoColor(t *testing.T) {
	out := rawSplitGlobalsHelp(t, "--no-color")
	require.NotEmpty(t, globalFlagsHeading(out),
		"root must split with SplitGlobals")
	assert.False(t, hasANSI(out),
		"root help must not contain ANSI escapes under --no-color\ngot: %q", out)
}
