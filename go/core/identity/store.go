package identity

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"hop.top/kit/go/core/xdg"
)

// ErrNotFound indicates no keypair exists at the expected path.
var ErrNotFound = errors.New("identity: keypair not found")

// Store manages keypair persistence in the filesystem.
type Store struct {
	dir string
}

// NewStore creates a Store at the given directory.
// Creates the directory with 0700 if it doesn't exist.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("identity: create store dir: %w", err)
	}
	return &Store{dir: dir}, nil
}

// DefaultStore returns a Store at xdg.DataHome/kit/identity/.
func DefaultStore() (*Store, error) {
	base, err := xdg.DataDir("kit")
	if err != nil {
		return nil, fmt.Errorf("identity: resolve data dir: %w", err)
	}
	return NewStore(filepath.Join(base, "identity"))
}

// Save writes keypair to disk atomically (private key 0600, public key 0644).
// Uses temp file + rename to prevent partial writes.
func (s *Store) Save(kp *Keypair) error {
	privPEM, err := kp.MarshalPrivateKey()
	if err != nil {
		return err
	}
	pubPEM, err := kp.MarshalPublicKey()
	if err != nil {
		return err
	}

	privPath := filepath.Join(s.dir, "id_ed25519")
	pubPath := filepath.Join(s.dir, "id_ed25519.pub")

	if err := atomicWrite(privPath, privPEM, 0600); err != nil {
		return fmt.Errorf("identity: write private key: %w", err)
	}
	if err := atomicWrite(pubPath, pubPEM, 0644); err != nil {
		return fmt.Errorf("identity: write public key: %w", err)
	}
	return nil
}

// atomicWrite writes data to a temp file then renames to dst.
func atomicWrite(dst string, data []byte, perm os.FileMode) error {
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// Load reads keypair from disk. Returns ErrNotFound if no keypair exists.
func (s *Store) Load() (*Keypair, error) {
	privPath := filepath.Join(s.dir, "id_ed25519")
	data, err := os.ReadFile(privPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("identity: read private key: %w", err)
	}
	return ParsePrivateKey(data)
}

// LoadOrGenerate loads existing keypair or generates + saves a new one.
func (s *Store) LoadOrGenerate() (*Keypair, error) {
	kp, err := s.Load()
	if err == nil {
		return kp, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	kp, err = Generate()
	if err != nil {
		return nil, err
	}
	if err := s.Save(kp); err != nil {
		return nil, err
	}
	return kp, nil
}

// Exists returns true if a keypair is stored.
func (s *Store) Exists() bool {
	privPath := filepath.Join(s.dir, "id_ed25519")
	_, err := os.Stat(privPath)
	return err == nil
}
