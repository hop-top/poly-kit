package usp

import (
	"context"
	"time"

	"hop.top/kit/go/ai/toolspec"
)

// ResolveIncremental resolves a tool spec incrementally: if a cached
// entry exists, only sessions newer than the last scan are fetched
// and their transitions merged into the existing data.
func (s *USPSource) ResolveIncremental(tool string) (*toolspec.ToolSpec, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()

	entry, cached := s.cache[tool]
	if !cached {
		if s.cfg.Adapter == nil {
			return nil, nil
		}
		// No cache — fall through to full scan (unlocked).
		s.mu.Unlock()
		spec, err := s.Resolve(tool)
		s.mu.Lock()
		return spec, err
	}

	if s.cfg.Adapter == nil {
		return entry.spec, nil // return cached, can't scan further
	}

	// Incremental: only scan sessions since last resolve.
	newIDs, err := s.cfg.Adapter.ListSessionsSince(
		ctx, s.cfg.CWD, entry.resolvedAt, s.cfg.maxSessions(),
	)
	if err != nil {
		return nil, err
	}
	if len(newIDs) == 0 {
		return entry.spec, nil // nothing new
	}

	// Collect new sessions.
	newSessions := s.collectSessions(ctx, newIDs)
	if len(newSessions) == 0 {
		return entry.spec, nil
	}

	// Count transitions from new sessions (minCount=1 for raw merge).
	newTM := CountTransitions(newSessions, 1)

	// Merge into existing transitions.
	if entry.transitions == nil {
		entry.transitions = make(TransitionMap)
	}
	MergeTransitions(entry.transitions, newTM)

	// Rebuild spec from merged transitions with minCount filter.
	filtered := filterTransitions(entry.transitions, s.cfg.minCount())
	spec := BuildSpec(tool, filtered)

	entry.spec = spec
	entry.resolvedAt = time.Now()

	return spec, nil
}

// filterTransitions returns a new TransitionMap with only entries
// meeting the minimum count threshold.
func filterTransitions(tm TransitionMap, minCount int) TransitionMap {
	out := make(TransitionMap)
	for src, dsts := range tm {
		for dst, cnt := range dsts {
			if cnt >= minCount {
				if out[src] == nil {
					out[src] = make(map[string]int)
				}
				out[src][dst] = cnt
			}
		}
	}
	return out
}

// CachedTransitions returns the raw transition data for a tool,
// or nil if not cached. Useful for persistence layers.
func (s *USPSource) CachedTransitions(tool string) TransitionMap {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.cache[tool]
	if !ok || entry.transitions == nil {
		return nil
	}
	// Deep copy to avoid leaking internal map.
	out := make(TransitionMap, len(entry.transitions))
	for src, dsts := range entry.transitions {
		cp := make(map[string]int, len(dsts))
		for k, v := range dsts {
			cp[k] = v
		}
		out[src] = cp
	}
	return out
}

// LoadTransitions seeds the cache with pre-existing transition data
// (e.g. loaded from SQLite). This avoids a full rescan on startup.
func (s *USPSource) LoadTransitions(tool string, tm TransitionMap) {
	s.mu.Lock()
	defer s.mu.Unlock()

	filtered := filterTransitions(tm, s.cfg.minCount())
	spec := BuildSpec(tool, filtered)

	s.cache[tool] = &cacheEntry{
		spec:        spec,
		transitions: tm,
		resolvedAt:  time.Now(),
	}
}
