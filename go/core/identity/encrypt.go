package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
	"golang.org/x/crypto/nacl/secretbox"
)

const nonceSize = 24

// DeriveKey derives a 32-byte symmetric key from the keypair's private key.
// Uses HKDF-SHA256 with domain separation for safe key derivation.
func (k *Keypair) DeriveKey() [32]byte {
	r := hkdf.New(sha256.New, k.PrivateKey.Seed(), nil, []byte("kit-identity-encryption-v1"))
	var key [32]byte
	// hkdf.New reader only errors on length overflow; 32 bytes is always valid.
	_, _ = io.ReadFull(r, key[:])
	return key
}

// Encrypt encrypts plaintext using NaCl secretbox with a random nonce.
// Returns nonce || ciphertext.
func Encrypt(key [32]byte, plaintext []byte) ([]byte, error) {
	var nonce [nonceSize]byte
	if _, err := io.ReadFull(rand.Reader, nonce[:]); err != nil {
		return nil, fmt.Errorf("identity: generate nonce: %w", err)
	}

	sealed := secretbox.Seal(nonce[:], plaintext, &nonce, &key)
	return sealed, nil
}

// Decrypt decrypts NaCl secretbox ciphertext.
// Expects nonce || ciphertext format.
func Decrypt(key [32]byte, ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < nonceSize+secretbox.Overhead {
		return nil, errors.New("identity: ciphertext too short")
	}

	var nonce [nonceSize]byte
	copy(nonce[:], ciphertext[:nonceSize])

	plaintext, ok := secretbox.Open(nil, ciphertext[nonceSize:], &nonce, &key)
	if !ok {
		return nil, errors.New("identity: decryption failed")
	}
	return plaintext, nil
}
