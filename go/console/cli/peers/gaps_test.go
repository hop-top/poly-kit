package peers_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/peers"
)

// Gap 1b (closed): peer-init failure preserves the rest of the chain.
//
// Previously, when initPeers() returned an error, New() overwrote
// PersistentPreRunE with a single closure that returned that error,
// dropping the chdir hook. The new chain composer collects all hooks
// into a slice and assigns PersistentPreRunE exactly once; chdir runs
// before the peer-error short-circuit, exposing the failure to the
// caller without losing the prior chain.
//
// The peers Init is now an extension InitHook (cli.InitHook); its
// error takes the deferred slot the peer-init error held. The
// deterministic seam used here is peers.With without cli.WithIdentity:
// Init returns "peer: peers.With requires cli.WithIdentity" without
// touching disk or starting goroutines.
func TestGap_PeerInitFailure_DropsChdirHook(t *testing.T) {
	dir := t.TempDir()
	orig, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	r := cli.New(cli.Config{Name: "gap1b", Version: "0.0.0", Short: "gap", DisableValidate: true},
		peers.With(peers.Config{DataDir: dir}))
	require.NotNil(t, r.Cmd.PersistentPreRunE,
		"chain composer must install PersistentPreRunE even on peer-init failure")

	require.NoError(t, r.Cmd.ParseFlags([]string{"--chdir", dir}))
	err = r.Cmd.PersistentPreRunE(r.Cmd, nil)

	// chdir must have run before the peer-init failure surfaced.
	cwd, cwdErr := os.Getwd()
	require.NoError(t, cwdErr)
	wantReal, _ := filepath.EvalSymlinks(dir)
	gotReal, _ := filepath.EvalSymlinks(cwd)
	assert.Equal(t, wantReal, gotReal,
		"chdir hook must run before peer-init error short-circuits the chain")

	require.Error(t, err, "peer-init error must still surface")
	assert.Contains(t, err.Error(), "peers.With requires cli.WithIdentity")
}
