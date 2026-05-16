package file

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"hop.top/kit/go/storage/secret"
)

// Store reads and writes secrets as files on disk.
type Store struct {
	dir    string
	keeper secret.Keeper
}

// New returns a Store rooted at dir. If keeper is non-nil, values are
// encrypted at rest.
func New(dir string, keeper secret.Keeper) *Store {
	return &Store{dir: filepath.Clean(dir), keeper: keeper}
}

func (s *Store) resolve(key string) (string, error) {
	p := filepath.Join(s.dir, filepath.FromSlash(key))
	if !strings.HasPrefix(p, s.dir+string(os.PathSeparator)) && p != s.dir {
		return "", fmt.Errorf("key %q escapes store root", key)
	}
	return p, nil
}

// Get reads a secret file, decrypting if a keeper is configured.
func (s *Store) Get(ctx context.Context, key string) (*secret.Secret, error) {
	p, err := s.resolve(key)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, secret.ErrNotFound
		}
		return nil, err
	}
	if s.keeper != nil {
		data, err = s.keeper.Decrypt(ctx, data)
		if err != nil {
			return nil, err
		}
	}
	return &secret.Secret{Key: key, Value: data}, nil
}

// List returns keys whose relative path matches the given prefix,
// walking subdirectories created by Set.
func (s *Store) List(_ context.Context, prefix string) ([]string, error) {
	var keys []string
	err := filepath.WalkDir(s.dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && p != s.dir {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(filepath.Base(p), ".") {
			return nil
		}
		rel, err := filepath.Rel(s.dir, p)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if matchesPrefix(key, prefix) {
			keys = append(keys, key)
		}
		return nil
	})
	if err != nil && os.IsNotExist(err) {
		return nil, nil
	}
	return keys, err
}

// Exists reports whether the secret file exists.
func (s *Store) Exists(_ context.Context, key string) (bool, error) {
	p, err := s.resolve(key)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Set writes a secret file, encrypting if a keeper is configured.
func (s *Store) Set(ctx context.Context, key string, value []byte) error {
	p, err := s.resolve(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	data := value
	if s.keeper != nil {
		var err error
		data, err = s.keeper.Encrypt(ctx, value)
		if err != nil {
			return err
		}
	}
	return os.WriteFile(p, data, 0600)
}

// matchesPrefix reports whether key matches the given prefix respecting
// path boundaries. When prefix ends with "/" or contains no "/" (flat
// keys), plain prefix match is used. Otherwise key must equal prefix
// or have prefix followed by "/".
func matchesPrefix(key, prefix string) bool {
	if prefix == "" || strings.HasSuffix(prefix, "/") {
		return strings.HasPrefix(key, prefix)
	}
	if !strings.Contains(prefix, "/") {
		return strings.HasPrefix(key, prefix)
	}
	return key == prefix || strings.HasPrefix(key, prefix+"/")
}

// Delete removes a secret file.
func (s *Store) Delete(_ context.Context, key string) error {
	p, err := s.resolve(key)
	if err != nil {
		return err
	}
	err = os.Remove(p)
	if os.IsNotExist(err) {
		return secret.ErrNotFound
	}
	return err
}

// Metadata reports the on-disk file's mtime as UpdatedAt. Scopes and
// expiry are not encoded in the file format so they remain unset.
func (s *Store) Metadata(_ context.Context, key string) (secret.StoredMeta, error) {
	p, err := s.resolve(key)
	if err != nil {
		return secret.StoredMeta{}, err
	}
	info, err := os.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return secret.StoredMeta{}, secret.ErrNotFound
		}
		return secret.StoredMeta{}, err
	}
	return secret.StoredMeta{
		Key:       key,
		Source:    "file/" + p,
		Backend:   "file",
		UpdatedAt: info.ModTime(),
	}, nil
}

var _ secret.MetadataReader = (*Store)(nil)
