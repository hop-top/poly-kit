package usp

import (
	"context"
	"sync"
	"time"

	"hop.top/kit/go/ai/toolspec"
)

// SessionAdapter provides access to session tool calls.
// Decoupled from ash — consumers wire the adapter.
type SessionAdapter interface {
	// ListSessions returns session IDs for the given working directory.
	ListSessions(ctx context.Context, cwd string, limit int) ([]string, error)
	// GetToolCalls returns tool calls for a session.
	GetToolCalls(ctx context.Context, sessionID string) ([]ToolCall, error)
	// ListSessionsSince returns sessions created after the given time.
	ListSessionsSince(ctx context.Context, cwd string, since time.Time, limit int) ([]string, error)
}

// Config for USPSource.
type Config struct {
	Adapter     SessionAdapter
	CWD         string
	MinCount    int // minimum transition count to include (default 2)
	MaxSessions int // max sessions to scan (default 50)
}

func (c Config) minCount() int {
	if c.MinCount <= 0 {
		return 2
	}
	return c.MinCount
}

func (c Config) maxSessions() int {
	if c.MaxSessions <= 0 {
		return 50
	}
	return c.MaxSessions
}

// cacheEntry stores a resolved spec and its transition data.
type cacheEntry struct {
	spec        *toolspec.ToolSpec
	transitions TransitionMap
	resolvedAt  time.Time
}

// USPSource implements toolspec.Source by learning from sessions.
type USPSource struct {
	cfg   Config
	mu    sync.Mutex
	cache map[string]*cacheEntry
}

// NewUSPSource returns a ready-to-use USP source.
func NewUSPSource(cfg Config) *USPSource {
	return &USPSource{
		cfg:   cfg,
		cache: make(map[string]*cacheEntry),
	}
}

// Resolve implements toolspec.Source.
func (s *USPSource) Resolve(tool string) (*toolspec.ToolSpec, error) {
	// Check cache under lock.
	s.mu.Lock()
	if entry, ok := s.cache[tool]; ok {
		s.mu.Unlock()
		return entry.spec, nil
	}
	adapter := s.cfg.Adapter
	s.mu.Unlock()

	if adapter == nil {
		return nil, nil
	}

	ctx := context.Background()

	// I/O outside lock.
	sessionIDs, err := adapter.ListSessions(
		ctx, s.cfg.CWD, s.cfg.maxSessions(),
	)
	if err != nil {
		return nil, err
	}
	if len(sessionIDs) == 0 {
		return nil, nil
	}

	// Collect parsed commands from sessions.
	sessions := s.collectSessions(ctx, sessionIDs)
	if len(sessions) == 0 {
		return nil, nil
	}

	// Count transitions and build spec.
	tm := CountTransitions(sessions, s.cfg.minCount())
	spec := BuildSpec(tool, tm)

	// Detect and merge cross-tool workflows.
	spec = DetectAndMerge(spec, tm, DefaultPatterns())

	// Nil out empty specs (no workflows discovered).
	if len(spec.Workflows) == 0 {
		spec = nil
	}

	// Re-acquire lock to update cache (double-check).
	s.mu.Lock()
	if entry, ok := s.cache[tool]; ok {
		// Another goroutine populated cache while we did I/O.
		s.mu.Unlock()
		return entry.spec, nil
	}
	s.cache[tool] = &cacheEntry{
		spec:        spec,
		transitions: tm,
		resolvedAt:  time.Now(),
	}
	s.mu.Unlock()

	return spec, nil
}

// collectSessions fetches and extracts parsed commands from sessions.
func (s *USPSource) collectSessions(
	ctx context.Context, ids []string,
) [][]ParsedCommand {
	var sessions [][]ParsedCommand
	for _, id := range ids {
		calls, err := s.cfg.Adapter.GetToolCalls(ctx, id)
		if err != nil {
			continue
		}
		cmds := Extract(calls)
		if len(cmds) > 0 {
			sessions = append(sessions, cmds)
		}
	}
	return sessions
}
