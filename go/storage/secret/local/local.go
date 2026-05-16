package local

import (
	"context"

	"hop.top/kit/go/core/identity"
)

// Keeper implements secret.Keeper using NaCl secretbox via identity.
type Keeper struct {
	key [32]byte
}

// NewKeeper derives an encryption key from the given keypair.
func NewKeeper(kp *identity.Keypair) *Keeper {
	return &Keeper{key: kp.DeriveKey()}
}

// Encrypt encrypts plaintext using NaCl secretbox.
func (k *Keeper) Encrypt(_ context.Context, plaintext []byte) ([]byte, error) {
	return identity.Encrypt(k.key, plaintext)
}

// Decrypt decrypts NaCl secretbox ciphertext.
func (k *Keeper) Decrypt(_ context.Context, ciphertext []byte) ([]byte, error) {
	return identity.Decrypt(k.key, ciphertext)
}
