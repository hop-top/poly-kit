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
