package identity_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/core/identity"
	"hop.top/kit/go/storage/sqlstore"
)

func TestE2E_FullRoundtrip(t *testing.T) {
	dir := t.TempDir()
	store, err := identity.NewStore(dir)
	require.NoError(t, err)

	// Generate + save.
	kp, err := identity.Generate()
	require.NoError(t, err)
	require.NoError(t, store.Save(kp))

	// Load.
	loaded, err := store.Load()
	require.NoError(t, err)
	assert.Equal(t, kp.PublicKeyID(), loaded.PublicKeyID())

	// Sign JWT.
	claims := identity.Claims{
		Subject:   "test-user",
		Issuer:    "kit-test",
		Scopes:    []string{"read", "write"},
		IssuedAt:  time.Now().Unix(),
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}
	token, err := loaded.SignJWT(claims)
	require.NoError(t, err)
	assert.NotEmpty(t, token)

	// Verify JWT.
	got, err := identity.VerifyJWT(token, loaded.PublicKey)
	require.NoError(t, err)
	assert.Equal(t, "test-user", got.Subject)
	assert.Equal(t, []string{"read", "write"}, got.Scopes)

	// Encrypt + decrypt.
	key := loaded.DeriveKey()
	plaintext := []byte("sensitive data")
	cipher, err := identity.Encrypt(key, plaintext)
	require.NoError(t, err)
	decrypted, err := identity.Decrypt(key, cipher)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestE2E_CLIWithIdentity(t *testing.T) {
	dir := t.TempDir()
	root := cli.New(cli.Config{Name: "e2e", Version: "0.1.0", Short: "e2e test", DisableValidate: true},
		cli.WithIdentity(cli.IdentityConfig{Dir: dir}))

	require.NotNil(t, root.Identity)

	// Sign JWT with CLI-managed identity.
	claims := identity.Claims{
		Subject:  root.Identity.PublicKeyID(),
		IssuedAt: time.Now().Unix(),
	}
	token, err := root.Identity.SignJWT(claims)
	require.NoError(t, err)

	got, err := identity.VerifyJWT(token, root.Identity.PublicKey)
	require.NoError(t, err)
	assert.Equal(t, root.Identity.PublicKeyID(), got.Subject)
}

func TestE2E_EncryptedStore(t *testing.T) {
	kp, err := identity.Generate()
	require.NoError(t, err)

	inner, err := sqlstore.Open(filepath.Join(t.TempDir(), "e2e.db"), sqlstore.Options{})
	require.NoError(t, err)
	defer inner.Close()

	enc := sqlstore.NewEncryptedStore(inner, kp)
	ctx := context.Background()

	require.NoError(t, enc.Put(ctx, "secret", map[string]string{"api_key": "sk-123"}))

	var out map[string]string
	found, err := enc.Get(ctx, "secret", &out)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "sk-123", out["api_key"])
}
