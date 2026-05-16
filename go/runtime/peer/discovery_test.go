package peer_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/peer"
)

func TestPeerInfo_Validate(t *testing.T) {
	tests := []struct {
		name    string
		info    peer.PeerInfo
		wantErr string
	}{
		{
			name:    "missing ID",
			info:    peer.PeerInfo{PublicKey: []byte("key"), Addrs: []string{"localhost:9000"}},
			wantErr: "ID is required",
		},
		{
			name:    "missing PublicKey",
			info:    peer.PeerInfo{ID: "abc123", Addrs: []string{"localhost:9000"}},
			wantErr: "PublicKey is required",
		},
		{
			name:    "missing Addrs",
			info:    peer.PeerInfo{ID: "abc123", PublicKey: []byte("key")},
			wantErr: "at least one address",
		},
		{
			name: "valid",
			info: peer.PeerInfo{ID: "abc123", PublicKey: []byte("key"), Addrs: []string{"localhost:9000"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.info.Validate()
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// staticDiscoverer is a test implementation of Discoverer.
type staticDiscoverer struct {
	peers []peer.PeerInfo
}

func (s *staticDiscoverer) Announce(_ context.Context, _ peer.PeerInfo) error {
	return nil
}

func (s *staticDiscoverer) Browse(_ context.Context) ([]peer.PeerInfo, error) {
	return s.peers, nil
}

func (s *staticDiscoverer) Stop() error { return nil }

func TestDiscoverer_InterfaceCompliance(t *testing.T) {
	var d peer.Discoverer = &staticDiscoverer{
		peers: []peer.PeerInfo{{ID: "p1", PublicKey: []byte("k"), Addrs: []string{"a:1"}}},
	}

	require.NoError(t, d.Announce(context.Background(), peer.PeerInfo{}))

	peers, err := d.Browse(context.Background())
	require.NoError(t, err)
	assert.Len(t, peers, 1)
	assert.Equal(t, "p1", peers[0].ID)

	require.NoError(t, d.Stop())
}
