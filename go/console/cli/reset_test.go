package cli_test

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
)

func TestResetFlags_FlagReset(t *testing.T) {
	r := root()
	r.Cmd.SetArgs([]string{"--quiet"})
	require.NoError(t, r.Execute(t.Context()))

	q := r.Cmd.PersistentFlags().Lookup("quiet")
	require.NotNil(t, q)
	assert.Equal(t, "true", q.Value.String(), "quiet should be true after execute")

	r.Reset()

	assert.Equal(t, "false", q.Value.String(), "quiet should be false after reset")
}

func TestResetFlags_PersistentFlagLeak(t *testing.T) {
	r := root()
	child := &cobra.Command{
		Use: "sub",
		RunE: func(_ *cobra.Command, _ []string) error {
			return nil
		},
	}
	r.Cmd.AddCommand(child)

	r.Cmd.SetArgs([]string{"--no-color", "sub"})
	require.NoError(t, r.Execute(t.Context()))

	nc := r.Cmd.PersistentFlags().Lookup("no-color")
	require.NotNil(t, nc)
	assert.Equal(t, "true", nc.Value.String())

	r.Reset()

	assert.Equal(t, "false", nc.Value.String(),
		"no-color must be false after reset")
}

func TestResetFlags_ChangedCleared(t *testing.T) {
	r := root()
	r.Cmd.SetArgs([]string{"--quiet"})
	require.NoError(t, r.Execute(t.Context()))

	q := r.Cmd.PersistentFlags().Lookup("quiet")
	require.True(t, q.Changed, "Changed must be true after setting flag")

	r.Reset()

	assert.False(t, q.Changed, "Changed must be false after reset")
}

func TestResetFlags_ViperBindingRefresh(t *testing.T) {
	r := root()
	r.Cmd.SetArgs([]string{"--format=json"})
	require.NoError(t, r.Execute(t.Context()))

	assert.Equal(t, "json", r.Viper.GetString("format"),
		"viper should return json before reset")

	r.Reset()

	assert.Equal(t, "table", r.Viper.GetString("format"),
		"viper should return default after reset")
}

func TestResetFlags_RecursiveReset(t *testing.T) {
	r := root()
	child := &cobra.Command{
		Use: "sub",
		RunE: func(_ *cobra.Command, _ []string) error {
			return nil
		},
	}
	child.Flags().Bool("dry-run", false, "dry run mode")
	r.Cmd.AddCommand(child)

	// Set parent persistent + child local flags.
	r.Cmd.SetArgs([]string{"--quiet", "sub", "--dry-run"})
	require.NoError(t, r.Execute(t.Context()))

	q := r.Cmd.PersistentFlags().Lookup("quiet")
	dr := child.Flags().Lookup("dry-run")
	assert.Equal(t, "true", q.Value.String())
	assert.Equal(t, "true", dr.Value.String())

	// Use the standalone function directly.
	cli.ResetFlags(r.Cmd)

	assert.Equal(t, "false", q.Value.String(),
		"parent flag must reset")
	assert.Equal(t, "false", dr.Value.String(),
		"child flag must reset")
}

func TestResetFlags_SetArgsCleared(t *testing.T) {
	r := root()

	var subRuns int
	child := &cobra.Command{
		Use: "sub",
		RunE: func(_ *cobra.Command, _ []string) error {
			subRuns++
			return nil
		},
	}
	r.Cmd.AddCommand(child)

	r.Cmd.SetArgs([]string{"sub"})
	require.NoError(t, r.Execute(t.Context()))
	assert.Equal(t, 1, subRuns, "subcommand should run on first execution")

	r.Reset()

	// After Reset, args are empty — root runs, not the subcommand.
	require.NoError(t, r.Execute(t.Context()))
	assert.Equal(t, 1, subRuns,
		"previous SetArgs state must not be reused after reset")
}

func TestResetFlags_SequentialTestPattern(t *testing.T) {
	r := root()
	child := &cobra.Command{
		Use: "greet",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !r.Viper.GetBool("quiet") {
				cmd.Println("hello")
			}
			return nil
		},
	}
	r.Cmd.AddCommand(child)

	// Run 1: quiet — should produce no "hello" output.
	var buf1 bytes.Buffer
	r.Cmd.SetOut(&buf1)
	r.Cmd.SetArgs([]string{"--quiet", "greet"})
	require.NoError(t, r.Execute(t.Context()))
	assert.NotContains(t, buf1.String(), "hello",
		"quiet run must suppress output")

	r.Reset()

	// Run 2: no flags — should produce "hello".
	var buf2 bytes.Buffer
	r.Cmd.SetOut(&buf2)
	r.Cmd.SetArgs([]string{"greet"})
	require.NoError(t, r.Execute(t.Context()))
	assert.Contains(t, buf2.String(), "hello",
		"non-quiet run must produce output")
}
