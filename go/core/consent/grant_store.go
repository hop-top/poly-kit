package consent

import (
	"context"
	"sort"
	"sync"
)

// GrantStore persists until and always grants. Once and session
// grants never reach a store; Grants keeps them in memory.
//
// The telemetry capability maps onto the Decision record that Store
// reads and writes, so one file backs both interfaces in FileStore.
type GrantStore interface {
	// Grants returns every persisted grant sorted by capability, then
	// subject. A store that cannot be read whole returns an error of
	// kind malformed_store; callers fail closed on it.
	Grants(ctx context.Context) ([]Grant, error)

	// PutGrant replaces the grant for g's capability and subject.
	PutGrant(ctx context.Context, g Grant) error

	// DeleteGrant removes the grant for the key and reports whether
	// one was there.
	DeleteGrant(ctx context.Context, c Capability, subject string) (bool, error)
}

func sortGrants(gs []Grant) {
	sort.Slice(gs, func(i, j int) bool {
		if gs[i].Capability != gs[j].Capability {
			return gs[i].Capability < gs[j].Capability
		}
		return gs[i].Subject < gs[j].Subject
	})
}

func checkPersistable(g Grant) error {
	if err := g.Validate(); err != nil {
		return err
	}
	if !g.Duration.Persisted() {
		return grantErr(KindUnsupportedScope, "%s grants are not persisted", g.Duration)
	}
	return nil
}

// MemoryGrantStore is a GrantStore held in memory: for tests and for
// hosts with no filesystem. The zero value is ready to use.
type MemoryGrantStore struct {
	mu sync.Mutex
	m  map[grantKey]Grant
}

func (s *MemoryGrantStore) Grants(ctx context.Context) ([]Grant, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Grant, 0, len(s.m))
	for _, g := range s.m {
		out = append(out, g)
	}
	sortGrants(out)
	return out, nil
}

func (s *MemoryGrantStore) PutGrant(ctx context.Context, g Grant) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := checkPersistable(g); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[grantKey]Grant{}
	}
	s.m[g.key()] = g
	return nil
}

func (s *MemoryGrantStore) DeleteGrant(ctx context.Context, c Capability, subject string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := grantKey{c, subject}
	_, ok := s.m[k]
	delete(s.m, k)
	return ok, nil
}
