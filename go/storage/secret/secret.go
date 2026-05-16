package secret

import (
	"context"
	"errors"
)

// ErrNotFound is returned when a secret doesn't exist.
var ErrNotFound = errors.New("secret: not found")

// ErrNotSupported is returned when an operation is not supported by the backend.
var ErrNotSupported = errors.New("secret: not supported")

// Secret is a retrieved secret value.
type Secret struct {
	Key      string
	Value    []byte
	Metadata map[string]string
}

// Store retrieves secrets from a backend.
type Store interface {
	Get(ctx context.Context, key string) (*Secret, error)
	List(ctx context.Context, prefix string) ([]string, error)
	Exists(ctx context.Context, key string) (bool, error)
}

// MutableStore can also write secrets.
type MutableStore interface {
	Store
	Set(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
}

// Keeper manages encryption/decryption of secrets at rest.
type Keeper interface {
	Encrypt(ctx context.Context, plaintext []byte) ([]byte, error)
	Decrypt(ctx context.Context, ciphertext []byte) ([]byte, error)
}
