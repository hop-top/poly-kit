//go:build mdns

package peer_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/identity"
	"hop.top/kit/go/runtime/peer"
)

func TestMDNS_AnnounceAndBrowse(t *testing.T) {
	kp, err := identity.Generate()
	require.NoError(t, err)
	pub, err := kp.MarshalPublicKey()
	require.NoError(t, err)

	self := peer.PeerInfo{
		ID:        kp.PublicKeyID(),
		Name:      "mdns-test",
		Addrs:     []string{"127.0.0.1:9999"},
		PublicKey: pub,
		Metadata:  map[string]string{"version": "1"},
	}

	disc := peer.NewMDNSDiscoverer("_kit-test._tcp")
	require.NoError(t, disc.Announce(context.Background(), self))
	defer disc.Stop()

	// Browse from a second discoverer
	disc2 := peer.NewMDNSDiscoverer("_kit-test._tcp")
	// Give mDNS a moment to propagate
	time.Sleep(500 * time.Millisecond)

	peers, err := disc2.Browse(context.Background())
	require.NoError(t, err)
	// On loopback we should find ourselves
	found := false
	for _, p := range peers {
		if p.ID == self.ID {
			found = true
			assert.Equal(t, "mdns-test", p.Name)
			assert.Equal(t, "1", p.Metadata["version"])
		}
	}
	assert.True(t, found, "expected to discover self via mDNS")
}
