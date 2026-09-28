package cli_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/idemstore"
	"hop.top/kit/go/transport/cmdsurface"
)

// mintRoot is a prepared root wiring store as its idempotency store,
// with one conditional-idempotent write whose output names its owner:
// the shape of a command whose recorded output is the caller's alone.
func mintRoot(t *testing.T, store idemstore.Store) *cli.Root {
	t.Helper()
	r := cli.New(cli.Config{
		Name:            "idemtool",
		Version:         "0.0.0",
		Short:           "idempotency test tool",
		DisableValidate: true,
	}, cli.WithIdempotencyStore(store))
	mint := &cobra.Command{
		Use:   "mint",
		Short: "Mint a secret for its owner",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			owner, _ := cmd.Flags().GetString("owner")
			fmt.Fprintf(cmd.OutOrStdout(), "secret for %s\n", owner)
			return nil
		},
	}
	mint.Flags().String("owner", "", "owner")
	cli.SetSideEffect(mint, cli.SideEffectWriteLocal)
	cli.SetIdempotency(mint, cli.IdempotencyConditional)
	r.Cmd.AddCommand(mint)
	require.NoError(t, r.Prepare())
	return r
}

func mintAs(t *testing.T, b *cmdsurface.Bridge, meta cmdsurface.Meta, owner string) string {
	t.Helper()
	res, err := b.Invoke(context.Background(), cmdsurface.Invocation{
		Path:  []string{"mint"},
		Flags: map[string]any{"owner": owner},
		Meta:  meta,
	})
	require.NoError(t, err)
	require.Equal(t, 0, res.ExitCode, "stderr: %s", res.Stderr)
	return res.Stdout
}

// The capture around a keyed run must leave the command writing
// where it wrote before: on a shared tree, a leaf left holding one
// invocation's buffer would swallow every later invocation's output.
func TestServedIdempotencyKey_CaptureDoesNotPinTheWriter(t *testing.T) {
	store := idemstore.Memory()
	defer store.Close()
	r := mintRoot(t, store)
	b := cmdsurface.New(r.Cmd).Expose("*", cmdsurface.SurfaceREST)

	keyed := cmdsurface.Meta{Surface: cmdsurface.SurfaceREST, IdempotencyKey: "k1"}
	require.Equal(t, "secret for alice\n", mintAs(t, b, keyed, "alice"))
	assert.Equal(t, "secret for bob\n", mintAs(t, b, cmdsurface.Meta{Surface: cmdsurface.SurfaceREST}, "bob"),
		"an unkeyed run after a keyed one must reach its own caller")
	fresh := cmdsurface.Meta{Surface: cmdsurface.SurfaceREST, IdempotencyKey: "k9"}
	assert.Equal(t, "secret for carol\n", mintAs(t, b, fresh, "carol"),
		"a keyed run after a keyed one must reach its own caller")
}
