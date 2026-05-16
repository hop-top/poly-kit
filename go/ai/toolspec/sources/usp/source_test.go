package usp

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/ai/toolspec"
)

// mockAdapter implements SessionAdapter for testing.
type mockAdapter struct {
	sessions   map[string][]ToolCall
	sessionIDs []string
	sinceIDs   []string
	listCalls  int
	sinceCalls int
}

func (m *mockAdapter) ListSessions(_ context.Context, _ string, limit int) ([]string, error) {
	m.listCalls++
	if limit > len(m.sessionIDs) {
		return m.sessionIDs, nil
	}
	return m.sessionIDs[:limit], nil
}

func (m *mockAdapter) GetToolCalls(_ context.Context, id string) ([]ToolCall, error) {
	return m.sessions[id], nil
}

func (m *mockAdapter) ListSessionsSince(
	_ context.Context, _ string, _ time.Time, limit int,
) ([]string, error) {
	m.sinceCalls++
	if limit > len(m.sinceIDs) {
		return m.sinceIDs, nil
	}
	return m.sinceIDs[:limit], nil
}

func TestUSPSource_Resolve(t *testing.T) {
	adapter := &mockAdapter{
		sessionIDs: []string{"s1", "s2"},
		sessions: map[string][]ToolCall{
			"s1": {
				{Name: "Bash", Input: "git add ."},
				{Name: "Bash", Input: "git commit -m 'init'"},
			},
			"s2": {
				{Name: "Bash", Input: "git add ."},
				{Name: "Bash", Input: "git commit -m 'fix'"},
			},
		},
	}

	src := NewUSPSource(Config{
		Adapter:  adapter,
		CWD:      "/test",
		MinCount: 2,
	})

	spec, err := src.Resolve("git")
	require.NoError(t, err)
	require.NotNil(t, spec)
	assert.Equal(t, "git", spec.Name)
	assert.NotEmpty(t, spec.Workflows)
}

func TestUSPSource_Resolve_EmptySessions(t *testing.T) {
	adapter := &mockAdapter{
		sessionIDs: []string{},
		sessions:   map[string][]ToolCall{},
	}

	src := NewUSPSource(Config{
		Adapter: adapter,
		CWD:     "/test",
	})

	spec, err := src.Resolve("git")
	require.NoError(t, err)
	assert.Nil(t, spec)
}

func TestUSPSource_Resolve_NilAdapter(t *testing.T) {
	src := NewUSPSource(Config{CWD: "/test"})
	spec, err := src.Resolve("git")
	require.NoError(t, err)
	assert.Nil(t, spec)
}

func TestUSPSource_Resolve_CacheHit(t *testing.T) {
	adapter := &mockAdapter{
		sessionIDs: []string{"s1", "s2"},
		sessions: map[string][]ToolCall{
			"s1": {
				{Name: "Bash", Input: "git add ."},
				{Name: "Bash", Input: "git commit -m 'x'"},
			},
			"s2": {
				{Name: "Bash", Input: "git add ."},
				{Name: "Bash", Input: "git commit -m 'y'"},
			},
		},
	}

	src := NewUSPSource(Config{
		Adapter:  adapter,
		CWD:      "/test",
		MinCount: 2,
	})

	// First call populates cache.
	_, err := src.Resolve("git")
	require.NoError(t, err)
	assert.Equal(t, 1, adapter.listCalls)

	// Second call hits cache — no additional ListSessions call.
	_, err = src.Resolve("git")
	require.NoError(t, err)
	assert.Equal(t, 1, adapter.listCalls)
}

func TestUSPSource_Resolve_BelowMinCount(t *testing.T) {
	adapter := &mockAdapter{
		sessionIDs: []string{"s1"},
		sessions: map[string][]ToolCall{
			"s1": {
				{Name: "Bash", Input: "git add ."},
				{Name: "Bash", Input: "git commit -m 'x'"},
			},
		},
	}

	src := NewUSPSource(Config{
		Adapter:  adapter,
		CWD:      "/test",
		MinCount: 5, // threshold too high
	})

	spec, err := src.Resolve("git")
	require.NoError(t, err)
	assert.Nil(t, spec)
}

// Verify USPSource satisfies toolspec.Source interface at compile time.
var _ toolspec.Source = (*USPSource)(nil)

func TestUSPSource_ImplementsSource(t *testing.T) {
	// USPSource.Resolve has the right signature for toolspec.Source.
	src := NewUSPSource(Config{})
	_ = src.Resolve
}
