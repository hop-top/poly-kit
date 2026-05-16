package cli_test

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/runtime/peer"
)

func TestWithPeers_AddsPeerCommandGroup(t *testing.T) {
	dir := t.TempDir()
	root := cli.New(cli.Config{Name: "test", Version: "0.1.0", Short: "t", DisableValidate: true},
		cli.WithIdentity(cli.IdentityConfig{Dir: dir}),
		cli.WithPeers(cli.PeerConfig{DataDir: dir}))

	found := false
	for _, c := range root.Cmd.Commands() {
		if c.Name() == "peer" {
			found = true
			break
		}
	}
	assert.True(t, found, "peer command group should exist")
}

func TestWithPeers_SubcommandsExist(t *testing.T) {
	dir := t.TempDir()
	root := cli.New(cli.Config{Name: "test", Version: "0.1.0", Short: "t", DisableValidate: true},
		cli.WithIdentity(cli.IdentityConfig{Dir: dir}),
		cli.WithPeers(cli.PeerConfig{DataDir: dir}))

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

func TestWithoutPeers_NoPeerCommand(t *testing.T) {
	root := cli.New(cli.Config{Name: "test", Version: "0.1.0", Short: "t", DisableValidate: true})

	for _, c := range root.Cmd.Commands() {
		assert.NotEqual(t, "peer", c.Name(), "peer should not exist without WithPeers")
	}
}

func TestWithPeers_MeshOnRoot(t *testing.T) {
	dir := t.TempDir()
	root := cli.New(cli.Config{Name: "test", Version: "0.1.0", Short: "t", DisableValidate: true},
		cli.WithIdentity(cli.IdentityConfig{Dir: dir}),
		cli.WithPeers(cli.PeerConfig{DataDir: dir}))

	assert.NotNil(t, root.Mesh, "Mesh should be set on Root")
}

func TestWithPeers_CustomDiscovery(t *testing.T) {
	dir := t.TempDir()
	d := &peer.StaticDiscoverer{}
	root := cli.New(cli.Config{Name: "test", Version: "0.1.0", Short: "t", DisableValidate: true},
		cli.WithIdentity(cli.IdentityConfig{Dir: dir}),
		cli.WithPeers(cli.PeerConfig{Discovery: d, DataDir: dir}))

	assert.NotNil(t, root.Mesh)
}

func TestWithPeers_RegistryAndTrustOnRoot(t *testing.T) {
	dir := t.TempDir()
	root := cli.New(cli.Config{Name: "test", Version: "0.1.0", Short: "t", DisableValidate: true},
		cli.WithIdentity(cli.IdentityConfig{Dir: dir}),
		cli.WithPeers(cli.PeerConfig{DataDir: dir}))

	assert.NotNil(t, root.PeerRegistry)
	assert.NotNil(t, root.PeerTrust)
}
