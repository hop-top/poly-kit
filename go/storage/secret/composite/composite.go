// Package composite routes secret operations across multiple backends,
// each owning a disjoint (or overlapping, first-wins) slice of the
// keyspace.
//
// Composition is code-level only: callers open backends via secret.Open
// as usual, then assemble them with New. The composite itself is not
// registered as a backend because routing predicates are arbitrary Go
// funcs that cannot be expressed in secret.Config.
package composite

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"hop.top/kit/go/storage/secret"
)

// ErrNoWriter is returned by Set/Delete when no writable Member claims
// the key.
var ErrNoWriter = errors.New("composite: no writable owner for key")

// Member is one backend in a composite, with the rule that decides
// whether this member is responsible for a given key.
type Member struct {
	// Name is a human label surfaced in StoredMeta.Source. Required.
	Name string

	// Store is the underlying backend. Required.
	Store secret.MutableStore

	// Owns is the routing predicate. nil means "matches everything"
	// (catch-all). Members whose Owns returns true are considered
	// owners of the key.
	Owns func(key string) bool

	// RO marks the Member read-only. RO members are skipped by
	// Set/Delete and consulted last by Get/Exists/Metadata (they act
	// as read fallbacks).
	RO bool
}

// owns reports whether m claims key. nil predicate = catch-all.
func (m Member) owns(key string) bool {
	if m.Owns == nil {
		return true
	}
	return m.Owns(key)
}

// Store routes operations across Members.
//
// Routing rules:
//   - Reads (Get/Exists/Metadata): first Member whose Owns(key) returns
//     true AND that has the key wins. If no owner has it, fall through
//     to non-owning Members in declaration order. RO has no effect on
//     reads beyond ordering — owners (RO or not) are tried before
//     non-owners.
//   - Writes (Set/Delete): first non-RO Member whose Owns(key) returns
//     true wins. No match → ErrNoWriter. Delete additionally requires
//     that the chosen owner has the key (otherwise ErrNotFound).
//   - List: union across all Members, deduped, sorted.
type Store struct {
	members []Member
}

// New returns a Store composed of the given Members, in priority order.
// Earlier Members are checked first for both reads and writes.
//
// New panics if any Member has an empty Name or nil Store — those are
// programmer errors, not runtime conditions.
func New(members ...Member) *Store {
	for i, m := range members {
		if m.Name == "" {
			panic(fmt.Sprintf("composite: member %d has empty Name", i))
		}
		if m.Store == nil {
			panic(fmt.Sprintf("composite: member %q has nil Store", m.Name))
		}
	}
	return &Store{members: members}
}

// Get returns the secret for key. Searches owners first (in declaration
// order), then non-owners. Returns ErrNotFound if no Member has the key.
func (s *Store) Get(ctx context.Context, key string) (*secret.Secret, error) {
	for _, m := range s.orderedFor(key) {
		got, err := m.Store.Get(ctx, key)
		if err == nil {
			return got, nil
		}
		if !errors.Is(err, secret.ErrNotFound) {
			return nil, fmt.Errorf("composite: member %q: %w", m.Name, err)
		}
	}
	return nil, secret.ErrNotFound
}

// Exists reports whether any Member has the key, following the same
// owner-first ordering as Get.
func (s *Store) Exists(ctx context.Context, key string) (bool, error) {
	for _, m := range s.orderedFor(key) {
		ok, err := m.Store.Exists(ctx, key)
		if err != nil {
			return false, fmt.Errorf("composite: member %q: %w", m.Name, err)
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// List returns the deduped union of keys with the given prefix across
// all Members, sorted lexicographically.
func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	seen := map[string]struct{}{}
	for _, m := range s.members {
		keys, err := m.Store.List(ctx, prefix)
		if err != nil {
			return nil, fmt.Errorf("composite: member %q: %w", m.Name, err)
		}
		for _, k := range keys {
			seen[k] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

// Set writes to the first non-RO owner of key. Returns ErrNoWriter when
// no writable owner matches.
func (s *Store) Set(ctx context.Context, key string, value []byte) error {
	for _, m := range s.members {
		if m.RO || !m.owns(key) {
			continue
		}
		if err := m.Store.Set(ctx, key, value); err != nil {
			return fmt.Errorf("composite: member %q: %w", m.Name, err)
		}
		return nil
	}
	return fmt.Errorf("%w: %q", ErrNoWriter, key)
}

// Delete removes key from the first non-RO owner that has it. Returns
// ErrNoWriter when no writable owner matches, ErrNotFound when writable
// owners exist but none have the key.
func (s *Store) Delete(ctx context.Context, key string) error {
	sawWriter := false
	for _, m := range s.members {
		if m.RO || !m.owns(key) {
			continue
		}
		sawWriter = true
		err := m.Store.Delete(ctx, key)
		if err == nil {
			return nil
		}
		if !errors.Is(err, secret.ErrNotFound) {
			return fmt.Errorf("composite: member %q: %w", m.Name, err)
		}
	}
	if !sawWriter {
		return fmt.Errorf("%w: %q", ErrNoWriter, key)
	}
	return secret.ErrNotFound
}

// Metadata implements secret.MetadataReader by following the same
// read order as Get. Members that don't implement MetadataReader or
// return ErrNotSupported are skipped (not fatal). The first Member
// that returns usable metadata wins; its result has Source rewritten
// to "composite/<member.Name>" so callers can see the routing.
func (s *Store) Metadata(ctx context.Context, key string) (secret.StoredMeta, error) {
	var lastErr error
	for _, m := range s.orderedFor(key) {
		mr, ok := m.Store.(secret.MetadataReader)
		if !ok {
			continue
		}
		meta, err := mr.Metadata(ctx, key)
		if err == nil {
			meta.Source = "composite/" + m.Name
			return meta, nil
		}
		if errors.Is(err, secret.ErrNotSupported) {
			continue
		}
		if errors.Is(err, secret.ErrNotFound) {
			lastErr = err
			continue
		}
		return secret.StoredMeta{}, fmt.Errorf("composite: member %q: %w", m.Name, err)
	}
	if lastErr != nil {
		return secret.StoredMeta{}, lastErr
	}
	return secret.StoredMeta{}, fmt.Errorf("composite: %w", secret.ErrNotSupported)
}

// orderedFor returns members sorted as: owners first (in declaration
// order), then non-owners (in declaration order). Used by all read
// paths.
func (s *Store) orderedFor(key string) []Member {
	out := make([]Member, 0, len(s.members))
	for _, m := range s.members {
		if m.owns(key) {
			out = append(out, m)
		}
	}
	for _, m := range s.members {
		if !m.owns(key) {
			out = append(out, m)
		}
	}
	return out
}

// Compile-time interface checks.
var (
	_ secret.MutableStore   = (*Store)(nil)
	_ secret.MetadataReader = (*Store)(nil)
)
