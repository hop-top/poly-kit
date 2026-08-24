// Package local implements blob.Store using the local filesystem.
//
// Keys may contain path separators (e.g. "a/b/c"); intermediate
// directories are created automatically on Put.
package local

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"hop.top/kit/go/storage/blob"
)

// Store is a filesystem-backed blob store rooted at a directory.
type Store struct {
	root string
}

// New returns a Store rooted at dir. The directory is created if needed.
func New(dir string) (*Store, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("blob/local: abs root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, fmt.Errorf("blob/local: mkdir root: %w", err)
	}
	return &Store{root: abs}, nil
}

// isTempName reports whether name is an in-flight (or crash-orphaned) Put
// staging file, which must never surface as a key.
func isTempName(name string) bool {
	return strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".tmp")
}

func (s *Store) resolve(key string) (string, error) {
	resolved := filepath.Join(s.root, filepath.FromSlash(key))
	if !strings.HasPrefix(resolved, s.root+string(os.PathSeparator)) && resolved != s.root {
		return "", fmt.Errorf("blob/local: key %q escapes store root", key)
	}
	return resolved, nil
}

// Put writes the contents of r to key, creating subdirectories as needed.
//
// The write is atomic: contents land in a sibling temp file that is synced
// and then renamed over the destination. A concurrent Get therefore never
// observes a partial blob, and an interrupted write leaves any previous
// value intact.
func (s *Store) Put(_ context.Context, key string, r io.Reader, _ string) error {
	p, err := s.resolve(key)
	if err != nil {
		return err
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("blob/local: mkdir: %w", err)
	}

	f, err := os.CreateTemp(dir, "."+filepath.Base(p)+".*.tmp")
	if err != nil {
		return fmt.Errorf("blob/local: create tmp: %w", err)
	}
	tmp := f.Name()

	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("blob/local: write: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("blob/local: sync: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("blob/local: close tmp: %w", err)
	}
	if err := os.Chmod(tmp, 0o640); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("blob/local: chmod tmp: %w", err)
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("blob/local: rename: %w", err)
	}
	return nil
}

// Get opens the blob for reading. Caller must close the returned ReadCloser.
func (s *Store) Get(_ context.Context, key string) (io.ReadCloser, error) {
	p, err := s.resolve(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, fmt.Errorf("blob/local: open: %w", err)
	}
	return f, nil
}

// Delete removes the blob at key.
func (s *Store) Delete(_ context.Context, key string) error {
	p, err := s.resolve(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil {
		return fmt.Errorf("blob/local: remove: %w", err)
	}
	return nil
}

// List returns all objects whose key starts with prefix.
func (s *Store) List(_ context.Context, prefix string) ([]blob.Object, error) {
	var objects []blob.Object
	root := s.root
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if isTempName(d.Name()) {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		key := filepath.ToSlash(rel)
		if !strings.HasPrefix(key, prefix) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		objects = append(objects, blob.Object{
			Key:  key,
			Size: info.Size(),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("blob/local: walk: %w", err)
	}
	return objects, nil
}

// Exists reports whether a blob exists at key.
func (s *Store) Exists(_ context.Context, key string) (bool, error) {
	p, err := s.resolve(key)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(p)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("blob/local: stat: %w", err)
}
