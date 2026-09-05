package cli_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
)

// unknownRoot builds a non-runnable root — kit's own shape, and the
// shape of every tool whose root only groups commands — carrying one
// non-runnable parent with a runnable leaf under it, so an unknown
// word can be aimed at either level.
func unknownRoot(t *testing.T, reached *bool) *cli.Root {
	t.Helper()
	r := cli.New(cli.Config{
		Name: "unknowntool", Version: "0.0.0", Short: "unknown test tool",
		DisableValidate: true,
	})
	parent := &cobra.Command{Use: "widget", Short: "manage widgets", Aliases: []string{"wid"}}
	leaf := &cobra.Command{
		Use:   "list",
		Short: "list widgets",
		Long:  "list widgets",
		RunE: func(*cobra.Command, []string) error {
			if reached != nil {
				*reached = true
			}
			return nil
		},
	}
	cli.SetSideEffect(leaf, cli.SideEffectRead)
	parent.AddCommand(leaf)
	r.Cmd.AddCommand(parent)
	return r
}

// executeUnknown runs args through the real Execute path and returns
// the taxonomy envelope (nil when the error is not one), stdout,
// stderr, and the raw error.
func executeUnknown(t *testing.T, r *cli.Root, args ...string) (*output.Error, string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	r.Cmd.SetOut(&stdout)
	r.Cmd.SetErr(&stderr)
	r.SetArgs(args)
	err := r.Execute(context.Background())
	var ce *output.Error
	errors.As(err, &ce)
	return ce, stdout.String(), stderr.String(), err
}

