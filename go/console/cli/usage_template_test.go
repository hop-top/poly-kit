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

// Adopters that run r.Cmd.Execute() instead of r.Execute() get cobra's
// help renderer, not fang's. ApplyGroupVisibility is documented for that
// path; these tests pin how kit's command groups render through it.

// groupedRoot builds a root with one default-group command, one
// management command, an "extras" group with one command, an "empty"
// group with no commands, and a "ghost" group whose only command is
// hidden.
func groupedRoot() *cli.Root {
	r := cli.New(cli.Config{
		Name: "mytool", Version: "0.1.0", Short: "A tool",
		Help: cli.HelpConfig{
			Groups: []cli.GroupConfig{
				{ID: "extras", Title: "EXTRAS"},
				{ID: "empty", Title: "EMPTY"},
				{ID: "ghost", Title: "GHOST"},
			},
		},
		DisableValidate: true,
	})
	run := func(*cobra.Command, []string) {}
	r.Cmd.AddCommand(
		&cobra.Command{Use: "deploy", Short: "Deploy the app", Run: run},
		&cobra.Command{Use: "config", Short: "Manage configuration", GroupID: "management", Run: run},
		&cobra.Command{Use: "bonus", Short: "Bonus feature", GroupID: "extras", Run: run},
		&cobra.Command{Use: "spook", Short: "Hidden feature", GroupID: "ghost", Hidden: true, Run: run},
	)
	return r
}

// cobraHelp renders help the way an adopter bypassing r.Execute does.
func cobraHelp(t *testing.T, r *cli.Root, args ...string) string {
	t.Helper()
	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetErr(&buf)
	r.SetArgs(args)
	r.ApplyGroupVisibility()
	require.NoError(t, r.Cmd.Execute())
	return buf.String()
}

// commandHeaders are the command-section headers cobra's renderer can
// print for groupedRoot.
var commandHeaders = []string{
	"COMMANDS", "Available Commands:", "Additional Commands:",
	"MANAGEMENT", "EXTRAS", "EMPTY", "GHOST",
}

// assertNoEmptyCommandSections fails when a command-section header is not
// followed by at least one indented command line.
func assertNoEmptyCommandSections(t *testing.T, out string) {
	t.Helper()
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		for _, h := range commandHeaders {
			if l != h {
				continue
			}
			if i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], "  ") {
				t.Errorf("section header %q has no commands under it:\n%s", h, out)
			}
		}
	}
}

func hasLine(out, want string) bool {
	for _, l := range strings.Split(out, "\n") {
		if l == want {
			return true
		}
	}
	return false
}

func TestCobraHelp_HiddenGroupHasNoHeader(t *testing.T) {
	out := cobraHelp(t, groupedRoot(), "--help")

	assert.False(t, hasLine(out, "MANAGEMENT"),
		"hidden management group must not render its header:\n%s", out)
	assert.NotContains(t, out, "Manage configuration")
	assert.Contains(t, out, "Deploy the app")
	assertNoEmptyCommandSections(t, out)
}

func TestCobraHelp_GroupWithoutCommandsHasNoHeader(t *testing.T) {
	out := cobraHelp(t, groupedRoot(), "--help")

	assert.False(t, hasLine(out, "EMPTY"),
		"group with no commands must not render its header:\n%s", out)
	assert.True(t, hasLine(out, "EXTRAS"), "group with commands keeps its header")
	assert.Contains(t, out, "Bonus feature")
}

func TestCobraHelp_GroupOfHiddenCommandsHasNoHeader(t *testing.T) {
	out := cobraHelp(t, groupedRoot(), "--help-all")

	assert.False(t, hasLine(out, "GHOST"),
		"group whose commands are all hidden must not render its header:\n%s", out)
	assert.NotContains(t, out, "Hidden feature")
	assert.False(t, hasLine(out, "EMPTY"))
	assertNoEmptyCommandSections(t, out)
}

func TestCobraHelp_HelpAllShowsManagement(t *testing.T) {
	out := cobraHelp(t, groupedRoot(), "--help-all")

	assert.True(t, hasLine(out, "MANAGEMENT"), "--help-all reveals management:\n%s", out)
	assert.Contains(t, out, "Manage configuration")
	assert.Contains(t, out, "Deploy the app")
}

func TestCobraHelp_PerGroupShowsOnlyThatGroup(t *testing.T) {
	out := cobraHelp(t, groupedRoot(), "--help-management")

	assert.True(t, hasLine(out, "MANAGEMENT"), "requested group keeps its header:\n%s", out)
	assert.Contains(t, out, "Manage configuration")
	assert.NotContains(t, out, "Deploy the app")
	assert.NotContains(t, out, "Bonus feature")
	// kit's help subcommand is hidden by design; it must not surface
	// (with a header of its own) under a group it does not belong to.
	assert.NotContains(t, out, "\n  help ")
	assertNoEmptyCommandSections(t, out)
}

