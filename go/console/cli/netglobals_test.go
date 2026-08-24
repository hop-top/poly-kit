package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
)

// newNetGlobalsRoot builds a minimal root with a probe leaf whose RunE
// captures the context AFTER the PersistentPreRunE chain ran, so
// assertions see exactly what production leaves see.
func newNetGlobalsRoot(t *testing.T) (*cli.Root, *context.Context) {
	t.Helper()
	r := cli.New(cli.Config{Name: "t", Version: "0.1.0", Short: "t", DisableValidate: true})
	var captured context.Context
	leaf := &cobra.Command{
		Use:  "do",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			captured = cmd.Context()
			return nil
		},
	}
	cli.SetSideEffect(leaf, cli.SideEffectRead)
	r.Cmd.AddCommand(leaf)
	r.Cmd.SetOut(&bytes.Buffer{})
	r.Cmd.SetErr(&bytes.Buffer{})
	return r, &captured
}

func TestNetGlobals_RegisteredByDefault(t *testing.T) {
	r := cli.New(cli.Config{Name: "t", Version: "0.1.0", Short: "t", DisableValidate: true})
	pf := r.Cmd.PersistentFlags()
	for _, name := range []string{"offline"} {
		f := pf.Lookup(name)
		require.NotNil(t, f, "--%s must be registered by default", name)
		// Parity contract flags stay visible in --help (unlike the
		// hidden kit plumbing such as --chdir/--config/--dry-run).
		assert.False(t, f.Hidden, "--%s must not be hidden", name)
	}
}

func TestNetGlobals_VisibleInHelp(t *testing.T) {
	r := cli.New(cli.Config{Name: "t", Version: "0.1.0", Short: "t", DisableValidate: true})
	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetErr(&buf)
	r.SetArgs([]string{"--help"})
	_ = r.Execute(context.Background())
	out := buf.String()
	for _, flag := range []string{"--offline"} {
		assert.Contains(t, out, flag, "%s must appear in root --help", flag)
	}
}

func TestOffline_TagsContext(t *testing.T) {
	r, captured := newNetGlobalsRoot(t)
	r.SetArgs([]string{"do", "--offline"})
	require.NoError(t, r.Execute(context.Background()))
	require.NotNil(t, *captured)
	assert.True(t, cli.IsOffline(*captured), "RunE ctx must carry the offline tag")
	assert.True(t, r.Offline(), "Root.Offline must report the flag")
}

func TestOffline_DefaultOff(t *testing.T) {
	r, captured := newNetGlobalsRoot(t)
	r.SetArgs([]string{"do"})
	require.NoError(t, r.Execute(context.Background()))
	require.NotNil(t, *captured)
	assert.False(t, cli.IsOffline(*captured), "without --offline, ctx must be untagged")
	assert.False(t, r.Offline())
}

// TestNetGlobals_NilRootAccessors guards the nil-safety contract that
// InitCmd-style consumers (which accept a nil *cli.Root) rely on.
func TestNetGlobals_NilRootAccessors(t *testing.T) {
	var r *cli.Root
	assert.False(t, r.Offline())
}

// TestOffline_HelpUnaffected: --offline on a help invocation must not
// break help rendering (hooks run for help too via cobra).
func TestOffline_HelpUnaffected(t *testing.T) {
	r := cli.New(cli.Config{Name: "t", Version: "0.1.0", Short: "t", DisableValidate: true})
	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetErr(&buf)
	r.SetArgs([]string{"--offline", "--help"})
	require.NoError(t, r.Execute(context.Background()))
	assert.True(t, strings.Contains(buf.String(), "USAGE") || strings.Contains(buf.String(), "Usage"),
		"help output must render under --offline:\n%s", buf.String())
}
