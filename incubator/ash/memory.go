package ash

import (
	"context"
	"sort"
	"sync"
	"time"
)

// MemoryStore is a thread-safe, in-memory Store for tests and
// ephemeral sessions.
type MemoryStore struct {
	mu       sync.Mutex
	sessions map[string]*Session
	metas    map[string]SessionMeta
}

// NewMemoryStore returns a ready-to-use MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		sessions: make(map[string]*Session),
		metas:    make(map[string]SessionMeta),
	}
}

func (m *MemoryStore) Create(
	_ context.Context, meta SessionMeta,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.metas[meta.ID] = meta
	m.sessions[meta.ID] = &Session{
		ID:        meta.ID,
		Metadata:  deepCopyMeta(meta.Metadata),
		ParentID:  meta.ParentID,
		CreatedAt: meta.CreatedAt,
		UpdatedAt: meta.UpdatedAt,
	}
	return nil
}

func (m *MemoryStore) Load(
	_ context.Context, id string,
) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[id]
	if !ok {
		return nil, ErrSessionNotFound
	}

	// Return a deep copy to prevent mutation of internal state.
	turns := make([]Turn, len(s.Turns))
	for i, t := range s.Turns {
		turns[i] = deepCopyTurn(t)
	}
	children := make([]string, len(s.Children))
	copy(children, s.Children)

	cp := &Session{
		ID:        s.ID,
		Turns:     turns,
		Metadata:  deepCopyMeta(s.Metadata),
		ParentID:  s.ParentID,
		Children:  children,
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
		ClosedAt:  s.ClosedAt,
	}
	return cp, nil
}

func (m *MemoryStore) AppendTurn(
	_ context.Context, sessionID string, turn Turn,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[sessionID]
	if !ok {
		return ErrSessionNotFound
	}
	if s.ClosedAt != nil {
		return ErrSessionClosed
	}

	s.Turns = append(s.Turns, deepCopyTurn(turn))
	now := time.Now()
	s.UpdatedAt = now

	meta := m.metas[sessionID]
	meta.TurnCount = len(s.Turns)
	meta.UpdatedAt = now
	m.metas[sessionID] = meta

	return nil
}

func (m *MemoryStore) List(
	_ context.Context, f Filter,
) ([]SessionMeta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []SessionMeta
	for _, meta := range m.metas {
		if f.ParentID != "" && meta.ParentID != f.ParentID {
			continue
		}
		if !f.After.IsZero() && !meta.CreatedAt.After(f.After) {
			continue
		}
		if !f.Before.IsZero() && !meta.CreatedAt.Before(f.Before) {
			continue
		}
		out = append(out, meta)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})

	if f.Offset > 0 && f.Offset < len(out) {
		out = out[f.Offset:]
	} else if f.Offset >= len(out) && f.Offset > 0 {
		return nil, nil
	}

	if f.Limit > 0 && f.Limit < len(out) {
		out = out[:f.Limit]
	}

	return out, nil
}

func (m *MemoryStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.sessions[id]; !ok {
		return ErrSessionNotFound
	}
	delete(m.sessions, id)
	delete(m.metas, id)
	return nil
}

// CloseSession marks a session as closed, preventing further appends.
func (m *MemoryStore) CloseSession(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[id]
	if !ok {
		return ErrSessionNotFound
	}

	now := time.Now()
	s.ClosedAt = &now
	return nil
}

// ListTurns returns turns for the given session matching the filter.
func (m *MemoryStore) ListTurns(_ context.Context, sessionID string, f TurnFilter) ([]Turn, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[sessionID]
	if !ok {
		return nil, ErrSessionNotFound
	}

	var result []Turn
	for _, t := range s.Turns {
		if f.Role != "" && t.Role != f.Role {
			continue
		}
		if !f.After.IsZero() && !t.Timestamp.After(f.After) {
			continue
		}
		if !f.Before.IsZero() && !t.Timestamp.Before(f.Before) {
			continue
		}
		result = append(result, deepCopyTurn(t))
	}

	if f.Offset > 0 && f.Offset < len(result) {
		result = result[f.Offset:]
	} else if f.Offset >= len(result) {
		return nil, nil
	}
	if f.Limit > 0 && f.Limit < len(result) {
		result = result[:f.Limit]
	}
	return result, nil
}

func (m *MemoryStore) Close() error { return nil }
