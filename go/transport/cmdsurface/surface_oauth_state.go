package cmdsurface

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

// ErrUnknownState is returned by StateStore.Consume when the supplied
// state value is not present, has expired, or was issued for a
// different provider. The OAuth callback handler maps this to an
// invalid_state error response.
var ErrUnknownState = errors.New("cmdsurface: unknown or expired oauth state")

// StateStore persists OAuth state nonces between the authorization
// redirect and the callback. Adopters implement against Redis,
// in-memory, or a session store.
//
// Issue creates and persists a state value; the returned value is what
// the adopter places in the OAuth authorization URL's `state` query
// parameter. The TTL is provided so adopters can expire state
// independent of the surface.
//
// Consume looks up and atomically deletes a state value (single-use).
// Returns ErrUnknownState if not found, expired, or issued for a
// different provider.
type StateStore interface {
	Issue(ctx context.Context, providerName string, ttl time.Duration) (state string, err error)
	Consume(ctx context.Context, providerName, state string) error
}

// InMemoryStateStore is a process-local StateStore suitable for
// single-instance adopters or tests. It uses lazy expiry: entries are
// checked for expiration only at Consume time. Safe for concurrent
// use.
type InMemoryStateStore struct {
	mu    sync.Mutex
	items map[string]stateEntry
	// now is overridable for tests; production callers leave it nil
	// (defaults to time.Now).
	now func() time.Time
}

type stateEntry struct {
	provider string
	expires  time.Time
}

// NewInMemoryStateStore returns an empty InMemoryStateStore ready for
// use.
func NewInMemoryStateStore() *InMemoryStateStore {
	return &InMemoryStateStore{items: make(map[string]stateEntry)}
}

// Issue generates 32 bytes of cryptographic randomness, URL-safe
// base64-encodes them (43 chars, no padding), records the value with
// the supplied provider and TTL, and returns the encoded state.
//
// A non-positive ttl is treated as "never expires" (the entry has a
// zero-value expires time).
func (s *InMemoryStateStore) Issue(_ context.Context, providerName string, ttl time.Duration) (string, error) {
	if providerName == "" {
		return "", errors.New("cmdsurface: empty oauth provider name")
	}
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	state := base64.RawURLEncoding.EncodeToString(buf[:])

	var expires time.Time
	if ttl > 0 {
		expires = s.timeNow().Add(ttl)
	}

	s.mu.Lock()
	s.items[state] = stateEntry{provider: providerName, expires: expires}
	s.mu.Unlock()
	return state, nil
}

// Consume looks up state, verifies it was issued for providerName and
// has not expired, deletes the entry, and returns nil on success.
// Returns ErrUnknownState in every failure case (not present, expired,
// or wrong provider) so callers cannot distinguish causes.
func (s *InMemoryStateStore) Consume(_ context.Context, providerName, state string) error {
	if state == "" {
		return ErrUnknownState
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.items[state]
	if !ok {
		return ErrUnknownState
	}
	// Always delete on lookup — even on mismatch — to avoid leaking
	// the existence of a state value for the wrong provider via
	// timing differences across repeated Consume attempts. The entry
	// was single-use by contract; the caller burned it.
	delete(s.items, state)
	if entry.provider != providerName {
		return ErrUnknownState
	}
	if !entry.expires.IsZero() && !s.timeNow().Before(entry.expires) {
		return ErrUnknownState
	}
	return nil
}

// timeNow returns the configured clock or time.Now when unset.
func (s *InMemoryStateStore) timeNow() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}
