package prompt

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// FragmentStore holds reusable text snippets by alias. A missing alias
// matches ErrNotFound.
type FragmentStore interface {
	Get(ctx context.Context, alias string) ([]byte, error)
}

// FragmentSeparator joins the fragments ResolveFragments returns.
const FragmentSeparator = "\n---\n"

// ResolveFragments fetches each alias in order and joins the contents with
// FragmentSeparator.
func ResolveFragments(ctx context.Context, s FragmentStore, aliases []string) (string, error) {
	parts := make([]string, 0, len(aliases))
	for _, a := range aliases {
		b, err := s.Get(ctx, a)
		if err != nil {
			return "", fmt.Errorf("resolve %q: %w", a, err)
		}
		parts = append(parts, string(b))
	}
	return strings.Join(parts, FragmentSeparator), nil
}

// MemStore is an in-memory FragmentStore, safe for concurrent use.
type MemStore struct {
	mu sync.RWMutex
	m  map[string][]byte
}

// NewMemStore returns an empty MemStore.
func NewMemStore() *MemStore { return &MemStore{m: map[string][]byte{}} }

// Set stores content under alias, replacing any earlier one.
func (s *MemStore) Set(alias string, content []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[alias] = append([]byte(nil), content...)
}

// Get returns a copy of the content stored under alias.
func (s *MemStore) Get(ctx context.Context, alias string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("fragment %q: %w", alias, err)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.m[alias]
	if !ok {
		return nil, fmt.Errorf("%w: fragment %q", ErrNotFound, alias)
	}
	return append([]byte(nil), b...), nil
}

// DirStore reads the fragment `alias` from the file dir/alias.
type DirStore struct{ dir string }

// NewDirStore returns a DirStore over dir.
func NewDirStore(dir string) *DirStore { return &DirStore{dir: dir} }

// Get reads the fragment file. An alias is one path segment.
func (s *DirStore) Get(ctx context.Context, alias string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("fragment %q: %w", alias, err)
	}
	name, err := checkName("fragment", alias)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(s.dir, name)) //nolint:gosec // G304: caller-injected dir, alias checked
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: fragment %q", ErrNotFound, alias)
	}
	if err != nil {
		return nil, fmt.Errorf("read fragment %q: %w", alias, err)
	}
	return b, nil
}
