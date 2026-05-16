package cmdsurface

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrNonceUsed is returned by NonceStore.Consume when the nonce has
// already been consumed or revoked. The signed-URL verifier maps it
// to a 401 nonce_used response.
var ErrNonceUsed = errors.New("cmdsurface: signed-url nonce already used or revoked")

// NonceStore tracks consumed nonces for single-use enforcement of
// signed URLs. Adopters implement against Redis, a DB, or in-memory.
//
// Consume returns nil on first call for a given nonce within TTL,
// and ErrNonceUsed on subsequent calls. Revoke marks a nonce as
// used without an actual visit (admin revocation).
type NonceStore interface {
	// Consume marks nonce as used. exp is the nonce's wall-clock
	// expiry; implementations MAY use it to purge state. Returns
	// ErrNonceUsed when the nonce has already been consumed/revoked.
	Consume(ctx context.Context, nonce string, exp time.Time) error
	// Revoke marks nonce as used without an actual visit. Subsequent
	// Consume calls for the same nonce return ErrNonceUsed. Revoking
	// an unknown nonce is a no-op-or-record at the implementation's
	// discretion; the in-memory store records it so a later issue/
	// visit with the same id is refused.
	Revoke(ctx context.Context, nonce string) error
}

// InMemoryNonceStore is a process-local NonceStore. Concurrent-safe.
// Suitable for single-process adopters and tests; multi-replica
// deployments should wire a shared backend (Redis, DB).
//
// Entries are not actively swept — the map grows with the number of
// distinct nonces consumed. Adopters who need bounded growth should
// implement their own store with a periodic purge.
type InMemoryNonceStore struct {
	mu   sync.Mutex
	seen map[string]time.Time // nonce → expiry (zero = revoked-only)
}

// NewInMemoryNonceStore returns a ready-to-use process-local store.
func NewInMemoryNonceStore() *InMemoryNonceStore {
	return &InMemoryNonceStore{seen: make(map[string]time.Time)}
}

// Consume implements NonceStore.
func (s *InMemoryNonceStore) Consume(_ context.Context, nonce string, exp time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seen[nonce]; ok {
		return ErrNonceUsed
	}
	s.seen[nonce] = exp
	return nil
}

// Revoke implements NonceStore. A revoked nonce is recorded with a
// zero expiry so a subsequent Consume sees it as already used.
func (s *InMemoryNonceStore) Revoke(_ context.Context, nonce string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seen[nonce]; ok {
		// Already consumed or revoked — leave the prior expiry in
		// place; either way Consume will reject.
		return nil
	}
	s.seen[nonce] = time.Time{}
	return nil
}
