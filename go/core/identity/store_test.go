package identity

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStore_SaveLoad_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	require.NoError(t, err)

	kp, err := Generate()
	require.NoError(t, err)

	require.NoError(t, store.Save(kp))

	loaded, err := store.Load()
	require.NoError(t, err)
	assert.Equal(t, kp.PublicKey, loaded.PublicKey)
	assert.Equal(t, kp.PrivateKey, loaded.PrivateKey)
}

func TestStore_Permissions(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	require.NoError(t, err)

	kp, err := Generate()
	require.NoError(t, err)
	require.NoError(t, store.Save(kp))

	privInfo, err := os.Stat(filepath.Join(dir, "id_ed25519"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), privInfo.Mode().Perm())

	pubInfo, err := os.Stat(filepath.Join(dir, "id_ed25519.pub"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0644), pubInfo.Mode().Perm())
}

func TestStore_Load_NotFound(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	require.NoError(t, err)

	_, err = store.Load()
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestStore_LoadOrGenerate(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	require.NoError(t, err)

	// First call generates
	kp1, err := store.LoadOrGenerate()
	require.NoError(t, err)
	assert.True(t, store.Exists())

	// Second call loads the same
	kp2, err := store.LoadOrGenerate()
	require.NoError(t, err)
	assert.Equal(t, kp1.PublicKey, kp2.PublicKey)
}

func TestStore_DirectoryCreation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "dir")
	store, err := NewStore(dir)
	require.NoError(t, err)
	assert.NotNil(t, store)

	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
	assert.Equal(t, os.FileMode(0700), info.Mode().Perm())
}

func TestStore_Exists(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	require.NoError(t, err)

	assert.False(t, store.Exists())

	kp, err := Generate()
	require.NoError(t, err)
	require.NoError(t, store.Save(kp))

	assert.True(t, store.Exists())
}
