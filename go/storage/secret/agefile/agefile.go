// Package agefile reads secrets from an age-encrypted YAML file.
//
// The decrypted payload is a flat map[string]string. Unlike
// secret/file + secret/local (which use NaCl secretbox keyed off a
// single identity), this backend uses age — supporting multiple
// recipients, hardware keys, and SSH-key recipients out of the box.
//
// Set/Delete are not supported: edit the encrypted file out-of-band
// (e.g. with `age -d | $EDITOR | age -e -R recipients.txt`).
package agefile

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"filippo.io/age"
	"gopkg.in/yaml.v3"

	"hop.top/kit/go/storage/secret"
)

// Store reads age-encrypted YAML.
type Store struct {
	path         string
	identityFile string
}

// New creates an age-file Store. path is the encrypted YAML file;
// identityFile holds one or more age identities used for decryption.
func New(path, identityFile string) *Store {
	return &Store{path: path, identityFile: identityFile}
}

func (s *Store) decryptAll() (map[string]string, error) {
	identBytes, err := os.ReadFile(s.identityFile)
	if err != nil {
		return nil, fmt.Errorf("agefile: read identity: %w", err)
	}
	identities, err := age.ParseIdentities(strings.NewReader(string(identBytes)))
	if err != nil {
		return nil, fmt.Errorf("agefile: parse identity: %w", err)
	}

	f, err := os.Open(s.path)
	if err != nil {
		return nil, fmt.Errorf("agefile: open: %w", err)
	}
	defer f.Close()

	dec, err := age.Decrypt(f, identities...)
	if err != nil {
		return nil, fmt.Errorf("agefile: decrypt: %w", err)
	}
	plain, err := io.ReadAll(dec)
	if err != nil {
		return nil, fmt.Errorf("agefile: read decrypted: %w", err)
	}
	var kv map[string]string
	if err := yaml.Unmarshal(plain, &kv); err != nil {
		return nil, fmt.Errorf("agefile: parse yaml: %w", err)
	}
	return kv, nil
}

// Get returns the secret value for key.
func (s *Store) Get(_ context.Context, key string) (*secret.Secret, error) {
	kv, err := s.decryptAll()
	if err != nil {
		return nil, err
	}
	v, ok := kv[key]
	if !ok {
		return nil, secret.ErrNotFound
	}
	return &secret.Secret{Key: key, Value: []byte(v)}, nil
}

// List returns all keys with the given prefix.
func (s *Store) List(_ context.Context, prefix string) ([]string, error) {
	kv, err := s.decryptAll()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(kv))
	for k := range kv {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out, nil
}

// Exists reports whether key is present in the file.
func (s *Store) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.Get(ctx, key)
	if err == secret.ErrNotFound {
		return false, nil
	}
	return err == nil, err
}

// Set is not supported: re-encrypt the YAML file out-of-band.
func (s *Store) Set(_ context.Context, _ string, _ []byte) error {
	return secret.ErrNotSupported
}

// Delete is not supported: re-encrypt the YAML file out-of-band.
func (s *Store) Delete(_ context.Context, _ string) error {
	return secret.ErrNotSupported
}

// Metadata reports the encrypted file's mtime as UpdatedAt. Scopes
// are not encoded in the agefile format so they remain unset. The
// key must exist in the decrypted YAML to avoid leaking a presence
// oracle for arbitrary keys.
func (s *Store) Metadata(ctx context.Context, key string) (secret.StoredMeta, error) {
	ok, err := s.Exists(ctx, key)
	if err != nil {
		return secret.StoredMeta{}, err
	}
	if !ok {
		return secret.StoredMeta{}, secret.ErrNotFound
	}
	meta := secret.StoredMeta{
		Key:     key,
		Source:  "agefile/" + s.path,
		Backend: "agefile",
	}
	if info, statErr := os.Stat(s.path); statErr == nil {
		meta.UpdatedAt = info.ModTime()
	}
	return meta, nil
}

var _ secret.Store = (*Store)(nil)
var _ secret.MutableStore = (*Store)(nil)
var _ secret.MetadataReader = (*Store)(nil)
