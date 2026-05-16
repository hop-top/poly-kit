package peer_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/identity"
	"hop.top/kit/go/runtime/peer"
	"hop.top/kit/go/storage/sqlstore"
)

func newTestTrustManager(t *testing.T) (*peer.TrustManager, *peer.Registry, *identity.Keypair) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "trust.db")
	store, err := sqlstore.Open(path, sqlstore.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	kp, err := identity.Generate()
	require.NoError(t, err)

	reg := peer.NewRegistry(store)
	tm := peer.NewTrustManager(reg, kp)
	return tm, reg, kp
}

func peerInfoFromKeypair(kp *identity.Keypair) peer.PeerInfo {
	pub, _ := kp.MarshalPublicKey()
	return peer.PeerInfo{
		ID:        kp.PublicKeyID(),
		Name:      "test-peer",
		Addrs:     []string{"localhost:8000"},
		PublicKey: pub,
	}
}

func TestTOFU_FirstEncounter(t *testing.T) {
	tm, reg, _ := newTestTrustManager(t)
	remote, _ := identity.Generate()
	info := peerInfoFromKeypair(remote)

	require.NoError(t, tm.AcceptTOFU(info))

	rec, err := reg.Get(info.ID)
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.Equal(t, peer.PendingTOFU, rec.Trust)
}

func TestTOFU_RepeatSameKey(t *testing.T) {
	tm, _, _ := newTestTrustManager(t)
	remote, _ := identity.Generate()
	info := peerInfoFromKeypair(remote)

	require.NoError(t, tm.AcceptTOFU(info))
	require.NoError(t, tm.AcceptTOFU(info)) // same key = ok
}

func TestTOFU_RejectKeyMismatch(t *testing.T) {
	tm, _, _ := newTestTrustManager(t)
	remote, _ := identity.Generate()
	info := peerInfoFromKeypair(remote)

	require.NoError(t, tm.AcceptTOFU(info))

	// Different key, same ID
	imposter, _ := identity.Generate()
	imposterPub, _ := imposter.MarshalPublicKey()
	info.PublicKey = imposterPub

	err := tm.AcceptTOFU(info)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pubkey mismatch")
}

func TestTrust_ExplicitTrustBlockRevoke(t *testing.T) {
	tm, reg, _ := newTestTrustManager(t)
	remote, _ := identity.Generate()
	info := peerInfoFromKeypair(remote)
	require.NoError(t, reg.Add(info))

	require.NoError(t, tm.Trust(info.ID))
	ok, err := tm.IsTrusted(info.ID)
	require.NoError(t, err)
	assert.True(t, ok)

	require.NoError(t, tm.Block(info.ID))
	ok, err = tm.IsTrusted(info.ID)
	require.NoError(t, err)
	assert.False(t, ok)

	require.NoError(t, tm.Revoke(info.ID))
	rec, _ := reg.Get(info.ID)
	assert.Equal(t, peer.Unknown, rec.Trust)
}

func TestChallenge_Roundtrip(t *testing.T) {
	tm, _, self := newTestTrustManager(t)
	selfPub, err := self.MarshalPublicKey()
	require.NoError(t, err)

	// Challenge targets self (simulating remote verifying our challenge)
	token, err := tm.Challenge(self.PublicKeyID())
	require.NoError(t, err)
	assert.NotEmpty(t, token)

	require.NoError(t, tm.VerifyChallenge(token, selfPub))
}

func TestChallenge_WrongKey(t *testing.T) {
	tm, _, self := newTestTrustManager(t)
	other, _ := identity.Generate()
	otherPub, _ := other.MarshalPublicKey()

	token, err := tm.Challenge(self.PublicKeyID())
	require.NoError(t, err)

	err = tm.VerifyChallenge(token, otherPub)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "verification failed")
}

func TestVerify(t *testing.T) {
	tm, reg, _ := newTestTrustManager(t)
	remote, _ := identity.Generate()
	info := peerInfoFromKeypair(remote)
	require.NoError(t, reg.Add(info))

	ok, err := tm.Verify(info)
	require.NoError(t, err)
	assert.True(t, ok)

	// Unknown peer
	ok, err = tm.Verify(peer.PeerInfo{ID: "unknown", PublicKey: []byte("x"), Addrs: []string{"a:1"}})
	require.NoError(t, err)
	assert.False(t, ok)
}
