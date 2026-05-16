package router

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubEmbedder returns a fixed embedding.
type stubEmbedder struct {
	embedding []float64
	err       error
}

func (s *stubEmbedder) Embed(_ context.Context, _ string) ([]float64, error) {
	return s.embedding, s.err
}

func TestSWRankingRouter_ImplementsRouter(t *testing.T) {
	var _ Router = (*SWRankingRouter)(nil)
}

func TestNewSWRankingRouter_Validation(t *testing.T) {
	emb := &stubEmbedder{embedding: []float64{1, 0, 0}}

	// No embedder.
	_, err := NewSWRankingRouter(SWConfig{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "embedder is required")

	// No model names.
	_, err = NewSWRankingRouter(SWConfig{Embedder: emb})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "model names required")

	// No battles.
	_, err = NewSWRankingRouter(SWConfig{
		Embedder: emb, StrongModel: "s", WeakModel: "w",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "battles data is required")

	// Mismatched lengths.
	_, err = NewSWRankingRouter(SWConfig{
		Embedder:    emb,
		StrongModel: "s",
		WeakModel:   "w",
		Battles: []BattleRecord{
			{ModelA: "a", ModelB: "b", Winner: "model_a"},
		},
		BattleEmbeddings: [][]float64{},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "length mismatch")
}

func TestSWRankingRouter_Score(t *testing.T) {
	battles := []BattleRecord{
		{ModelA: "strong", ModelB: "weak", Winner: "model_a"},
		{ModelA: "strong", ModelB: "weak", Winner: "model_a"},
		{ModelA: "weak", ModelB: "strong", Winner: "model_b"},
	}
	embeddings := [][]float64{
		{1, 0, 0},
		{0.9, 0.1, 0},
		{0.8, 0.2, 0},
	}

	emb := &stubEmbedder{embedding: []float64{1, 0, 0}}

	r, err := NewSWRankingRouter(SWConfig{
		Embedder:         emb,
		StrongModel:      "strong",
		WeakModel:        "weak",
		NumTiers:         2,
		Battles:          battles,
		BattleEmbeddings: embeddings,
	})
	require.NoError(t, err)

	score, err := r.Score(context.Background(), "test prompt")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, score, 0.0)
	assert.LessOrEqual(t, score, 1.0)
}

func TestDotProduct(t *testing.T) {
	a := []float64{1, 2, 3}
	b := []float64{4, 5, 6}
	assert.InDelta(t, 32.0, dotProduct(a, b), 0.001)
}

func TestVecNorm(t *testing.T) {
	v := []float64{3, 4}
	assert.InDelta(t, 5.0, vecNorm(v), 0.001)
}

func TestComputeWeightings(t *testing.T) {
	sims := []float64{0.5, 1.0, 0.2}
	w := computeWeightings(sims)
	assert.Len(t, w, 3)
	// Max similarity (1.0) should give weight = 10 * 10^1 = 100
	assert.InDelta(t, 100.0, w[1], 0.001)
}

func TestComputeEloMLE(t *testing.T) {
	battles := []BattleRecord{
		{ModelA: "a", ModelB: "b", Winner: "model_a"},
		{ModelA: "a", ModelB: "b", Winner: "model_a"},
		{ModelA: "b", ModelB: "a", Winner: "model_b"},
	}
	ratings := computeEloMLE(battles, nil)
	// Model A should have higher rating since it wins more.
	assert.Greater(t, ratings["a"], ratings["b"])
}

func TestComputeTiers(t *testing.T) {
	ratings := map[string]float64{
		"a": 1200,
		"b": 1100,
		"c": 1000,
		"d": 900,
	}
	tiers := computeTiers(ratings, 2)
	assert.Len(t, tiers, 4)
	// Higher rated models should be in lower tier numbers.
	assert.Less(t, tiers["a"], tiers["d"])
}

func TestComputeTiers_Empty(t *testing.T) {
	tiers := computeTiers(map[string]float64{}, 5)
	assert.Empty(t, tiers)
}

func TestComputeTiers_MoreTiersThanModels(t *testing.T) {
	ratings := map[string]float64{"a": 1000, "b": 900}
	tiers := computeTiers(ratings, 10)
	assert.Len(t, tiers, 2)
}

func TestSWRankingRouter_EmbedError(t *testing.T) {
	battles := []BattleRecord{
		{ModelA: "a", ModelB: "b", Winner: "model_a"},
	}
	emb := &stubEmbedder{err: assert.AnError}

	r, err := NewSWRankingRouter(SWConfig{
		Embedder:         emb,
		StrongModel:      "a",
		WeakModel:        "b",
		Battles:          battles,
		BattleEmbeddings: [][]float64{{1, 0}},
	})
	require.NoError(t, err)

	_, err = r.Score(context.Background(), "test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "embed prompt")
}

func TestCosineSimilarity(t *testing.T) {
	a := []float64{1, 0}
	b := []float64{0, 1}
	sim := dotProduct(a, b) / (vecNorm(a) * vecNorm(b))
	assert.InDelta(t, 0.0, sim, 0.001)

	c := []float64{1, 1}
	d := []float64{1, 1}
	sim2 := dotProduct(c, d) / (vecNorm(c) * vecNorm(d))
	assert.InDelta(t, 1.0, sim2, 0.001)

	_ = math.Sqrt // ensure math import
}