// With every visible command in a group, the ungrouped section holds only
// kit's hidden help subcommand and must not render.
func TestCobraHelp_NoUngroupedSectionWhenAllCommandsAreGrouped(t *testing.T) {
	build := func() *cli.Root {
		r := cli.New(cli.Config{
			Name: "mytool", Version: "0.1.0", Short: "A tool",
			Help:            cli.HelpConfig{Groups: []cli.GroupConfig{{ID: "extras", Title: "EXTRAS"}}},
			DisableValidate: true,
		})
		// Outside r.Execute cobra adds its completion command ungrouped.
		r.Cmd.CompletionOptions.DisableDefaultCmd = true
		run := func(*cobra.Command, []string) {}
		r.Cmd.AddCommand(
			&cobra.Command{Use: "bonus", Short: "Bonus feature", GroupID: "extras", Run: run},
			&cobra.Command{Use: "config", Short: "Manage configuration", GroupID: "management", Run: run},
		)
		return r
	}

	for _, args := range [][]string{{"--help"}, {"--help-all"}} {
		out := cobraHelp(t, build(), args...)
		assert.True(t, hasLine(out, "EXTRAS"), "%v:\n%s", args, out)
		assert.False(t, hasLine(out, "Additional Commands:"),
			"%v: no visible ungrouped command, so no ungrouped header:\n%s", args, out)
		assertNoEmptyCommandSections(t, out)
	}
}

func TestCobraHelp_SubcommandGroupWithoutCommandsHasNoHeader(t *testing.T) {
	r := cli.New(cli.Config{Name: "mytool", Version: "0.1.0", Short: "A tool", DisableValidate: true})
	svc := &cobra.Command{Use: "svc", Short: "Services"}
	svc.AddGroup(
		&cobra.Group{ID: "run", Title: "RUN"},
		&cobra.Group{ID: "idle", Title: "IDLE"},
	)
	svc.AddCommand(
		&cobra.Command{
			Use: "start", Short: "Start a service", GroupID: "run",
			Run: func(*cobra.Command, []string) {},
		},
		&cobra.Command{
			Use: "secret", Short: "Hidden ungrouped", Hidden: true,
			Run: func(*cobra.Command, []string) {},
		},
	)
	r.Cmd.AddCommand(svc)

	out := cobraHelp(t, r, "svc", "--help")

	assert.True(t, hasLine(out, "RUN"), "group with commands keeps its header:\n%s", out)
	assert.False(t, hasLine(out, "IDLE"),
		"subcommand group with no commands must not render its header:\n%s", out)
	assert.False(t, hasLine(out, "Additional Commands:"),
		"only hidden ungrouped commands, so no ungrouped header:\n%s", out)
}

// Fang, behind r.Execute, must hold the same rule.
func TestExecuteHelp_NoEmptyGroupHeaders(t *testing.T) {
	r := groupedRoot()
	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.SetArgs([]string{"--help"})
	require.NoError(t, r.Execute(t.Context()))
	out := stripANSI(buf.String())

	for _, h := range []string{"MANAGEMENT", "EMPTY", "GHOST"} {
		assert.NotContains(t, out, h, "header %q has no visible commands", h)
	}
	assert.Contains(t, out, "EXTRAS")
}

// At the root, ungrouped commands are kit's default group: fang titles it
// COMMANDS and renders it first. Cobra's "Additional Commands:" must not
// stand in for it.
func TestCobraHelp_RootUngroupedCommandsTitledCommandsFirst(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"--help-all"}} {
		out := cobraHelp(t, groupedRoot(), args...)
		lines := strings.Split(out, "\n")

		assert.False(t, hasLine(out, "Additional Commands:"), "%v:\n%s", args, out)
		cmds := sectionIdx(lines, "COMMANDS")
		require.GreaterOrEqual(t, cmds, 0, "%v: COMMANDS header missing:\n%s", args, out)
		var body []string
		for _, l := range lines[cmds+1:] {
			if !strings.HasPrefix(l, "  ") {
				break
			}
			body = append(body, l)
		}
		assert.Contains(t, strings.Join(body, "\n"), "Deploy the app",
			"%v: default-group command under COMMANDS:\n%s", args, out)
		for _, h := range []string{"EXTRAS", "MANAGEMENT"} {
			if i := sectionIdx(lines, h); i >= 0 {
				assert.Less(t, cmds, i, "%v: COMMANDS renders before %s", args, h)
			}
		}
	}
}

// Below the root there is no kit default group: cobra's title stays.
func TestCobraHelp_SubcommandUngroupedKeepsCobraTitle(t *testing.T) {
	r := cli.New(cli.Config{Name: "mytool", Version: "0.1.0", Short: "A tool", DisableValidate: true})
	run := func(*cobra.Command, []string) {}
	svc := &cobra.Command{Use: "svc", Short: "Services"}
	svc.AddGroup(&cobra.Group{ID: "run", Title: "RUN"})
	svc.AddCommand(
		&cobra.Command{Use: "start", Short: "Start a service", GroupID: "run", Run: run},
		&cobra.Command{Use: "status", Short: "Service status", Run: run},
	)
	r.Cmd.AddCommand(svc)

	out := cobraHelp(t, r, "svc", "--help")

	assert.True(t, hasLine(out, "Additional Commands:"), "\n%s", out)
	assert.False(t, hasLine(out, "COMMANDS"), "\n%s", out)
}
