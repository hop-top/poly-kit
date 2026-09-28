package cli_test

import (
	"bytes"
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

// A served caller sending another caller's Idempotency-Key must not
// be answered with that caller's recorded output, whichever runner
// executes the command.
func TestServedIdempotencyKey_ReplayIsScopedToTheCaller(t *testing.T) {
	runners := map[string]func(*cli.Root, idemstore.Store) []cmdsurface.Option{
		"shared tree": func(*cli.Root, idemstore.Store) []cmdsurface.Option { return nil },
		"root factory": func(_ *cli.Root, store idemstore.Store) []cmdsurface.Option {
			return []cmdsurface.Option{cmdsurface.WithRunner(cmdsurface.InProcessRunner(nil,
				cmdsurface.WithRootFactory(func() *cobra.Command { return mintRoot(t, store).Cmd })))}
		},
	}
	for name, opts := range runners {
		t.Run(name, func(t *testing.T) {
			store := idemstore.Memory()
			defer store.Close()
			r := mintRoot(t, store)
			b := cmdsurface.New(r.Cmd, opts(r, store)...).Expose("*", cmdsurface.SurfaceREST, cmdsurface.SurfaceSocket)

			alice := cmdsurface.Meta{Surface: cmdsurface.SurfaceREST, Caller: "alice",
				Established: cmdsurface.EstablishedVerified, IdempotencyKey: "k1"}
			bob := cmdsurface.Meta{Surface: cmdsurface.SurfaceREST, Caller: "bob",
				Established: cmdsurface.EstablishedVerified, IdempotencyKey: "k1"}
			carol := cmdsurface.Meta{Surface: cmdsurface.SurfaceSocket, Caller: "carol", IdempotencyKey: "k2"}
			dave := cmdsurface.Meta{Surface: cmdsurface.SurfaceSocket, Caller: "dave", IdempotencyKey: "k2"}

			assert.Equal(t, "secret for alice\n", mintAs(t, b, alice, "alice"))
			assert.Equal(t, "secret for bob\n", mintAs(t, b, bob, "bob"),
				"bob sent alice's key and must not receive alice's output")
			assert.Equal(t, "secret for alice\n", mintAs(t, b, alice, "alice-2"),
				"alice's own key still replays her recorded output")

			// A socket caller the owner-only file let in claims
			// alice's name: the transport vouched for the connection,
			// not the name, so it never reaches verified alice's record.
			sockAlice := cmdsurface.Meta{Surface: cmdsurface.SurfaceSocket, Caller: "alice",
				Established: cmdsurface.EstablishedTransport, IdempotencyKey: "k1"}
			assert.Equal(t, "secret for mallory\n", mintAs(t, b, sockAlice, "mallory"),
				"a transport-established claim of alice's name must not receive her output")

			assert.Equal(t, "secret for carol\n", mintAs(t, b, carol, "carol"))
			assert.Equal(t, "secret for dave\n", mintAs(t, b, dave, "dave"),
				"a socket caller sending another's key must not receive its output")
		})
	}
}

// A key a served caller used never answers the local command line,
// nor the reverse: a local user's records stay under the key as typed.
func TestServedIdempotencyKey_NeverMeetsTheLocalKey(t *testing.T) {
	store := idemstore.Memory()
	defer store.Close()
	r := mintRoot(t, store)
	b := cmdsurface.New(r.Cmd).Expose("*", cmdsurface.SurfaceREST)

	alice := cmdsurface.Meta{Surface: cmdsurface.SurfaceREST, Caller: "alice",
		Established: cmdsurface.EstablishedVerified, IdempotencyKey: "k1"}
	require.Equal(t, "secret for alice\n", mintAs(t, b, alice, "alice"))

	_, hit, err := store.Lookup(context.Background(), "k1")
	require.NoError(t, err)
	assert.False(t, hit, "a served record is stored under the raw key")

	local := mintRoot(t, store)
	var out bytes.Buffer
	local.Cmd.SetOut(&out)
	local.Cmd.SetArgs([]string{"mint", "--owner", "local", "--idempotency-key", "k1"})
	require.NoError(t, local.Cmd.ExecuteContext(context.Background()))
	assert.Equal(t, "secret for local\n", out.String(),
		"the local command line must not replay a served caller's record")

	_, hit, err = store.Lookup(context.Background(), "k1")
	require.NoError(t, err)
	assert.True(t, hit, "a local record is stored under the key as typed")
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
