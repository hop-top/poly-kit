package cli_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
)

// After "--" nothing is a flag, however it is spelled. The flags kit
// reads from the raw argv before cobra parses it honor that too: an
// argument that looks like one of them stays the argument it is.

// echoRoot is a root with one command, "echo", that prints the
// arguments it received, "|"-joined. The command is introduced at API
// version 2.0, so an --api-version below that would withhold it.
func echoRoot(t *testing.T, out *bytes.Buffer) *cli.Root {
	t.Helper()
	r := cli.New(cli.Config{Name: "tool", Version: "1.0.0", Short: "t", DisableValidate: true})
	echo := &cobra.Command{
		Use:   "echo",
		Short: "Print the arguments",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprint(out, strings.Join(args, "|"))
			return nil
		},
	}
	cli.SetSinceVersion(echo, "2.0")
	r.Cmd.AddCommand(echo)
	return r
}

func TestEndOfOptions_PreParsedFlagsAreArgumentsAfterIt(t *testing.T) {
	for _, arg := range []string{"--help-all", "--help-management", "--api-version=1.0"} {
		t.Run(arg, func(t *testing.T) {
			var out bytes.Buffer
			r := echoRoot(t, &out)
			r.SetArgs([]string{"echo", "--", arg})
			require.NoError(t, r.Execute(t.Context()))
			assert.Equal(t, arg, out.String())
		})
	}
}

// The same flags before "--" still take effect: the fix narrows where
// kit looks, not what it recognizes.
func TestEndOfOptions_PreParsedFlagsStillApplyBeforeIt(t *testing.T) {
	var out bytes.Buffer
	r := echoRoot(t, &out)
	r.SetArgs([]string{"--api-version=1.0", "echo", "--", "x"})
	assert.Error(t, r.Execute(t.Context()), "echo is withheld below its since version")
	assert.Empty(t, out.String())
}
