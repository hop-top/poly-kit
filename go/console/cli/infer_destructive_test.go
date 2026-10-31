package cli

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/ai/cmdreflect"
	"hop.top/kit/go/console/cli/cmdmeta"
)

// The CLI gate and the reflected descriptor read one name table: a
// name the descriptor reports as inferred-destructive is refused
// without confirmation at the command line, and one it does not is
// not.
func TestGateAgreesWithDescriptorOnInferredDestructive(t *testing.T) {
	names := append(cmdmeta.DestructiveNames(), "list", "delete-all")
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			r := New(Config{Name: "itool", Version: "0.0.0", Short: "infer test"})
			leaf := &cobra.Command{
				Use: name, Short: name,
				RunE: func(*cobra.Command, []string) error { return nil },
			}
			r.Cmd.AddCommand(leaf)

			var inferred bool
			for _, d := range cmdreflect.Reflect(r.Cmd).Descriptors {
				if d.PathKey() == name {
					inferred = d.Safety.TierInferred
				}
			}

			_, _, err := runWithStdin(t, r, []string{name}, "", false)
			if inferred {
				require.Error(t, err, "gate must refuse what the descriptor infers destructive")
				assert.Contains(t, err.Error(), "refused")
				_, _, err = runWithStdin(t, r, []string{"--confirm=yes", name}, "", false)
				assert.NoError(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, inferred, cmdmeta.IsDestructiveName(name))
		})
	}
}

func TestExplicitSideEffectBeatsDestructiveName(t *testing.T) {
	for _, tier := range []SideEffect{SideEffectRead, SideEffectWrite, "bogus"} {
		r := New(Config{Name: "itool", Version: "0.0.0", Short: "infer test"})
		leaf := &cobra.Command{
			Use: "delete", Short: "d",
			RunE: func(*cobra.Command, []string) error { return nil },
		}
		SetSideEffect(leaf, tier)
		SetIdempotency(leaf, IdempotencyYes)
		r.Cmd.AddCommand(leaf)
		_, _, err := runWithStdin(t, r, []string{"delete"}, "", false)
		assert.NoError(t, err, "tier %q", tier)
	}
}
