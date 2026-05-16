package cli_test

import (
	"context"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/progress"
)

// makeRootWithCapture builds a Root and a child "run" command whose
// RunE captures the active progress.Reporter via FromContext.
func makeRootWithCapture(t *testing.T) (*cli.Root, *progress.Reporter) {
	t.Helper()
	r := cli.New(cli.Config{
		Name:            "mytool",
		Version:         "1.2.3",
		Short:           "A test tool",
		DisableValidate: true,
	})
	var captured progress.Reporter
	run := &cobra.Command{
		Use: "run",
		RunE: func(cmd *cobra.Command, _ []string) error {
			captured = progress.FromContext(cmd.Context())
			return nil
		},
	}
	r.Cmd.AddCommand(run)
	return r, &captured
}

func TestRoot_ProgressFormat_FlagRegistered(t *testing.T) {
	r := cli.New(cli.Config{Name: "x", Version: "0.0.1", Short: "x", DisableValidate: true})
	pf := r.Cmd.PersistentFlags()
	f := pf.Lookup("progress-format")
	require.NotNil(t, f, "--progress-format must be registered by default")
	assert.Equal(t, "human", f.DefValue,
		"default progress-format must be human")
}

func TestRoot_DisableProgress_SuppressesFlag(t *testing.T) {
	r := cli.New(cli.Config{
		Name: "x", Version: "0.0.1", Short: "x",
		Disable:         cli.Disable{Progress: true},
		DisableValidate: true,
	})
	pf := r.Cmd.PersistentFlags()
	assert.Nil(t, pf.Lookup("progress-format"),
		"--progress-format must be suppressed when Disable.Progress=true")
}

func TestRoot_ProgressFormat_InheritsFormatJSON(t *testing.T) {
	r, captured := makeRootWithCapture(t)
	r.Cmd.SetArgs([]string{"run", "--format", "json"})
	require.NoError(t, r.Cmd.ExecuteContext(context.Background()))

	require.NotNil(t, *captured, "RunE must have observed a Reporter")
	// Inheritance rule: --format json with no explicit --progress-format
	// means the active reporter is JSONL, not Human.
	assert.IsType(t, progress.JSONL(nil), *captured,
		"--format=json without --progress-format must select JSONL")
}

func TestRoot_ProgressFormat_ExplicitHumanBeatsFormatJSON(t *testing.T) {
	r, captured := makeRootWithCapture(t)
	r.Cmd.SetArgs([]string{"run", "--format", "json", "--progress-format", "human"})
	require.NoError(t, r.Cmd.ExecuteContext(context.Background()))

	require.NotNil(t, *captured, "RunE must have observed a Reporter")
	assert.IsType(t, progress.Human(nil), *captured,
		"explicit --progress-format=human must override --format=json inheritance")
}

func TestRoot_ProgressFormat_ExplicitJSON(t *testing.T) {
	r, captured := makeRootWithCapture(t)
	r.Cmd.SetArgs([]string{"run", "--progress-format", "json"})
	require.NoError(t, r.Cmd.ExecuteContext(context.Background()))

	require.NotNil(t, *captured, "RunE must have observed a Reporter")
	assert.IsType(t, progress.JSONL(nil), *captured,
		"--progress-format=json must select JSONL")
}

func TestRoot_QuietForcesDiscard(t *testing.T) {
	r, captured := makeRootWithCapture(t)
	// Even with --progress-format=json, --quiet wins.
	r.Cmd.SetArgs([]string{"run", "--quiet", "--progress-format", "json"})
	require.NoError(t, r.Cmd.ExecuteContext(context.Background()))

	require.NotNil(t, *captured, "RunE must have observed a Reporter")
	assert.IsType(t, progress.Discard(), *captured,
		"--quiet must force a Discard reporter regardless of --progress-format")
}

func TestRoot_DefaultIsHuman(t *testing.T) {
	r, captured := makeRootWithCapture(t)
	r.Cmd.SetArgs([]string{"run"})
	require.NoError(t, r.Cmd.ExecuteContext(context.Background()))

	require.NotNil(t, *captured, "RunE must have observed a Reporter")
	assert.IsType(t, progress.Human(nil), *captured,
		"default reporter must be Human when no flags are set")
}
