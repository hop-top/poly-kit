package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
)

// newServeCmdForTest builds the real serve command against a minimal
// root so flag registration matches production exactly.
func newServeCmdForTest(t *testing.T) *cli.Root {
	t.Helper()
	return cli.New(cli.Config{
		Name: "kit", Version: "0.0.0-test", Short: "t",
		DisableValidate: true,
	})
}

// TestServeNetOpts_OfflineFlipsBoth locks the --offline override for
// `kit serve`: an offline-tagged context flips both --no-peer and
// --no-sync on even when neither flag was passed, so peer discovery
// and sync replication are disabled.
func TestServeNetOpts_OfflineFlipsBoth(t *testing.T) {
	root := newServeCmdForTest(t)
	cmd := serveCmd(root)
	require.NoError(t, cmd.ParseFlags(nil))
	cmd.SetContext(cli.WithOffline(context.Background(), true))

	noPeer, noSync := serveNetOpts(cmd)
	assert.True(t, noPeer, "--offline must disable peer discovery")
	assert.True(t, noSync, "--offline must disable sync replication")
}

// TestServeNetOpts_DefaultsRespected: without --offline the individual
// flags keep their parsed values.
func TestServeNetOpts_DefaultsRespected(t *testing.T) {
	root := newServeCmdForTest(t)
	cmd := serveCmd(root)
	require.NoError(t, cmd.ParseFlags(nil))
	cmd.SetContext(context.Background())

	noPeer, noSync := serveNetOpts(cmd)
	assert.False(t, noPeer)
	assert.False(t, noSync)
}

// TestServeNetOpts_ExplicitFlagWithoutOffline: individual opt-outs
// still work standalone.
func TestServeNetOpts_ExplicitFlagWithoutOffline(t *testing.T) {
	root := newServeCmdForTest(t)
	cmd := serveCmd(root)
	require.NoError(t, cmd.ParseFlags([]string{"--no-peer"}))
	cmd.SetContext(context.Background())

	noPeer, noSync := serveNetOpts(cmd)
	assert.True(t, noPeer, "--no-peer alone must disable peer discovery")
	assert.False(t, noSync, "--no-sync must stay off when not passed")
}

// TestServeNetOpts_OfflineNeverUnsetsExplicit: --offline composes with
// an explicitly passed --no-peer/--no-sync — it never un-sets them.
func TestServeNetOpts_OfflineNeverUnsetsExplicit(t *testing.T) {
	root := newServeCmdForTest(t)
	cmd := serveCmd(root)
	require.NoError(t, cmd.ParseFlags([]string{"--no-peer", "--no-sync"}))
	cmd.SetContext(cli.WithOffline(context.Background(), true))

	noPeer, noSync := serveNetOpts(cmd)
	assert.True(t, noPeer)
	assert.True(t, noSync)
}
