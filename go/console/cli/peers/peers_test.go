package peers_test

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/peers"
	"hop.top/kit/go/runtime/peer"
)

func TestWith_AddsPeerCommandGroup(t *testing.T) {
	dir := t.TempDir()
	root := cli.New(cli.Config{Name: "test", Version: "0.1.0", Short: "t", DisableValidate: true},
		cli.WithIdentity(cli.IdentityConfig{Dir: dir}),
		peers.With(peers.Config{DataDir: dir}))

	found := false
	for _, c := range root.Cmd.Commands() {
		if c.Name() == "peer" {
			found = true
			break
		}
	}
	assert.True(t, found, "peer command group should exist")
}

func TestWith_SubcommandsExist(t *testing.T) {
	dir := t.TempDir()
	root := cli.New(cli.Config{Name: "test", Version: "0.1.0", Short: "t", DisableValidate: true},
		cli.WithIdentity(cli.IdentityConfig{Dir: dir}),
		peers.With(peers.Config{DataDir: dir}))

	var peerCmd *cobra.Command
	for _, c := range root.Cmd.Commands() {
		if c.Name() == "peer" {
			peerCmd = c
			break
		}
	}
	require.NotNil(t, peerCmd)

	subs := map[string]bool{}
	for _, c := range peerCmd.Commands() {
		subs[c.Name()] = true
	}
	assert.True(t, subs["list"], "peer list should exist")
	assert.True(t, subs["trust"], "peer trust should exist")
	assert.True(t, subs["block"], "peer block should exist")
	assert.True(t, subs["revoke"], "peer revoke should exist")
}

func TestWithout_NoPeerCommand(t *testing.T) {
	root := cli.New(cli.Config{Name: "test", Version: "0.1.0", Short: "t", DisableValidate: true})

	for _, c := range root.Cmd.Commands() {
		assert.NotEqual(t, "peer", c.Name(), "peer should not exist without peers.With")
	}
}

func TestWith_MeshOnRoot(t *testing.T) {
	dir := t.TempDir()
	root := cli.New(cli.Config{Name: "test", Version: "0.1.0", Short: "t", DisableValidate: true},
		cli.WithIdentity(cli.IdentityConfig{Dir: dir}),
		peers.With(peers.Config{DataDir: dir}))

	assert.NotNil(t, peers.Of(root).Mesh, "Mesh should be set on Root")
}

func TestWith_CustomDiscovery(t *testing.T) {
	dir := t.TempDir()
	d := &peer.StaticDiscoverer{}
	root := cli.New(cli.Config{Name: "test", Version: "0.1.0", Short: "t", DisableValidate: true},
		cli.WithIdentity(cli.IdentityConfig{Dir: dir}),
		peers.With(peers.Config{Discovery: d, DataDir: dir}))

	assert.NotNil(t, peers.Of(root).Mesh)
}

func TestWith_RegistryAndTrustOnRoot(t *testing.T) {
	dir := t.TempDir()
	root := cli.New(cli.Config{Name: "test", Version: "0.1.0", Short: "t", DisableValidate: true},
		cli.WithIdentity(cli.IdentityConfig{Dir: dir}),
		peers.With(peers.Config{DataDir: dir}))

	assert.NotNil(t, peers.Of(root).Registry)
	assert.NotNil(t, peers.Of(root).Trust)
}

// Of never returns nil: on a Root without peers.With, on a literal
// Root and on a nil Root it returns empty Peers, so a "peers
// configured" check reads the field, as it read the Root field before.
func TestOf_WithoutPeersIsEmptyNotNil(t *testing.T) {
	built := cli.New(cli.Config{Name: "test", Version: "0.1.0", Short: "t", DisableValidate: true})
	for name, r := range map[string]*cli.Root{
		"built by New": built,
		"literal Root": {Cmd: &cobra.Command{Use: "bare"}},
		"nil Root":     nil,
	} {
		t.Run(name, func(t *testing.T) {
			p := peers.Of(r)
			require.NotNil(t, p)
			assert.Nil(t, p.Mesh)
			assert.Nil(t, p.Registry)
			assert.Nil(t, p.Trust)
		})
	}
	// Asking does not attach: a later Of still finds nothing.
	_, attached := cli.Extension(built, peers.Key)
	assert.False(t, attached, "Of attaches nothing")
}

// Of returns the same Peers that Init filled, so fields read through
// it are the live mesh, registry and trust manager.
func TestOf_ReturnsTheInitialisedPeers(t *testing.T) {
	dir := t.TempDir()
	root := cli.New(cli.Config{Name: "test", Version: "0.1.0", Short: "t", DisableValidate: true},
		cli.WithIdentity(cli.IdentityConfig{Dir: dir}),
		peers.With(peers.Config{DataDir: dir}))
	assert.Same(t, peers.Of(root), peers.Of(root))
	assert.Same(t, peers.Of(root).Mesh, peers.Of(root).Mesh)
}

// With is order-independent with cli.WithIdentity: Init runs after
// every option, once identity is resolved.
func TestWith_BeforeWithIdentity(t *testing.T) {
	dir := t.TempDir()
	root := cli.New(cli.Config{Name: "test", Version: "0.1.0", Short: "t", DisableValidate: true},
		peers.With(peers.Config{DataDir: dir}),
		cli.WithIdentity(cli.IdentityConfig{Dir: dir}))
	assert.NotNil(t, peers.Of(root).Mesh)
}

// The peer group is mounted after New's reserved snapshot, so it stays
// unreserved, as it was when cli mounted it.
func TestWith_PeerGroupIsNotReserved(t *testing.T) {
	dir := t.TempDir()
	root := cli.New(cli.Config{Name: "test", Version: "0.1.0", Short: "t", DisableValidate: true},
		cli.WithIdentity(cli.IdentityConfig{Dir: dir}),
		peers.With(peers.Config{DataDir: dir}))
	assert.False(t, root.IsReserved("peer"))
}
