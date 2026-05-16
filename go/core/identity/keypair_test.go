package identity

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerate(t *testing.T) {
	kp, err := Generate()
	require.NoError(t, err)
	assert.Len(t, kp.PublicKey, 32)
	assert.Len(t, kp.PrivateKey, 64)
}

func TestPublicKeyID_Deterministic(t *testing.T) {
	kp, err := Generate()
	require.NoError(t, err)

	id1 := kp.PublicKeyID()
	id2 := kp.PublicKeyID()
	assert.Equal(t, id1, id2)
	assert.Len(t, id1, 16) // 8 bytes = 16 hex chars
}

func TestMarshalParsePublicKey_Roundtrip(t *testing.T) {
	kp, err := Generate()
	require.NoError(t, err)

	data, err := kp.MarshalPublicKey()
	require.NoError(t, err)

	pub, err := ParsePublicKey(data)
	require.NoError(t, err)
	assert.Equal(t, kp.PublicKey, pub)
}

func TestMarshalParsePrivateKey_Roundtrip(t *testing.T) {
	kp, err := Generate()
	require.NoError(t, err)

	data, err := kp.MarshalPrivateKey()
	require.NoError(t, err)

	loaded, err := ParsePrivateKey(data)
	require.NoError(t, err)
	assert.Equal(t, kp.PublicKey, loaded.PublicKey)
	assert.Equal(t, kp.PrivateKey, loaded.PrivateKey)
}

func TestParsePublicKey_InvalidPEM(t *testing.T) {
	_, err := ParsePublicKey([]byte("not pem"))
	assert.Error(t, err)
}

func TestParsePrivateKey_InvalidPEM(t *testing.T) {
	_, err := ParsePrivateKey([]byte("not pem"))
	assert.Error(t, err)
}
