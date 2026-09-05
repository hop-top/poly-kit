package main

import (
	"context"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
)

// newServeCmdForTest builds kit's real root and returns its serve
// command, so flag registration matches production exactly: the
// engine's --no-peer/--no-sync sit on the kit-owned serve parent.
func newServeCmdForTest(t *testing.T) *cobra.Command {
	t.Helper()
	root, eng := newKitRoot("0.0.0-test")
	t.Cleanup(eng.close)
	for _, c := range root.Cmd.Commands() {
		if c.Name() == "serve" {
			return c
		}
	}
	t.Fatal("kit root has no serve command")
	return nil
}

// TestServeNetOpts_OfflineFlipsBoth locks the --offline override for
// `kit serve`: an offline-tagged context flips both --no-peer and
// --no-sync on even when neither flag was passed, so peer discovery
// and sync replication are disabled.
func TestServeNetOpts_OfflineFlipsBoth(t *testing.T) {
	cmd := newServeCmdForTest(t)
	require.NoError(t, cmd.ParseFlags(nil))
	cmd.SetContext(cli.WithOffline(context.Background(), true))

	noPeer, noSync := serveNetOpts(cmd)
	assert.True(t, noPeer, "--offline must disable peer discovery")
	assert.True(t, noSync, "--offline must disable sync replication")
}

// TestServeNetOpts_DefaultsRespected: without --offline the individual
// flags keep their parsed values.
func TestServeNetOpts_DefaultsRespected(t *testing.T) {
	cmd := newServeCmdForTest(t)
	require.NoError(t, cmd.ParseFlags(nil))
	cmd.SetContext(context.Background())

	noPeer, noSync := serveNetOpts(cmd)
	assert.False(t, noPeer)
	assert.False(t, noSync)
}

// TestServeNetOpts_ExplicitFlagWithoutOffline: individual opt-outs
// still work standalone.
func TestServeNetOpts_ExplicitFlagWithoutOffline(t *testing.T) {
	cmd := newServeCmdForTest(t)
	require.NoError(t, cmd.ParseFlags([]string{"--no-peer"}))
	cmd.SetContext(context.Background())

	noPeer, noSync := serveNetOpts(cmd)
	assert.True(t, noPeer, "--no-peer alone must disable peer discovery")
	assert.False(t, noSync, "--no-sync must stay off when not passed")
}

// TestServeNetOpts_OfflineNeverUnsetsExplicit: --offline composes with
// an explicitly passed --no-peer/--no-sync — it never un-sets them.
func TestServeNetOpts_OfflineNeverUnsetsExplicit(t *testing.T) {
	cmd := newServeCmdForTest(t)
	require.NoError(t, cmd.ParseFlags([]string{"--no-peer", "--no-sync"}))
	cmd.SetContext(cli.WithOffline(context.Background(), true))

	noPeer, noSync := serveNetOpts(cmd)
	assert.True(t, noPeer)
	assert.True(t, noSync)
}
