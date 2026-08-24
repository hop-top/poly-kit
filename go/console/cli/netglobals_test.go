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
	for _, name := range []string{"offline", "profile", "instance"} {
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
	for _, flag := range []string{"--offline", "--profile", "--instance"} {
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

func TestProfileInstance_ReachContext(t *testing.T) {
	r, captured := newNetGlobalsRoot(t)
	r.SetArgs([]string{"do", "--profile", "work", "--instance", "staging"})
	require.NoError(t, r.Execute(context.Background()))
	require.NotNil(t, *captured)
	assert.Equal(t, "work", cli.ProfileFrom(*captured))
	assert.Equal(t, "staging", cli.InstanceFrom(*captured))
	assert.Equal(t, "work", r.Profile())
	assert.Equal(t, "staging", r.Instance())
}

// TestProfile_EnvFallback locks the parity-guide contract "--profile
// defaults to $APS_PROFILE": env is consulted only when the flag is
// absent; an explicit flag wins over the env var.
func TestProfile_EnvFallback(t *testing.T) {
	t.Setenv("APS_PROFILE", "envprof")

	r, captured := newNetGlobalsRoot(t)
	r.SetArgs([]string{"do"})
	require.NoError(t, r.Execute(context.Background()))
	require.NotNil(t, *captured)
	assert.Equal(t, "envprof", cli.ProfileFrom(*captured),
		"unset --profile must fall back to $APS_PROFILE")
	assert.Equal(t, "envprof", r.Profile())

	r2, captured2 := newNetGlobalsRoot(t)
	r2.SetArgs([]string{"do", "--profile", "explicit"})
	require.NoError(t, r2.Execute(context.Background()))
	require.NotNil(t, *captured2)
	assert.Equal(t, "explicit", cli.ProfileFrom(*captured2),
		"explicit --profile must beat $APS_PROFILE")
}

func TestNetGlobals_ContextHelpers_ZeroValues(t *testing.T) {
	ctx := context.Background()
	assert.False(t, cli.IsOffline(ctx))
	assert.Empty(t, cli.ProfileFrom(ctx))
	assert.Empty(t, cli.InstanceFrom(ctx))

	ctx = cli.WithOffline(ctx, true)
	ctx = cli.WithProfile(ctx, "p")
	ctx = cli.WithInstance(ctx, "i")
	assert.True(t, cli.IsOffline(ctx))
	assert.Equal(t, "p", cli.ProfileFrom(ctx))
	assert.Equal(t, "i", cli.InstanceFrom(ctx))
}

// TestNetGlobals_NilRootAccessors guards the nil-safety contract that
// InitCmd-style consumers (which accept a nil *cli.Root) rely on.
func TestNetGlobals_NilRootAccessors(t *testing.T) {
	var r *cli.Root
	assert.False(t, r.Offline())
	assert.Empty(t, r.Profile())
	assert.Empty(t, r.Instance())
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
