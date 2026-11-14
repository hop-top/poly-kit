package cmdsurface

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

// PKCEStateStore persists OAuth state nonces together with the PKCE
// code_verifier bound to each. The verifier never leaves the server side:
// only its S256 challenge goes into the authorization URL.
//
// Issue stores verifier and binding under a fresh state value for
// providerName. The binding ties the state to the browser that started the
// flow (login CSRF defense): an empty binding means no browser binding.
// Consume returns the verifier and atomically deletes the entry (single use)
// only when binding matches the issued one; a mismatch returns
// ErrBindingMismatch and leaves the entry in place so the genuine browser can
// still finish. Absent, expired, or other-provider states return
// ErrUnknownState.
type PKCEStateStore interface {
	Issue(ctx context.Context, providerName, verifier, binding string, ttl time.Duration) (state string, err error)
	Consume(ctx context.Context, providerName, state, binding string) (verifier string, err error)
}

var (
	// ErrBindingMismatch marks a callback whose browser binding does not match
	// the one recorded at /authorize.
	ErrBindingMismatch = errors.New("cmdsurface: oauth state not bound to this browser")
	// ErrStateStoreFull marks a store that refuses new states at capacity.
	ErrStateStoreFull = errors.New("cmdsurface: oauth state store full")
)

// DefaultPKCEStateCap bounds the entries of an InMemoryPKCEStateStore.
const DefaultPKCEStateCap = 10000

// InMemoryPKCEStateStore is a process-local PKCEStateStore for single-instance
// hosts, CLIs and tests. Safe for concurrent use.
type InMemoryPKCEStateStore struct {
	mu    sync.Mutex
	items map[string]pkceEntry
	cap   int
}

type pkceEntry struct {
	provider string
	verifier string
	binding  string
	expires  time.Time
}

// NewInMemoryPKCEStateStore returns an empty store.
func NewInMemoryPKCEStateStore() *InMemoryPKCEStateStore {
	return &InMemoryPKCEStateStore{items: make(map[string]pkceEntry), cap: DefaultPKCEStateCap}
}

// Issue implements PKCEStateStore. A non-positive ttl never expires.
// Expired entries are pruned first; at capacity it returns ErrStateStoreFull.
func (s *InMemoryPKCEStateStore) Issue(_ context.Context, providerName, verifier, binding string, ttl time.Duration) (string, error) {
	if providerName == "" {
		return "", errors.New("cmdsurface: empty oauth provider name")
	}
	if verifier == "" {
		return "", errors.New("cmdsurface: empty pkce verifier")
	}
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	state := base64.RawURLEncoding.EncodeToString(buf[:])
	now := time.Now()
	var exp time.Time
	if ttl > 0 {
		exp = now.Add(ttl)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, e := range s.items {
		if !e.expires.IsZero() && !now.Before(e.expires) {
			delete(s.items, k)
		}
	}
	if len(s.items) >= s.cap {
		return "", ErrStateStoreFull
	}
	s.items[state] = pkceEntry{provider: providerName, verifier: verifier, binding: binding, expires: exp}
	return state, nil
}

// Consume implements PKCEStateStore.
func (s *InMemoryPKCEStateStore) Consume(_ context.Context, providerName, state, binding string) (string, error) {
	if state == "" {
		return "", ErrUnknownState
	}
	s.mu.Lock()
	e, ok := s.items[state]
	if ok && e.binding != "" &&
		subtle.ConstantTimeCompare([]byte(e.provider), []byte(providerName)) == 1 &&
		(e.expires.IsZero() || time.Now().Before(e.expires)) &&
		subtle.ConstantTimeCompare([]byte(e.binding), []byte(binding)) != 1 {
		s.mu.Unlock()
		return "", ErrBindingMismatch
	}
	delete(s.items, state)
	s.mu.Unlock()
	if !ok || subtle.ConstantTimeCompare([]byte(e.provider), []byte(providerName)) != 1 {
		return "", ErrUnknownState
	}
	if !e.expires.IsZero() && !time.Now().Before(e.expires) {
		return "", ErrUnknownState
	}
	return e.verifier, nil
}

var _ PKCEStateStore = (*InMemoryPKCEStateStore)(nil)
