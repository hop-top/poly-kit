package router

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubTokenizer returns fixed token IDs.
type stubTokenizer struct {
	ids []int32
	err error
}

func (s *stubTokenizer) Tokenize(_ string) ([]int32, error) {
	return s.ids, s.err
}

func TestBERTRouter_ImplementsRouter(t *testing.T) {
	var _ Router = (*BERTRouter)(nil)
}

func TestNewBERTRouter_Validation(t *testing.T) {
	// No tokenizer.
	_, err := NewBERTRouter(BERTConfig{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "tokenizer is required")

	// No scorer.
	_, err = NewBERTRouter(BERTConfig{
		Tokenizer: &stubTokenizer{ids: []int32{1}},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "scorer is required")
}

func TestNewBERTRouter_Defaults(t *testing.T) {
	r, err := NewBERTRouter(BERTConfig{
		Tokenizer: &stubTokenizer{ids: []int32{1}},
		Scorer:    &mockScorer{score: 0.5},
	})
	require.NoError(t, err)
	assert.Equal(t, 3, r.numLabels)
}

func TestBERTRouter_Score(t *testing.T) {
	tok := &stubTokenizer{ids: []int32{101, 2003, 1037, 3231, 102}}
	scorer := &mockScorer{score: 0.7}

	r, err := NewBERTRouter(BERTConfig{
		Tokenizer: tok,
		Scorer:    scorer,
	})
	require.NoError(t, err)

	score, err := r.Score(context.Background(), "this is a test")
	require.NoError(t, err)
	assert.InDelta(t, 0.7, score, 0.001)
}

func TestBERTRouter_Score_TokenizeError(t *testing.T) {
	tok := &stubTokenizer{err: fmt.Errorf("tokenize failed")}
	scorer := &mockScorer{score: 0.5}

	r, err := NewBERTRouter(BERTConfig{
		Tokenizer: tok,
		Scorer:    scorer,
	})
	require.NoError(t, err)

	_, err = r.Score(context.Background(), "test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "tokenize")
}

func TestBERTRouter_Score_ScorerError(t *testing.T) {
	tok := &stubTokenizer{ids: []int32{1, 2, 3}}
	scorer := &mockScorer{err: fmt.Errorf("scorer failed")}

	r, err := NewBERTRouter(BERTConfig{
		Tokenizer: tok,
		Scorer:    scorer,
	})
	require.NoError(t, err)

	_, err = r.Score(context.Background(), "test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "scorer")
}

func TestSoftmax(t *testing.T) {
	logits := []float64{2.0, 1.0, 0.1}
	sm := Softmax(logits)

	require.Len(t, sm, 3)

	// Sum should be 1.
	var sum float64
	for _, v := range sm {
		sum += v
	}
	assert.InDelta(t, 1.0, sum, 0.0001)

	// First element should be largest.
	assert.Greater(t, sm[0], sm[1])
	assert.Greater(t, sm[1], sm[2])
}

func TestSoftmax_Empty(t *testing.T) {
	assert.Nil(t, Softmax(nil))
	assert.Nil(t, Softmax([]float64{}))
}

func TestSoftmax_NumericalStability(t *testing.T) {
	// Large values that would overflow without max subtraction.
	logits := []float64{1000, 1001, 1002}
	sm := Softmax(logits)

	var sum float64
	for _, v := range sm {
		sum += v
		assert.False(t, math.IsNaN(v))
		assert.False(t, math.IsInf(v, 0))
	}
	assert.InDelta(t, 1.0, sum, 0.0001)
}

func TestScoreFromLogits(t *testing.T) {
	// logits: [strong_wins=2.0, tie=1.0, weak_wins=0.1]
	// softmax gives most weight to strong_wins (index 0).
	// binary_prob = softmax[1] + softmax[2] (tie + weak).
	// strong_win_rate = 1 - binary_prob = softmax[0].
	logits := []float64{2.0, 1.0, 0.1}
	winRate := ScoreFromLogits(logits)
	sm := Softmax(logits)

	// Should equal softmax[0].
	assert.InDelta(t, sm[0], winRate, 0.0001)
}

func TestScoreFromLogits_Empty(t *testing.T) {
	assert.InDelta(t, 0.5, ScoreFromLogits(nil), 0.0001)
}

func TestScoreFromLogits_SingleClass(t *testing.T) {
	// With 1 class, binary_prob = softmax[0] = 1.0.
	// strong_win_rate = 1 - 1.0 = 0.
	assert.InDelta(t, 0.0, ScoreFromLogits([]float64{5.0}), 0.0001)
}
