package sqlstore_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/identity"
	"hop.top/kit/go/storage/sqlstore"
)

func openEncrypted(t *testing.T, kp *identity.Keypair) (*sqlstore.EncryptedStore, *sqlstore.Store) {
	t.Helper()
	inner, err := sqlstore.Open(filepath.Join(t.TempDir(), "enc.db"), sqlstore.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { inner.Close() })
	return sqlstore.NewEncryptedStore(inner, kp), inner
}

func TestEncryptedStore_PutGet(t *testing.T) {
	kp, err := identity.Generate()
	require.NoError(t, err)

	enc, _ := openEncrypted(t, kp)
	ctx := context.Background()

	require.NoError(t, enc.Put(ctx, "secret", map[string]string{"msg": "hello"}))

	var out map[string]string
	found, err := enc.Get(ctx, "secret", &out)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "hello", out["msg"])
}

func TestEncryptedStore_RawValueIsNotPlaintext(t *testing.T) {
	kp, err := identity.Generate()
	require.NoError(t, err)

	enc, inner := openEncrypted(t, kp)
	ctx := context.Background()

	require.NoError(t, enc.Put(ctx, "secret", "plaintext-value"))

	// Read raw value from inner store — should not contain plaintext.
	var raw []byte
	found, err := inner.Get(ctx, "secret", &raw)
	require.NoError(t, err)
	require.True(t, found)
	assert.NotContains(t, string(raw), "plaintext-value")
}

func TestEncryptedStore_DifferentKeypairCannotDecrypt(t *testing.T) {
	kp1, err := identity.Generate()
	require.NoError(t, err)
	kp2, err := identity.Generate()
	require.NoError(t, err)

	// Use same underlying DB file.
	dbPath := filepath.Join(t.TempDir(), "shared.db")
	inner, err := sqlstore.Open(dbPath, sqlstore.Options{})
	require.NoError(t, err)
	defer inner.Close()

	enc1 := sqlstore.NewEncryptedStore(inner, kp1)
	ctx := context.Background()

	require.NoError(t, enc1.Put(ctx, "data", "secret-payload"))

	// Try to decrypt with different key.
	enc2 := sqlstore.NewEncryptedStore(inner, kp2)
	var out string
	_, err = enc2.Get(ctx, "data", &out)
	assert.Error(t, err)
}

func TestEncryptedStore_MissingKey(t *testing.T) {
	kp, err := identity.Generate()
	require.NoError(t, err)

	enc, _ := openEncrypted(t, kp)
	ctx := context.Background()

	var out string
	found, err := enc.Get(ctx, "nonexistent", &out)
	require.NoError(t, err)
	assert.False(t, found)
}
