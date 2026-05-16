package keyring

import (
	"context"

	"github.com/zalando/go-keyring"

	"hop.top/kit/go/storage/secret"
)

// Store is an OS keychain-backed MutableStore.
type Store struct {
	service string
}

// New returns a Store using the given keyring service name.
func New(service string) *Store {
	return &Store{service: service}
}

func (s *Store) Get(_ context.Context, key string) (*secret.Secret, error) {
	v, err := keyring.Get(s.service, key)
	if err != nil {
		if err == keyring.ErrNotFound {
			return nil, secret.ErrNotFound
		}
		return nil, err
	}
	return &secret.Secret{Key: key, Value: []byte(v)}, nil
}

func (s *Store) Set(_ context.Context, key string, value []byte) error {
	return keyring.Set(s.service, key, string(value))
}

func (s *Store) Delete(_ context.Context, key string) error {
	err := keyring.Delete(s.service, key)
	if err == keyring.ErrNotFound {
		return secret.ErrNotFound
	}
	return err
}

func (s *Store) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.Get(ctx, key)
	if err != nil {
		if err == secret.ErrNotFound {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *Store) List(_ context.Context, _ string) ([]string, error) {
	return nil, secret.ErrNotSupported
}

// Metadata returns descriptive info about the keyring item. The
// underlying go-keyring library does not surface OS-level attributes
// (creation date, comment field, ACL), so we report what we can derive
// statically: key, source, backend. The presence check is honored via
// the keyring Get call so a missing key returns ErrNotFound.
func (s *Store) Metadata(ctx context.Context, key string) (secret.StoredMeta, error) {
	ok, err := s.Exists(ctx, key)
	if err != nil {
		return secret.StoredMeta{}, err
	}
	if !ok {
		return secret.StoredMeta{}, secret.ErrNotFound
	}
	return secret.StoredMeta{
		Key:     key,
		Source:  "keyring/" + s.service,
		Backend: "keyring",
	}, nil
}

var _ secret.MetadataReader = (*Store)(nil)
