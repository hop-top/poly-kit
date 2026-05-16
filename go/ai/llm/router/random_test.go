package router

import (
	"context"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRandomRouter_Score(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	r := NewRandomRouter(rng)

	ctx := context.Background()
	score, err := r.Score(ctx, "anything")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, score, 0.0)
	assert.LessOrEqual(t, score, 1.0)
}

func TestRandomRouter_ScoreDeterministic(t *testing.T) {
	r1 := NewRandomRouter(rand.New(rand.NewSource(123)))
	r2 := NewRandomRouter(rand.New(rand.NewSource(123)))

	ctx := context.Background()
	s1, _ := r1.Score(ctx, "test")
	s2, _ := r2.Score(ctx, "test")
	assert.Equal(t, s1, s2)
}

func TestRandomRouter_ScoreNilRng(t *testing.T) {
	r := NewRandomRouter(nil)
	score, err := r.Score(context.Background(), "test")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, score, 0.0)
	assert.LessOrEqual(t, score, 1.0)
}

func TestRandomRouter_ImplementsRouter(t *testing.T) {
	var _ Router = (*RandomRouter)(nil)
}
