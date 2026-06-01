package llm_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
)

func TestPickProviderInPool_PoolNarrowsCandidates(t *testing.T) {
	reg := newRegistry(
		t,
		model("openai", "gpt-4o", withCost(5, 5)),
		model("openai", "gpt-3.5-turbo", withCost(1, 1)),
		model("anthropic", "claude-sonnet-4-5", withCost(3, 3)),
	)
	// Pool only allows the anthropic model — cheapest should NOT be picked
	// even though BudgetCheap normally would prefer it.
	pool := []llm.PoolEntry{
		{Scheme: "anthropic", Model: "claude-sonnet-4-5", Enabled: true, Weight: 1.0},
	}

	got, err := llm.PickProviderInPool(context.Background(), reg, llm.RequestProfile{}, llm.BudgetCheap, pool)
	require.NoError(t, err)
	assert.Equal(t, "claude-sonnet-4-5", got.ID)
	assert.Equal(t, "anthropic", got.Provider)
}

func TestPickProviderInPool_EmptyPoolEqualsPickProvider(t *testing.T) {
	reg := newRegistry(
		t,
		model("openai", "gpt-4o", withCost(5, 5)),
		model("openai", "gpt-3.5-turbo", withCost(1, 1)),
		model("anthropic", "claude-sonnet-4-5", withCost(3, 3)),
	)
	prof := llm.RequestProfile{}

	plain, err := llm.PickProvider(context.Background(), reg, prof, llm.BudgetCheap)
	require.NoError(t, err)

	pooled, err := llm.PickProviderInPool(context.Background(), reg, prof, llm.BudgetCheap, nil)
	require.NoError(t, err)

	assert.Equal(t, plain.Provider, pooled.Provider)
	assert.Equal(t, plain.ID, pooled.ID)
}

func TestPickProviderInPool_DisabledEntryEliminated(t *testing.T) {
	reg := newRegistry(
		t,
		model("openai", "gpt-4o", withCost(5, 5)),
		model("openai", "gpt-3.5-turbo", withCost(1, 1)),
		model("anthropic", "claude-sonnet-4-5", withCost(3, 3)),
	)
	// gpt-3.5-turbo is the cheapest and would win Cheap, but it's disabled.
	pool := []llm.PoolEntry{
		{Scheme: "openai", Model: "gpt-4o", Enabled: true, Weight: 1.0},
		{Scheme: "openai", Model: "gpt-3.5-turbo", Enabled: false, Weight: 1.0},
		{Scheme: "anthropic", Model: "claude-sonnet-4-5", Enabled: true, Weight: 1.0},
	}

	got, err := llm.PickProviderInPool(context.Background(), reg, llm.RequestProfile{}, llm.BudgetCheap, pool)
	require.NoError(t, err)
	assert.NotEqual(t, "gpt-3.5-turbo", got.ID, "disabled entry must not win")
}

func TestPickProviderInPool_NoMatchAfterPoolFilter(t *testing.T) {
	reg := newRegistry(
		t,
		model("openai", "gpt-4o"),
		model("anthropic", "claude-sonnet-4-5"),
	)
	// Pool points at a model not in the registry.
	pool := []llm.PoolEntry{
		{Scheme: "openai", Model: "nonexistent", Enabled: true, Weight: 1.0},
	}

	_, err := llm.PickProviderInPool(context.Background(), reg, llm.RequestProfile{}, llm.BudgetCheap, pool)
	require.Error(t, err)
	assert.True(t, errors.Is(err, llm.ErrNoProviderMatches), "expected ErrNoProviderMatches sentinel")

	var nme *llm.NoMatchError
	require.True(t, errors.As(err, &nme), "expected *NoMatchError via errors.As")
	assert.Equal(t, 2, nme.CandidateCount, "CandidateCount = registry count, before pool filter")
	// Both registry models eliminated as pool_disabled.
	require.Len(t, nme.Eliminated, 2)
	for _, e := range nme.Eliminated {
		assert.Equal(t, llm.ElimPoolDisabled, e.Stage)
	}
}

// PickProviderInPool must honour the same LLM_PICKER_TRACE contract as
// PickProvider; the pool path is the more likely production caller, so a
// silent observability hole there would defeat the purpose of tracing.
func TestPickProviderInPool_Trace_On_Matched(t *testing.T) {
	t.Setenv("LLM_PICKER_TRACE", "1")
	buf := captureSlog(t, slog.LevelInfo)

	reg := newRegistry(
		t,
		model("openai", "gpt-4o", withCost(5, 5)),
		model("anthropic", "claude-sonnet-4-5", withCost(3, 3)),
	)
	pool := []llm.PoolEntry{
		{Scheme: "anthropic", Model: "claude-sonnet-4-5", Enabled: true, Weight: 1.0},
	}

	got, err := llm.PickProviderInPool(context.Background(), reg, llm.RequestProfile{}, llm.BudgetCheap, pool)
	require.NoError(t, err)
	require.Equal(t, "claude-sonnet-4-5", got.ID)

	logged := buf.String()
	assert.Contains(t, logged, "msg=llm.pick")
	assert.Contains(t, logged, "picker.outcome=matched")
	assert.Contains(t, logged, "picker.chosen.provider=anthropic")
	assert.Contains(t, logged, "picker.chosen.model=claude-sonnet-4-5")
}

func TestPickProviderInPool_Trace_On_NoMatch(t *testing.T) {
	t.Setenv("LLM_PICKER_TRACE", "1")
	buf := captureSlog(t, slog.LevelInfo)

	reg := newRegistry(t, model("openai", "gpt-4o"))
	pool := []llm.PoolEntry{
		{Scheme: "openai", Model: "nonexistent", Enabled: true, Weight: 1.0},
	}

	_, err := llm.PickProviderInPool(context.Background(), reg, llm.RequestProfile{}, llm.BudgetCheap, pool)
	require.Error(t, err)

	logged := buf.String()
	assert.Contains(t, logged, "msg=llm.pick")
	assert.Contains(t, logged, "picker.outcome=no_match")
	assert.NotContains(t, logged, "picker.chosen.")
}

func TestPickProviderInPool_Trace_Off(t *testing.T) {
	// Default env (unset) ⇒ no info-level trace line.
	buf := captureSlog(t, slog.LevelInfo)

	reg := newRegistry(t, model("openai", "gpt-4o"))
	pool := []llm.PoolEntry{{Scheme: "openai", Model: "gpt-4o", Enabled: true, Weight: 1.0}}

	_, err := llm.PickProviderInPool(context.Background(), reg, llm.RequestProfile{}, llm.BudgetCheap, pool)
	require.NoError(t, err)

	if strings.Contains(buf.String(), "msg=llm.pick") {
		t.Fatalf("pool path emitted trace with LLM_PICKER_TRACE unset\nlog:\n%s", buf.String())
	}
}
