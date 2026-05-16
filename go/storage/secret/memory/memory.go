package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"hop.top/kit/go/storage/secret"
)

// Store is an in-memory MutableStore for testing.
type Store struct {
	mu      sync.RWMutex
	secrets map[string][]byte
}

// New returns an initialized in-memory Store.
func New() *Store {
	return &Store{secrets: make(map[string][]byte)}
}

func (s *Store) Get(_ context.Context, key string) (*secret.Secret, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.secrets[key]
	if !ok {
		return nil, secret.ErrNotFound
	}
	cp := make([]byte, len(v))
	copy(cp, v)
	return &secret.Secret{Key: key, Value: cp}, nil
}

func (s *Store) List(_ context.Context, prefix string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var keys []string
	for k := range s.secrets {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

func (s *Store) Exists(_ context.Context, key string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.secrets[key]
	return ok, nil
}

func (s *Store) Set(_ context.Context, key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]byte, len(value))
	copy(cp, value)
	s.secrets[key] = cp
	return nil
}

func (s *Store) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.secrets[key]; !ok {
		return secret.ErrNotFound
	}
	delete(s.secrets, key)
	return nil
}

// Metadata always returns ErrNotSupported for the memory backend:
// in-memory storage carries no provenance, expiry, or scope info that
// could meaningfully populate a StoredMeta.
func (s *Store) Metadata(_ context.Context, _ string) (secret.StoredMeta, error) {
	return secret.StoredMeta{}, fmt.Errorf("memory backend: %w", secret.ErrNotSupported)
}

var _ secret.MetadataReader = (*Store)(nil)
