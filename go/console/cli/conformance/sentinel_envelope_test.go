package conformance_test

import (
	"errors"
	"testing"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/conformance"
)

// conformance's sentinel types document that errors.Is identity survives
// wrapping (see wrappedSentinel / UsageError). Those types implement
// AsCLIError, so they travel the RunE middleware's passthrough branch. This
// pins the end of that journey: identity must still hold for the error a
// caller receives from Execute, or the documented promise is only true right
// up until the point anything actually uses it.
func TestUsageErrorKeepsIdentityThroughMiddleware(t *testing.T) {
	handlerErr := conformance.UsageError("bad flag")

	if !errors.Is(handlerErr, conformance.ErrUsage) {
		t.Fatalf("precondition: UsageError does not match ErrUsage before the envelope")
	}

	got := runLeafReturning(t, handlerErr)

	if !errors.Is(got, conformance.ErrUsage) {
		t.Errorf("errors.Is(executeErr, ErrUsage) = false, want true; "+
			"the envelope conversion severed the sentinel (err=%#v)", got)
	}
}

// runLeafReturning executes a minimal command whose RunE returns runErr,
// through the same middleware chain adopters get, and returns what the
// caller sees.
func runLeafReturning(t *testing.T, runErr error) error {
	t.Helper()

	r := cli.New(cli.Config{
		Name:            "probe",
		Version:         "0.0.0",
		DisableValidate: true,
	})

	leaf := &cobra.Command{
		Use:   "do",
		Short: "do thing",
		Annotations: map[string]string{
			"kit/side-effect": "false",
		},
		RunE: func(_ *cobra.Command, _ []string) error { return runErr },
	}
	r.Cmd.AddCommand(leaf)

	r.Cmd.SetOut(discard{})
	r.Cmd.SetErr(discard{})
	r.Cmd.SetArgs([]string{"do"})
	r.WrapRunE()

	return r.Cmd.Execute()
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
