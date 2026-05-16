package peer_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/identity"
	"hop.top/kit/go/runtime/peer"
	"hop.top/kit/go/storage/sqlstore"
)

func newMeshSetup(t *testing.T) (*peer.Mesh, *identity.Keypair, *peer.StaticDiscoverer, *peer.Registry) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mesh.db")
	store, err := sqlstore.Open(path, sqlstore.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	self, err := identity.Generate()
	require.NoError(t, err)
	selfPub, err := self.MarshalPublicKey()
	require.NoError(t, err)

	selfInfo := peer.PeerInfo{
		ID:        self.PublicKeyID(),
		Name:      "self",
		Addrs:     []string{"localhost:8000"},
		PublicKey: selfPub,
	}

	reg := peer.NewRegistry(store)
	tm := peer.NewTrustManager(reg, self)
	disc := &peer.StaticDiscoverer{}
	m := peer.NewMesh(selfInfo, tm, disc)
	return m, self, disc, reg
}

func TestMesh_DiscoverAndConnect(t *testing.T) {
	m, _, disc, reg := newMeshSetup(t)

	remote, _ := identity.Generate()
	remotePub, _ := remote.MarshalPublicKey()
	remoteInfo := peer.PeerInfo{
		ID:        remote.PublicKeyID(),
		Name:      "remote",
		Addrs:     []string{"localhost:9000"},
		PublicKey: remotePub,
	}

	// Pre-trust the remote peer
	require.NoError(t, reg.Add(remoteInfo))
	require.NoError(t, reg.SetTrust(remoteInfo.ID, peer.Trusted))

	disc.Peers = []peer.PeerInfo{remoteInfo}

	var connected []peer.PeerInfo
	var mu sync.Mutex
	m.OnConnect(func(p peer.PeerInfo) {
		mu.Lock()
		connected = append(connected, p)
		mu.Unlock()
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	_ = m.Start(ctx)

	mu.Lock()
	assert.Len(t, connected, 1)
	assert.Equal(t, "remote", connected[0].Name)
	mu.Unlock()

	assert.Len(t, m.Peers(), 1)
}

func TestMesh_BlockedPeerSkipped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mesh2.db")
	store, err := sqlstore.Open(path, sqlstore.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	self, _ := identity.Generate()
	selfPub, _ := self.MarshalPublicKey()
	selfInfo := peer.PeerInfo{
		ID:        self.PublicKeyID(),
		Name:      "self",
		Addrs:     []string{"localhost:8000"},
		PublicKey: selfPub,
	}

	reg := peer.NewRegistry(store)
	tm := peer.NewTrustManager(reg, self)

	// Pre-add and block a peer
	blocked, _ := identity.Generate()
	blockedPub, _ := blocked.MarshalPublicKey()
	blockedInfo := peer.PeerInfo{
		ID:        blocked.PublicKeyID(),
		Name:      "blocked",
		Addrs:     []string{"localhost:9001"},
		PublicKey: blockedPub,
	}
	require.NoError(t, reg.Add(blockedInfo))
	require.NoError(t, reg.SetTrust(blockedInfo.ID, peer.Blocked))

	disc := &peer.StaticDiscoverer{Peers: []peer.PeerInfo{blockedInfo}}
	m := peer.NewMesh(selfInfo, tm, disc)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	_ = m.Start(ctx)

	assert.Empty(t, m.Peers())
}

func TestMesh_DisconnectDetected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mesh3.db")
	store, err := sqlstore.Open(path, sqlstore.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	self, _ := identity.Generate()
	selfPub, _ := self.MarshalPublicKey()
	selfInfo := peer.PeerInfo{
		ID:        self.PublicKeyID(),
		Name:      "self",
		Addrs:     []string{"localhost:8000"},
		PublicKey: selfPub,
	}

	reg := peer.NewRegistry(store)
	tm := peer.NewTrustManager(reg, self)

	remote, _ := identity.Generate()
	remotePub, _ := remote.MarshalPublicKey()
	remoteInfo := peer.PeerInfo{
		ID:        remote.PublicKeyID(),
		Name:      "remote",
		Addrs:     []string{"localhost:9000"},
		PublicKey: remotePub,
	}

	// Pre-trust the remote peer
	require.NoError(t, reg.Add(remoteInfo))
	require.NoError(t, reg.SetTrust(remoteInfo.ID, peer.Trusted))

	disc := &peer.StaticDiscoverer{Peers: []peer.PeerInfo{remoteInfo}}
	m := peer.NewMesh(selfInfo, tm, disc)

	// Start briefly to discover
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	_ = m.Start(ctx)
	assert.Len(t, m.Peers(), 1)
}
