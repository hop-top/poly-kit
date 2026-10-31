// Package local implements blob.Store using the local filesystem.
//
// Keys may contain path separators (e.g. "a/b/c"); intermediate
// directories are created automatically on Put.
//
// The content type given to Put is kept in a sidecar file under the
// reserved top-level directory ".blob-meta" (see metaDir), mirroring the
// key path with a ".json" suffix, and reported as Object.ContentType by
// List. Keys whose first path segment is ".blob-meta" are rejected, so the
// sidecar namespace cannot collide with user keys. Stores written before
// sidecars existed have none: their ContentType is empty.
package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"hop.top/kit/go/storage/blob"
)

// metaDir is the reserved top-level directory holding content-type
// sidecars. It is never a valid key prefix and never listed.
const metaDir = ".blob-meta"

// sidecar is the on-disk metadata record for one key.
type sidecar struct {
	ContentType string `json:"content_type"`
}

var (
	_ blob.Store  = (*Store)(nil)
	_ blob.Stater = (*Store)(nil)
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
	if rel, err := filepath.Rel(s.root, resolved); err == nil && isMetaSegment(rel) {
		return "", fmt.Errorf("blob/local: key %q uses reserved namespace %q", key, metaDir)
	}
	return resolved, nil
}

// isMetaSegment reports whether rel's first segment names the metadata
// directory as a filesystem would resolve it: case-insensitively (macOS,
// Windows) and with trailing dots and spaces dropped (Windows).
func isMetaSegment(rel string) bool {
	first, _, _ := strings.Cut(rel, string(os.PathSeparator))
	return strings.EqualFold(strings.TrimRight(first, ". "), metaDir)
}

// metaPath returns the sidecar path for a resolved content path p.
func (s *Store) metaPath(p string) string {
	rel, _ := filepath.Rel(s.root, p)
	return filepath.Join(s.root, metaDir, rel+".json")
}

// writeAtomic writes data to dst via a synced sibling temp file + rename.
func writeAtomic(dst string, data []byte) error {
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(dst)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o640); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// readContentType returns the stored type for p, or "" when the sidecar
// is missing or unreadable (metadata is advisory; old stores have none).
func (s *Store) readContentType(p string) string {
	b, err := os.ReadFile(s.metaPath(p))
	if err != nil {
		return ""
	}
	var sc sidecar
	if json.Unmarshal(b, &sc) != nil {
		return ""
	}
	return sc.ContentType
}

// Put writes the contents of r to key, creating subdirectories as needed.
//
// The write is atomic: contents land in a sibling temp file that is synced
// and then renamed over the destination. A concurrent Get therefore never
// observes a partial blob, and an interrupted write leaves any previous
// value intact.
//
// contentType is persisted in a sidecar written (atomically) after the
// content lands; an empty type removes any previous sidecar. A crash
// between the two renames leaves the new content with the previous type.
func (s *Store) Put(_ context.Context, key string, r io.Reader, contentType string) error {
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

	mp := s.metaPath(p)
	if contentType == "" {
		if err := os.Remove(mp); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("blob/local: remove meta: %w", err)
		}
		return nil
	}
	b, _ := json.Marshal(sidecar{ContentType: contentType})
	if err := writeAtomic(mp, b); err != nil {
		return fmt.Errorf("blob/local: write meta: %w", err)
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
		return nil, wrapMissing("open", err)
	}
	return f, nil
}

// Delete removes the blob at key. Deleting a missing key succeeds
// (idempotent), matching S3 semantics.
func (s *Store) Delete(_ context.Context, key string) error {
	p, err := s.resolve(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !isMissing(err) {
		return fmt.Errorf("blob/local: remove: %w", err)
	}
	if err := os.Remove(s.metaPath(p)); err != nil && !isMissing(err) {
		return fmt.Errorf("blob/local: remove meta: %w", err)
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
			if p == filepath.Join(root, metaDir) {
				return filepath.SkipDir
			}
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
			Key:         key,
			Size:        info.Size(),
			ContentType: s.readContentType(p),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("blob/local: walk: %w", err)
	}
	return objects, nil
}

// Stat returns the metadata of the blob at key: size from the file and
// content type from the sidecar (empty when none). A missing key matches
// blob.ErrNotFound and fs.ErrNotExist, as Get does. Implements blob.Stater.
func (s *Store) Stat(_ context.Context, key string) (blob.Object, error) {
	p, err := s.resolve(key)
	if err != nil {
		return blob.Object{}, err
	}
	info, err := os.Stat(p)
	if err != nil {
		return blob.Object{}, wrapMissing("stat", err)
	}
	if info.IsDir() {
		return blob.Object{}, wrapMissing("stat", fs.ErrNotExist)
	}
	return blob.Object{Key: key, Size: info.Size(), ContentType: s.readContentType(p)}, nil
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
	if isMissing(err) {
		return false, nil
	}
	return false, fmt.Errorf("blob/local: stat: %w", err)
}

// notFoundError is a missing-key error: it matches blob.ErrNotFound and
// keeps the filesystem error in the chain, so errors.Is(err,
// fs.ErrNotExist) still holds.
type notFoundError struct {
	op  string
	err error
}

func (e *notFoundError) Error() string   { return "blob/local: " + e.op + ": " + e.err.Error() }
func (e *notFoundError) Unwrap() []error { return []error{blob.ErrNotFound, fs.ErrNotExist, e.err} }

// isMissing reports whether err says the path names nothing: the file is
// absent, or a parent component is not a directory (the key cannot exist
// either way).
func isMissing(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

// wrapMissing wraps err from the filesystem op: a missing file becomes a
// notFoundError, anything else a plain wrapped error.
func wrapMissing(op string, err error) error {
	if isMissing(err) {
		return &notFoundError{op: op, err: err}
	}
	return fmt.Errorf("blob/local: %s: %w", op, err)
}