// TestUnknown_SubcommandUnderNonRunnableParentExitsTwo is the defect:
// an unrecognized word aimed at a non-runnable command printed help
// and exited 0, because cobra hands a non-runnable command straight to
// help before any Args validator can object.
func TestUnknown_SubcommandUnderNonRunnableParentExitsTwo(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		message string
	}{
		{"under the root", []string{"nosuch"},
			`unknown command "nosuch" for "unknowntool"`},
		{"under a parent", []string{"widget", "nosuch"},
			`unknown command "nosuch" for "unknowntool widget"`},
		{"under a parent by alias", []string{"wid", "nosuch"},
			`unknown command "nosuch" for "unknowntool widget"`},
		{"behind a root flag", []string{"--quiet", "nosuch"},
			`unknown command "nosuch" for "unknowntool"`},
		{"behind a valued root flag", []string{"--format", "json", "nosuch"},
			`unknown command "nosuch" for "unknowntool"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reached bool
			ce, stdout, stderr, err := executeUnknown(t, unknownRoot(t, &reached), tc.args...)
			require.Error(t, err, "an unknown subcommand must not succeed")
			require.NotNil(t, ce, "want a kit envelope, got %T: %v", err, err)
			assert.Equal(t, output.CodeUsage, ce.Code)
			assert.Equal(t, 2, ce.ExitCode)
			assert.Equal(t, tc.message, ce.Message)
			assert.Equal(t, output.TransiencePermanent, ce.Transience)
			assert.Contains(t, stderr, "USAGE: "+tc.message)
			assert.False(t, reached, "no leaf may run")
			assert.Empty(t, stdout, "help must not be printed as if the invocation succeeded")
		})
	}
}

// TestUnknown_ValidInvocationsKeepWorking pins every path that must
// still reach help or a leaf: the refusal is aimed only at a word
// that names nothing.
func TestUnknown_ValidInvocationsKeepWorking(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		wantLeaf bool
		wantOut  string
	}{
		{"bare non-runnable root prints help", nil, false, "Manage widgets"},
		{"bare non-runnable parent prints help", []string{"widget"}, false, "List widgets"},
		{"parent by alias prints help", []string{"wid"}, false, "List widgets"},
		{"root --help", []string{"--help"}, false, "Manage widgets"},
		{"root -h", []string{"-h"}, false, "Manage widgets"},
		{"parent --help", []string{"widget", "--help"}, false, "List widgets"},
		{"leaf runs", []string{"widget", "list"}, true, ""},
		{"leaf runs by parent alias", []string{"wid", "list"}, true, ""},
		{"completion is a real command", []string{"completion"}, false, "bash"},
		{"completion bash emits a script", []string{"completion", "bash"}, false, "bash"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reached bool
			ce, stdout, stderr, err := executeUnknown(t, unknownRoot(t, &reached), tc.args...)
			require.NoError(t, err, "stderr: %s", stderr)
			assert.Nil(t, ce)
			assert.Equal(t, tc.wantLeaf, reached)
			if tc.wantOut != "" {
				assert.Contains(t, stdout, tc.wantOut)
			}
		})
	}
}

// TestUnknown_FlagErrorsStayFlagErrors: a malformed flag is diagnosed
// as the flag error it is, not misreported as an unknown command.
func TestUnknown_FlagErrorsStayFlagErrors(t *testing.T) {
	ce, _, _, err := executeUnknown(t, unknownRoot(t, nil), "--nosuch", "nope")
	require.Error(t, err)
	require.NotNil(t, ce)
	assert.Equal(t, output.CodeUsage, ce.Code)
	assert.Equal(t, 2, ce.ExitCode)
	assert.Equal(t, "unknown flag: --nosuch", ce.Message,
		"the flag is the first thing wrong; it must be what is reported")
}

// TestUnknown_PassthroughLeafKeepsItsArgs: a leaf that declares it
// takes arbitrary args still receives them. The refusal applies to a
// word that would have to name a subcommand, never to an operand a
// runnable command accepts.
func TestUnknown_PassthroughLeafKeepsItsArgs(t *testing.T) {
	r := unknownRoot(t, nil)
	var got []string
	pass := &cobra.Command{
		Use: "run", Short: "run it", Long: "run it",
		Args: cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			got = args
			return nil
		},
	}
	cli.SetSideEffect(pass, cli.SideEffectRead)
	cli.SetPassthrough(pass)
	r.Cmd.AddCommand(pass)

	_, _, stderr, err := executeUnknown(t, r, "run", "anything", "at", "all")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Equal(t, []string{"anything", "at", "all"}, got)
}

// TestUnknown_CompletionRequestsAreNotRefused: cobra's hidden
// __complete command drives shell completion. It is not registered as
// a visible child, so a naive unknown-word check would refuse every
// completion request.
func TestUnknown_CompletionRequestsAreNotRefused(t *testing.T) {
	for _, args := range [][]string{
		{cobra.ShellCompRequestCmd, "wid"},
		{cobra.ShellCompNoDescRequestCmd, "widget", ""},
	} {
		t.Run(args[0], func(t *testing.T) {
			_, stdout, stderr, err := executeUnknown(t, unknownRoot(t, nil), args...)
			require.NoError(t, err, "stderr: %s", stderr)
			assert.Contains(t, stdout, ":", "a completion response carries a directive line")
		})
	}
}

// TestUnknown_HelpFlagPrecedence pins cobra's own reading of a help
// flag sharing a line with an unknown word. The rule is positional,
// not "a help flag anywhere wins": cobra tests the flag inside the
// resolved command's execute, which is only reached once Find has
// routed, so a flag ahead of the word prints help and a flag behind
// it never runs. gh and kubectl behave the same way.
func TestUnknown_HelpFlagPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		wantErr bool
	}{
		// Help flag first: cobra prints help, exit 0.
		{"--help before word", []string{"--help", "nosuch"}, false},
		{"-h before word", []string{"-h", "nosuch"}, false},
		{"--help-all before word", []string{"--help-all", "nosuch"}, false},
		{"--help=true before word", []string{"--help=true", "nosuch"}, false},
		// Help flag after the word: never reached, refusal stands.
		{"--help after word", []string{"nosuch", "--help"}, true},
		{"-h after word", []string{"nosuch", "-h"}, true},
		{"--help-all after word", []string{"nosuch", "--help-all"}, true},
		// Under a parent, cobra's legacyArgs check only fires at the
		// root, so the parent's own execute runs and honors the flag.
		// gh and kubectl agree: `gh repo bogus --help` exits 0 while
		// `gh bogus --help` exits 1.
		{"--help after word under a parent", []string{"widget", "nosuch", "--help"}, false},
		// A help flag behind "--" is an operand, not a flag.
		{"--help behind a terminator", []string{"nosuch", "--", "--help"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ce, _, stderr, err := executeUnknown(t, unknownRoot(t, nil), tc.args...)
			if tc.wantErr {
				require.Error(t, err, "want a refusal")
				require.NotNil(t, ce)
				assert.Equal(t, output.CodeUsage, ce.Code)
				assert.Equal(t, 2, ce.ExitCode)
				return
			}
			require.NoError(t, err, "a help flag ahead of the word is a help request\nstderr: %s", stderr)
			assert.Nil(t, ce)
		})
	}
}
