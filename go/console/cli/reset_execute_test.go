package cli_test

import (
	"context"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
)

// rerunRoot builds a root whose leaf records what the flags held when
// it ran, so a second Execute can be compared against the first.
func rerunRoot(t *testing.T, seen *[]string, sawSlice *[][]string) *cli.Root {
	t.Helper()
	r := cli.New(cli.Config{
		Name: "reruntool", Version: "0.0.0", Short: "rerun test tool",
		DisableValidate: true,
	})
	leaf := &cobra.Command{
		Use: "go", Short: "go", Long: "go",
		RunE: func(cmd *cobra.Command, _ []string) error {
			name, _ := cmd.Flags().GetString("name")
			*seen = append(*seen, name)
			if sawSlice != nil {
				tags, _ := cmd.Flags().GetStringSlice("tag")
				*sawSlice = append(*sawSlice, append([]string(nil), tags...))
			}
			return nil
		},
	}
	cli.SetSideEffect(leaf, cli.SideEffectRead)
	leaf.Flags().String("name", "", "a name")
	leaf.Flags().StringSlice("tag", nil, "tags (repeatable)")
	r.Cmd.AddCommand(leaf)
	return r
}

// TestRerun_LeafFlagDoesNotLeakIntoSecondExecute is the defect: run 1
// sets a flag, run 2 omits it, and run 2 saw run 1's value because
// nothing returned the flag to its default between dispatches.
func TestRerun_LeafFlagDoesNotLeakIntoSecondExecute(t *testing.T) {
	var seen []string
	r := rerunRoot(t, &seen, nil)

	r.SetArgs([]string{"go", "--name=first"})
	require.NoError(t, r.Execute(context.Background()))

	r.SetArgs([]string{"go"})
	require.NoError(t, r.Execute(context.Background()))

	assert.Equal(t, []string{"first", ""}, seen,
		"the second run omitted --name, so it must see the default")
}

// TestRerun_ChangedDoesNotLeak: viper and every Changed-gated branch
// read the bit, so it has to be cleared with the value.
func TestRerun_ChangedDoesNotLeak(t *testing.T) {
	var seen []string
	r := rerunRoot(t, &seen, nil)

	r.SetArgs([]string{"go", "--name=first"})
	require.NoError(t, r.Execute(context.Background()))

	var changed bool
	leaf, _, err := r.Cmd.Find([]string{"go"})
	require.NoError(t, err)
	leaf.RunE = func(cmd *cobra.Command, _ []string) error {
		changed = cmd.Flags().Changed("name")
		return nil
	}

	r.SetArgs([]string{"go"})
	require.NoError(t, r.Execute(context.Background()))
	assert.False(t, changed, "Changed must be false when the flag was not passed")
}

// TestRerun_PersistentRootFlagDoesNotLeak: a root persistent flag is
// shared by pointer into every child's merged set, so a leak there
// reaches every leaf.
func TestRerun_PersistentRootFlagDoesNotLeak(t *testing.T) {
	var seen []bool
	r := cli.New(cli.Config{
		Name: "reruntool", Version: "0.0.0", Short: "rerun test tool",
		DisableValidate: true,
	})
	leaf := &cobra.Command{
		Use: "go", Short: "go", Long: "go",
		RunE: func(*cobra.Command, []string) error {
			seen = append(seen, r.Viper.GetBool("quiet"))
			return nil
		},
	}
	cli.SetSideEffect(leaf, cli.SideEffectRead)
	r.Cmd.AddCommand(leaf)

	r.SetArgs([]string{"--quiet", "go"})
	require.NoError(t, r.Execute(context.Background()))

	r.SetArgs([]string{"go"})
	require.NoError(t, r.Execute(context.Background()))

	assert.Equal(t, []bool{true, false}, seen,
		"--quiet was passed only to the first run")
}

// TestRerun_SliceFlagDoesNotAccumulate: pflag appends to a slice on
// every Set after the first, so a reset that only restores the value
// without emptying it stacks run 2 on top of run 1.
func TestRerun_SliceFlagDoesNotAccumulate(t *testing.T) {
	var seen []string
	var slices [][]string
	r := rerunRoot(t, &seen, &slices)

	r.SetArgs([]string{"go", "--tag=a"})
	require.NoError(t, r.Execute(context.Background()))

	r.SetArgs([]string{"go", "--tag=b"})
	require.NoError(t, r.Execute(context.Background()))

	require.Len(t, slices, 2)
	assert.Equal(t, []string{"a"}, slices[0])
	assert.Equal(t, []string{"b"}, slices[1],
		"the second run's tags must not carry the first run's")
}

// TestRerun_CallbackFlagIsNotReplayed: a pflag Func flag's Set IS the
// adopter's callback, so resetting it would invoke that callback with
// the default and fabricate a call the operator never made.
func TestRerun_CallbackFlagIsNotReplayed(t *testing.T) {
	var calls []string
	r := cli.New(cli.Config{
		Name: "reruntool", Version: "0.0.0", Short: "rerun test tool",
		DisableValidate: true,
	})
	leaf := &cobra.Command{
		Use: "go", Short: "go", Long: "go",
		RunE: func(*cobra.Command, []string) error { return nil },
	}
	cli.SetSideEffect(leaf, cli.SideEffectRead)
	leaf.Flags().Func("note", "record a note", func(v string) error {
		calls = append(calls, v)
		return nil
	})
	r.Cmd.AddCommand(leaf)

	r.SetArgs([]string{"go", "--note=one"})
	require.NoError(t, r.Execute(context.Background()))
	r.SetArgs([]string{"go"})
	require.NoError(t, r.Execute(context.Background()))

	assert.Equal(t, []string{"one"}, calls,
		"the reset must not invoke the adopter's callback")
}
