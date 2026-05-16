package env

import (
	"context"
	"fmt"
	"os"
	"strings"

	"hop.top/kit/go/storage/secret"
)

// Store reads secrets from environment variables.
type Store struct {
	prefix string
}

// New returns a Store that reads env vars with the given prefix.
func New(prefix string) *Store {
	return &Store{prefix: prefix}
}

func (s *Store) envKey(key string) string {
	return s.prefix + strings.ToUpper(strings.ReplaceAll(key, "/", "_"))
}

// Get retrieves a secret from an environment variable.
func (s *Store) Get(_ context.Context, key string) (*secret.Secret, error) {
	v, ok := os.LookupEnv(s.envKey(key))
	if !ok {
		return nil, secret.ErrNotFound
	}
	return &secret.Secret{Key: key, Value: []byte(v)}, nil
}

// List returns keys matching the combined store prefix and filter prefix.
func (s *Store) List(_ context.Context, prefix string) ([]string, error) {
	full := s.prefix + strings.ToUpper(strings.ReplaceAll(prefix, "/", "_"))
	var keys []string
	for _, e := range os.Environ() {
		k, _, _ := strings.Cut(e, "=")
		if strings.HasPrefix(k, full) {
			// strip store prefix, return remainder lowercased
			keys = append(keys, strings.ToLower(strings.TrimPrefix(k, s.prefix)))
		}
	}
	return keys, nil
}

// Exists reports whether the env var exists.
func (s *Store) Exists(_ context.Context, key string) (bool, error) {
	_, ok := os.LookupEnv(s.envKey(key))
	return ok, nil
}

// Metadata always returns ErrNotSupported for the env backend:
// environment variables carry no expiry, scope, or provenance metadata
// beyond the name itself, which is already in the key.
func (s *Store) Metadata(_ context.Context, _ string) (secret.StoredMeta, error) {
	return secret.StoredMeta{}, fmt.Errorf("env backend: %w", secret.ErrNotSupported)
}

var _ secret.MetadataReader = (*Store)(nil)
