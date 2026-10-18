package mcpserve_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/internal/testhome"
	"hop.top/kit/go/console/cli/mcpserve"
	"hop.top/kit/go/core/identity"
	"hop.top/kit/go/runtime/peer"
)

// TestPeerIsNeverAnMCPTool pins that no MCP caller lists, trusts,
// blocks or revokes a peer: none of the peer leaves is a tool, a call
// naming one is refused, and a pending peer stays pending.
func TestPeerIsNeverAnMCPTool(t *testing.T) {
	testhome.Isolate(t)
	dir := t.TempDir()
	run, endpoint := startMCP(t, mcpserve.Config{}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"},
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

	sess := dialMCP(t, endpoint, nil, nil)
	names := toolNames(t, sess)
	require.Contains(t, names, "ping", "the catalog is not empty")
	for _, n := range names {
		assert.False(t, n == "peer" || strings.HasPrefix(n, "peer."), "%q is a tool", n)
	}
	for _, verb := range []string{"list", "trust", "block", "revoke"} {
		res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
			Name: "peer." + verb, Arguments: map[string]any{"args": []string{id}},
		})
		assert.True(t, err != nil || res.IsError, "peer.%s must be refused", verb)
	}
	rec, err := run.root.PeerRegistry.Get(id)
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.Equal(t, peer.PendingTOFU, rec.Trust, "the peer is still waiting on a person")
}
