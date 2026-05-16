package sqlstore

import (
	"context"
	"encoding/json"
	"fmt"

	"hop.top/kit/go/core/identity"
)

// EncryptedStore wraps a Store with at-rest encryption.
// Values are JSON-marshaled, encrypted, then stored as raw bytes
// in the inner store. Keys are stored in plaintext.
type EncryptedStore struct {
	inner *Store
	key   [32]byte
}

// NewEncryptedStore wraps an existing store with encryption.
// The symmetric key is derived from the keypair's private key.
func NewEncryptedStore(inner *Store, kp *identity.Keypair) *EncryptedStore {
	return &EncryptedStore{
		inner: inner,
		key:   kp.DeriveKey(),
	}
}

// Put JSON-marshals v, encrypts the bytes, and stores via the inner store.
func (s *EncryptedStore) Put(ctx context.Context, key string, v any) error {
	plain, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encrypted store: marshal: %w", err)
	}
	cipher, err := identity.Encrypt(s.key, plain)
	if err != nil {
		return fmt.Errorf("encrypted store: encrypt: %w", err)
	}
	return s.inner.Put(ctx, key, cipher)
}

// Get reads from the inner store, decrypts, and JSON-unmarshals into dst.
func (s *EncryptedStore) Get(ctx context.Context, key string, dst any) (bool, error) {
	var cipher []byte
	found, err := s.inner.Get(ctx, key, &cipher)
	if !found || err != nil {
		return found, err
	}
	plain, err := identity.Decrypt(s.key, cipher)
	if err != nil {
		return false, fmt.Errorf("encrypted store: decrypt: %w", err)
	}
	return true, json.Unmarshal(plain, dst)
}

// Close delegates to the inner store.
func (s *EncryptedStore) Close() error { return s.inner.Close() }
