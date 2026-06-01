package llm_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/aim"
	"hop.top/kit/go/ai/llm"
)

func TestFilterByPool_EmptyPool(t *testing.T) {
	candidates := []aim.Model{
		{Provider: "openai", ID: "gpt-4o"},
		{Provider: "anthropic", ID: "claude-sonnet-4-5"},
	}
	survivors, elim := llm.FilterByPool(candidates, nil)
	assert.Equal(t, candidates, survivors, "empty pool must pass candidates through")
	assert.Empty(t, elim)
}

func TestFilterByPool_KeepsEnabled(t *testing.T) {
	candidates := []aim.Model{
		{Provider: "openai", ID: "gpt-4o"},
		{Provider: "openai", ID: "gpt-3.5-turbo"},
		{Provider: "anthropic", ID: "claude-sonnet-4-5"},
	}
	pool := []llm.PoolEntry{
		{Scheme: "openai", Model: "gpt-4o", Enabled: true, Weight: 1.0},
		{Scheme: "anthropic", Model: "claude-sonnet-4-5", Enabled: true, Weight: 1.0},
	}

	survivors, elim := llm.FilterByPool(candidates, pool)
	require.Len(t, survivors, 2)
	assert.Equal(t, "gpt-4o", survivors[0].ID)
	assert.Equal(t, "claude-sonnet-4-5", survivors[1].ID)

	require.Len(t, elim, 1)
	assert.Equal(t, "gpt-3.5-turbo", elim[0].Model.ID)
}

func TestFilterByPool_DropsDisabled(t *testing.T) {
	candidates := []aim.Model{
		{Provider: "openai", ID: "gpt-4o"},
	}
	pool := []llm.PoolEntry{
		{Scheme: "openai", Model: "gpt-4o", Enabled: false, Weight: 1.0},
	}

	survivors, elim := llm.FilterByPool(candidates, pool)
	assert.Empty(t, survivors)
	require.Len(t, elim, 1)
	assert.Equal(t, "gpt-4o", elim[0].Model.ID)
}

func TestFilterByPool_EliminationDetail(t *testing.T) {
	candidates := []aim.Model{
		{Provider: "openai", ID: "gpt-4o"},
	}
	pool := []llm.PoolEntry{
		{Scheme: "anthropic", Model: "claude-sonnet-4-5", Enabled: true, Weight: 1.0},
	}

	_, elim := llm.FilterByPool(candidates, pool)
	require.Len(t, elim, 1)
	assert.Equal(t, llm.ElimPoolDisabled, elim[0].Stage)
	assert.NotEmpty(t, elim[0].Detail, "Detail must be set for operator-readable logs")
}
