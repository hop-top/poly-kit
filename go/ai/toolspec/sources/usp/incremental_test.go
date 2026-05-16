package usp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveIncremental_NoCacheFallsToFull(t *testing.T) {
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

	spec, err := src.ResolveIncremental("git")
	require.NoError(t, err)
	require.NotNil(t, spec)
	assert.Equal(t, "git", spec.Name)
	assert.Equal(t, 1, adapter.listCalls) // full scan happened
}

func TestResolveIncremental_MergesNewSessions(t *testing.T) {
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
			"s3": {
				{Name: "Bash", Input: "git add ."},
				{Name: "Bash", Input: "git commit -m 'z'"},
			},
		},
		sinceIDs: []string{"s3"},
	}

	src := NewUSPSource(Config{
		Adapter:  adapter,
		CWD:      "/test",
		MinCount: 2,
	})

	// Populate cache with full scan.
	_, err := src.Resolve("git")
	require.NoError(t, err)

	// Incremental should pick up s3 via ListSessionsSince.
	spec, err := src.ResolveIncremental("git")
	require.NoError(t, err)
	require.NotNil(t, spec)
	assert.Equal(t, 1, adapter.sinceCalls)

	// Transitions should be merged (count should be higher).
	tm := src.CachedTransitions("git")
	require.NotNil(t, tm)
	assert.True(t, tm["git add"]["git commit"] >= 2,
		"expected merged count >= 2, got %d", tm["git add"]["git commit"])
}

func TestResolveIncremental_NoNewSessions(t *testing.T) {
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
		sinceIDs: []string{}, // nothing new
	}

	src := NewUSPSource(Config{
		Adapter:  adapter,
		CWD:      "/test",
		MinCount: 2,
	})

	// Populate cache.
	_, err := src.Resolve("git")
	require.NoError(t, err)

	// Incremental returns cached.
	spec, err := src.ResolveIncremental("git")
	require.NoError(t, err)
	require.NotNil(t, spec)
	assert.Equal(t, 1, adapter.sinceCalls)
}

func TestLoadTransitions(t *testing.T) {
	src := NewUSPSource(Config{
		CWD:      "/test",
		MinCount: 1,
	})

	tm := TransitionMap{
		"git add": {"git commit": 5},
	}

	src.LoadTransitions("git", tm)

	spec, err := src.Resolve("git")
	require.NoError(t, err)
	require.NotNil(t, spec)
	assert.Equal(t, "git", spec.Name)
	assert.NotEmpty(t, spec.Workflows)
}

func TestCachedTransitions_NilWhenNotCached(t *testing.T) {
	src := NewUSPSource(Config{CWD: "/test"})
	assert.Nil(t, src.CachedTransitions("git"))
}
