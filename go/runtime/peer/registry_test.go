package peer_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/peer"
	"hop.top/kit/go/storage/sqlstore"
)

func newTestRegistry(t *testing.T) *peer.Registry {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	store, err := sqlstore.Open(path, sqlstore.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return peer.NewRegistry(store)
}

func testPeerInfo(id string) peer.PeerInfo {
	return peer.PeerInfo{
		ID:        id,
		Name:      "peer-" + id,
		Addrs:     []string{"localhost:9000"},
		PublicKey: []byte("-----BEGIN PUBLIC KEY-----\ntest\n-----END PUBLIC KEY-----"),
	}
}

func TestRegistry_AddGet(t *testing.T) {
	reg := newTestRegistry(t)
	info := testPeerInfo("abc123")

	require.NoError(t, reg.Add(info))

	rec, err := reg.Get("abc123")
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.Equal(t, "abc123", rec.ID)
	assert.Equal(t, "peer-abc123", rec.Name)
	assert.Equal(t, peer.Unknown, rec.Trust)
	assert.False(t, rec.FirstSeen.IsZero())
	assert.False(t, rec.LastSeen.IsZero())
}

func TestRegistry_GetNotFound(t *testing.T) {
	reg := newTestRegistry(t)
	rec, err := reg.Get("nonexistent")
	require.NoError(t, err)
	assert.Nil(t, rec)
}

func TestRegistry_List(t *testing.T) {
	reg := newTestRegistry(t)
	require.NoError(t, reg.Add(testPeerInfo("p1")))
	require.NoError(t, reg.Add(testPeerInfo("p2")))

	list, err := reg.List()
	require.NoError(t, err)
	assert.Len(t, list, 2)
}

func TestRegistry_SetTrust(t *testing.T) {
	reg := newTestRegistry(t)
	require.NoError(t, reg.Add(testPeerInfo("p1")))

	require.NoError(t, reg.SetTrust("p1", peer.Trusted))
	rec, err := reg.Get("p1")
	require.NoError(t, err)
	assert.Equal(t, peer.Trusted, rec.Trust)

	require.NoError(t, reg.SetTrust("p1", peer.Blocked))
	rec, err = reg.Get("p1")
	require.NoError(t, err)
	assert.Equal(t, peer.Blocked, rec.Trust)
}

func TestRegistry_SetTrust_NotFound(t *testing.T) {
	reg := newTestRegistry(t)
	err := reg.SetTrust("missing", peer.Trusted)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestRegistry_UpdateLastSeen(t *testing.T) {
	reg := newTestRegistry(t)
	require.NoError(t, reg.Add(testPeerInfo("p1")))

	rec1, _ := reg.Get("p1")
	require.NoError(t, reg.UpdateLastSeen("p1"))
	rec2, _ := reg.Get("p1")

	assert.True(t, !rec2.LastSeen.Before(rec1.LastSeen))
}

func TestRegistry_Remove(t *testing.T) {
	reg := newTestRegistry(t)
	require.NoError(t, reg.Add(testPeerInfo("p1")))

	require.NoError(t, reg.Remove("p1"))
	rec, err := reg.Get("p1")
	require.NoError(t, err)
	assert.Nil(t, rec)
}

func TestRegistry_Trusted(t *testing.T) {
	reg := newTestRegistry(t)
	require.NoError(t, reg.Add(testPeerInfo("p1")))
	require.NoError(t, reg.Add(testPeerInfo("p2")))
	require.NoError(t, reg.Add(testPeerInfo("p3")))

	require.NoError(t, reg.SetTrust("p1", peer.Trusted))
	require.NoError(t, reg.SetTrust("p3", peer.Trusted))

	trusted, err := reg.Trusted()
	require.NoError(t, err)
	assert.Len(t, trusted, 2)
}
