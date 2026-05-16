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

// TestE2E_MeshTrustDiscovery exercises the full flow:
// discover peer → establish TOFU trust → mesh connects → verify bidirectional.
func TestE2E_MeshTrustDiscovery(t *testing.T) {
	// Setup peer A
	kpA, err := identity.Generate()
	require.NoError(t, err)
	pubA, _ := kpA.MarshalPublicKey()
	infoA := peer.PeerInfo{
		ID:        kpA.PublicKeyID(),
		Name:      "peer-a",
		Addrs:     []string{"127.0.0.1:8001"},
		PublicKey: pubA,
	}
	storeA, err := sqlstore.Open(filepath.Join(t.TempDir(), "a.db"), sqlstore.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { storeA.Close() })
	regA := peer.NewRegistry(storeA)
	tmA := peer.NewTrustManager(regA, kpA)

	// Setup peer B
	kpB, err := identity.Generate()
	require.NoError(t, err)
	pubB, _ := kpB.MarshalPublicKey()
	infoB := peer.PeerInfo{
		ID:        kpB.PublicKeyID(),
		Name:      "peer-b",
		Addrs:     []string{"127.0.0.1:8002"},
		PublicKey: pubB,
	}
	storeB, err := sqlstore.Open(filepath.Join(t.TempDir(), "b.db"), sqlstore.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { storeB.Close() })
	regB := peer.NewRegistry(storeB)
	tmB := peer.NewTrustManager(regB, kpB)

	// Pre-trust peers (simulating user approval after PendingTOFU)
	require.NoError(t, regA.Add(infoB))
	require.NoError(t, regA.SetTrust(infoB.ID, peer.Trusted))
	require.NoError(t, regB.Add(infoA))
	require.NoError(t, regB.SetTrust(infoA.ID, peer.Trusted))

	// A discovers B; B discovers A
	discA := &peer.StaticDiscoverer{Peers: []peer.PeerInfo{infoB}}
	discB := &peer.StaticDiscoverer{Peers: []peer.PeerInfo{infoA}}

	meshA := peer.NewMesh(infoA, tmA, discA)
	meshB := peer.NewMesh(infoB, tmB, discB)

	var connectedA, connectedB []peer.PeerInfo
	var muA, muB sync.Mutex

	meshA.OnConnect(func(p peer.PeerInfo) {
		muA.Lock()
		connectedA = append(connectedA, p)
		muA.Unlock()
	})
	meshB.OnConnect(func(p peer.PeerInfo) {
		muB.Lock()
		connectedB = append(connectedB, p)
		muB.Unlock()
	})

	ctx, cancel := context.WithCancel(context.Background())

	go func() { _ = meshA.Start(ctx) }()
	go func() { _ = meshB.Start(ctx) }()

	time.Sleep(200 * time.Millisecond)
	cancel()

	// Both meshes should have discovered each other
	muA.Lock()
	assert.Len(t, connectedA, 1, "A should discover B")
	if len(connectedA) > 0 {
		assert.Equal(t, "peer-b", connectedA[0].Name)
	}
	muA.Unlock()

	muB.Lock()
	assert.Len(t, connectedB, 1, "B should discover A")
	if len(connectedB) > 0 {
		assert.Equal(t, "peer-a", connectedB[0].Name)
	}
	muB.Unlock()

	// Verify TOFU trust was established
	trustedA, err := tmA.IsTrusted(infoB.ID)
	require.NoError(t, err)
	assert.True(t, trustedA, "A should trust B via TOFU")

	trustedB, err := tmB.IsTrusted(infoA.ID)
	require.NoError(t, err)
	assert.True(t, trustedB, "B should trust A via TOFU")

	// Verify challenge-response between peers
	challengeA, err := tmA.Challenge(infoB.ID)
	require.NoError(t, err)
	require.NoError(t, tmB.VerifyChallenge(challengeA, pubA))

	challengeB, err := tmB.Challenge(infoA.ID)
	require.NoError(t, err)
	require.NoError(t, tmA.VerifyChallenge(challengeB, pubB))
}
