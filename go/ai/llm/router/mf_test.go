package router

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockScorer implements triton.Scorer for testing.
type mockScorer struct {
	score float64
	err   error
}

func (m *mockScorer) Score(
	_ context.Context, _ []float32,
) (float64, error) {
	return m.score, m.err
}

func TestMFRouter_ImplementsRouter(t *testing.T) {
	var _ Router = (*MFRouter)(nil)
}

func TestNewMFRouter_Validation(t *testing.T) {
	// No embedder.
	_, err := NewMFRouter(MFConfig{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "embedder is required")

	// No scorer.
	_, err = NewMFRouter(MFConfig{
		Embedder: &stubEmbedder{embedding: []float64{1}},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "scorer is required")
}

func TestNewMFRouter_Defaults(t *testing.T) {
	r, err := NewMFRouter(MFConfig{
		Embedder: &stubEmbedder{embedding: []float64{1}},
		Scorer:   &mockScorer{score: 0.5},
	})
	require.NoError(t, err)
	assert.Equal(t, "gpt-4-1106-preview", r.strongModel)
	assert.Equal(t, "mixtral-8x7b-instruct-v0.1", r.weakModel)
}

func TestMFRouter_Score(t *testing.T) {
	emb := &stubEmbedder{embedding: []float64{1.0, 2.0, 3.0}}
	scorer := &mockScorer{score: 0.85}

	r, err := NewMFRouter(MFConfig{
		Embedder: emb,
		Scorer:   scorer,
	})
	require.NoError(t, err)

	score, err := r.Score(context.Background(), "test prompt")
	require.NoError(t, err)
	assert.InDelta(t, 0.85, score, 0.001)
}

func TestMFRouter_Score_EmbedError(t *testing.T) {
	emb := &stubEmbedder{err: fmt.Errorf("embed failed")}
	scorer := &mockScorer{score: 0.5}

	r, err := NewMFRouter(MFConfig{
		Embedder: emb,
		Scorer:   scorer,
	})
	require.NoError(t, err)

	_, err = r.Score(context.Background(), "test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "embed prompt")
}

func TestMFRouter_Score_ScorerError(t *testing.T) {
	emb := &stubEmbedder{embedding: []float64{1.0}}
	scorer := &mockScorer{err: fmt.Errorf("scorer failed")}

	r, err := NewMFRouter(MFConfig{
		Embedder: emb,
		Scorer:   scorer,
	})
	require.NoError(t, err)

	_, err = r.Score(context.Background(), "test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "scorer")
}
