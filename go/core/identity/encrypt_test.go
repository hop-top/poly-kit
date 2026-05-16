package identity

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncryptDecrypt_Roundtrip(t *testing.T) {
	kp, err := Generate()
	require.NoError(t, err)

	key := kp.DeriveKey()
	plaintext := []byte("hello, world!")

	ct, err := Encrypt(key, plaintext)
	require.NoError(t, err)

	got, err := Decrypt(key, ct)
	require.NoError(t, err)
	assert.Equal(t, plaintext, got)
}

func TestDecrypt_WrongKey(t *testing.T) {
	kp1, err := Generate()
	require.NoError(t, err)
	kp2, err := Generate()
	require.NoError(t, err)

	ct, err := Encrypt(kp1.DeriveKey(), []byte("secret"))
	require.NoError(t, err)

	_, err = Decrypt(kp2.DeriveKey(), ct)
	assert.ErrorContains(t, err, "decryption failed")
}

func TestDecrypt_ShortCiphertext(t *testing.T) {
	var key [32]byte
	_, err := Decrypt(key, []byte("short"))
	assert.ErrorContains(t, err, "too short")
}

func TestEncryptDecrypt_EmptyPlaintext(t *testing.T) {
	kp, err := Generate()
	require.NoError(t, err)

	key := kp.DeriveKey()
	ct, err := Encrypt(key, []byte{})
	require.NoError(t, err)

	got, err := Decrypt(key, ct)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestDeriveKey_Deterministic(t *testing.T) {
	kp, err := Generate()
	require.NoError(t, err)

	k1 := kp.DeriveKey()
	k2 := kp.DeriveKey()
	assert.Equal(t, k1, k2)
}
