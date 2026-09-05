package cli

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/output"
)

func nukeLeaf() *cobra.Command {
	return &cobra.Command{
		Use:         "nuke",
		Short:       "destroy",
		Long:        "destroy everything",
		Annotations: map[string]string{"kit/side-effect": "destructive"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Print("destroyed")
			return nil
		},
	}
}

// runDirect executes the tree the way a runner does: through cobra,
// not through Execute, with captured streams and an empty stdin.
func runDirect(t *testing.T, r *Root, args ...string) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	r.Cmd.SetOut(&out)
	r.Cmd.SetErr(&errOut)
	r.Cmd.SetIn(bytes.NewReader(nil))
	r.Cmd.SetArgs(args)
	err := r.Cmd.ExecuteContext(context.Background())
	return out.String(), err
}

func TestPrepareInstallsTheGatesWithoutExecuting(t *testing.T) {
	r := New(Config{Name: "p", Version: "0.1.0", DisableValidate: true})
	r.Cmd.AddCommand(nukeLeaf())
	require.NoError(t, r.Prepare())

	// The confirmation gate refuses an unconfirmed destructive run
	// with the taxonomy's UNAUTHORIZED code, exactly as Execute would.
	out, err := runDirect(t, r, "nuke")
	var oe *output.Error
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, output.UnauthorizedError("").ExitCode, oe.ExitCode)
	assert.Empty(t, out, "the command must not have run")

	out, err = runDirect(t, r, "nuke", "--confirm=yes")
	require.NoError(t, err)
	assert.Equal(t, "destroyed", out)
}

// The control for the test above: a tree from New alone has no gate,
// which is why a factory over New must go through Prepare.
func TestUnpreparedTreeRunsDestructiveUnconfirmed(t *testing.T) {
	r := New(Config{Name: "p", Version: "0.1.0", DisableValidate: true})
	r.Cmd.AddCommand(nukeLeaf())

	out, err := runDirect(t, r, "nuke")
	require.NoError(t, err)
	assert.Equal(t, "destroyed", out)
}

func TestPrepareIsIdempotent(t *testing.T) {
	r := New(Config{
		Name: "p", Version: "0.1.0", DisableValidate: true,
		Help: HelpConfig{ShowAliases: true},
	})
	leaf := nukeLeaf()
	leaf.Aliases = []string{"boom"}
	r.Cmd.AddCommand(leaf)

	require.NoError(t, r.Prepare())
	wrapped := reflect.ValueOf(leaf.RunE).Pointer()
	require.NoError(t, r.Prepare())

	assert.Equal(t, wrapped, reflect.ValueOf(leaf.RunE).Pointer(),
		"a second Prepare must not wrap RunE again")
	assert.Equal(t, "destroy (aliases: boom)", leaf.Short,
		"the alias annotation must be applied once")
	completions := 0
	for _, c := range r.Cmd.Commands() {
		if c.Name() == "completion" {
			completions++
		}
	}
	assert.Equal(t, 1, completions)

	// And Execute after Prepare still runs the tree once, gated.
	r.Cmd.SetIn(bytes.NewReader(nil))
	r.SetArgs([]string{"nuke"})
	err := r.Execute(context.Background())
	var oe *output.Error
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, output.UnauthorizedError("").ExitCode, oe.ExitCode)
}

func TestPrepareReturnsTheValidationErrorInsteadOfDispatching(t *testing.T) {
	// EnforceValidate is on and ValidationFailureMode is the default,
	// which would os.Exit from Execute. Prepare hands the error back.
	r := New(Config{Name: "p", Version: "0.1.0"})
	bare := &cobra.Command{
		Use:  "bare",
		RunE: func(*cobra.Command, []string) error { return nil },
	}
	r.Cmd.AddCommand(bare)

	err := r.Prepare()
	var ve *ValidationError
	require.ErrorAs(t, err, &ve)
	assert.Contains(t, ve.Missing, "p bare")
	assert.NotEqual(t, "true", bare.Annotations[wrappedAnnotation],
		"a tree that failed validation must not be gated as if it passed")
}

func TestPrepareRejectsSignatureViolationsWhenAsked(t *testing.T) {
	r := New(Config{
		Name: "p", Version: "0.1.0", DisableValidate: true,
		SignatureStrictness: SignatureStrictnessReject,
	})
	r.Cmd.AddCommand(&cobra.Command{
		Use:         "run",
		Short:       "run",
		Long:        "run",
		Args:        cobra.ArbitraryArgs, // without kit/passthrough
		Annotations: map[string]string{"kit/side-effect": "read"},
		RunE:        func(*cobra.Command, []string) error { return nil },
	})

	err := r.Prepare()
	var se *SignatureReportError
	require.ErrorAs(t, err, &se)
}

func TestVerboseCountReadsTheParsedFlag(t *testing.T) {
	r := New(Config{Name: "p", Version: "0.1.0", DisableValidate: true})
	r.Cmd.AddCommand(&cobra.Command{
		Use:  "sub",
		RunE: func(*cobra.Command, []string) error { return nil },
	})
	assert.Equal(t, 0, r.VerboseCount(), "unparsed: the default")

	r.SetArgs([]string{"-VV", "sub"})
	require.NoError(t, r.Execute(context.Background()))
	assert.Equal(t, 2, r.VerboseCount(), "parsed by a subcommand")
}

func TestOperatorGlobalsRoundTrip(t *testing.T) {
	// The serving root parsed -c twice, --region once, and left
	// --verbose alone; a fresh tree must see exactly that, Changed
	// bits included, so viper reports the values as parsed.
	cfg := Config{
		Name: "p", Version: "0.1.0", DisableValidate: true,
		Globals: []Flag{{Name: "region", Default: "us", Usage: "region"}},
	}
	served := New(cfg)
	served.Cmd.AddCommand(&cobra.Command{
		Use:  "noop",
		RunE: func(*cobra.Command, []string) error { return nil },
	})
	served.SetArgs([]string{"-c", "a=1", "-c", "b=2", "--region", "eu", "noop"})
	require.NoError(t, served.Execute(context.Background()))

	fresh := New(cfg)
	replayOperatorGlobals(fresh.Cmd, captureOperatorGlobals(served.Cmd))

	assert.Equal(t, "eu", fresh.Viper.GetString("region"))
	assert.Equal(t, map[string]any{"a": 1, "b": 2}, fresh.ConfigOverrides())
	assert.Equal(t, 0, fresh.VerboseCount(), "an untouched flag stays at its default")
	assert.True(t, fresh.Cmd.PersistentFlags().Lookup("region").Changed)
}
