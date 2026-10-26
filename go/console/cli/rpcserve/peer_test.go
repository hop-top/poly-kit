package rpcserve_test

import (
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/internal/testhome"
	"hop.top/kit/go/console/cli/rpcserve"
	"hop.top/kit/go/core/identity"
	"hop.top/kit/go/runtime/peer"
	"hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1"
)

// TestPeerIsNeverServedOverRPC pins that no rpc caller lists, trusts,
// blocks or revokes a peer: each path answers not_found, and a pending
// peer stays pending.
func TestPeerIsNeverServedOverRPC(t *testing.T) {
	testhome.Isolate(t)
	dir := t.TempDir()
	run, base := startRPC(t, rpcserve.With(rpcserve.Config{}), []string{"rpc", "--rpc-addr", "127.0.0.1:0"},
		cli.WithIdentity(cli.IdentityConfig{Dir: filepath.Join(dir, "identity")}),
		cli.WithPeers(cli.PeerConfig{DataDir: filepath.Join(dir, "peers")}))

	stranger, err := identity.Generate()
	require.NoError(t, err)
	pub, err := stranger.MarshalPublicKey()
	require.NoError(t, err)
	id := stranger.PublicKeyID()
	require.NoError(t, run.root.PeerTrust.AcceptTOFU(peer.PeerInfo{
		ID: id, Name: "evil", Addrs: []string{"127.0.0.1:1"}, PublicKey: pub,
	}))

	c := client(base, protocols[0])
	for _, verb := range []string{"list", "trust", "block", "revoke"} {
		_, err := c.Invoke(t.Context(), connect.NewRequest(&cmdsurfacev1.Invocation{
			Path: []string{"peer", verb}, Args: []string{id},
		}))
		require.Error(t, err, "peer %s ran over rpc", verb)
		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "peer %s", verb)
	}
	rec, err := run.root.PeerRegistry.Get(id)
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.Equal(t, peer.PendingTOFU, rec.Trust, "the peer is still waiting on a person")
}
