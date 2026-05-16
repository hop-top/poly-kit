package router

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// intentEmbedder maps specific strings to fixed embeddings.
type intentEmbedder struct {
	embeddings map[string][]float64
	fallback   []float64
}

func (e *intentEmbedder) Embed(
	_ context.Context, text string,
) ([]float64, error) {
	if emb, ok := e.embeddings[text]; ok {
		return emb, nil
	}
	return e.fallback, nil
}

func TestIntentDetector_NoIntents(t *testing.T) {
	det := NewIntentDetector(&stubEmbedder{embedding: []float64{1, 0}})
	intent, err := det.Detect(context.Background(), "test")
	require.NoError(t, err)
	assert.Equal(t, "general", intent)
}

func TestIntentDetector_DetectIntent(t *testing.T) {
	emb := &intentEmbedder{
		embeddings: map[string][]float64{
			"write code":   {1, 0, 0},
			"fix bug":      {0.9, 0.1, 0},
			"write essay":  {0, 1, 0},
			"write poem":   {0, 0.9, 0.1},
			"code request": {0.95, 0.05, 0},
		},
		fallback: []float64{0.5, 0.5, 0},
	}

	det := NewIntentDetector(emb)
	ctx := context.Background()

	require.NoError(t, det.AddExamples(ctx, "coding",
		[]string{"write code", "fix bug"}))
	require.NoError(t, det.AddExamples(ctx, "creative",
		[]string{"write essay", "write poem"}))

	intent, err := det.Detect(ctx, "code request")
	require.NoError(t, err)
	assert.Equal(t, "coding", intent)
}

func TestIntentDetector_Confidence(t *testing.T) {
	emb := &intentEmbedder{
		embeddings: map[string][]float64{
			"code":    {1, 0},
			"poetry":  {0, 1},
			"request": {0.9, 0.1},
		},
		fallback: []float64{0.5, 0.5},
	}

	det := NewIntentDetector(emb)
	ctx := context.Background()

	require.NoError(t, det.AddExamples(ctx, "coding", []string{"code"}))
	require.NoError(t, det.AddExamples(ctx, "creative", []string{"poetry"}))

	scores, err := det.Confidence(ctx, "request")
	require.NoError(t, err)
	assert.Len(t, scores, 2)

	// Scores should sum to 1.
	var total float64
	for _, s := range scores {
		total += s
	}
	assert.InDelta(t, 1.0, total, 0.001)
}

func TestIntentModelSelector_ImplementsMiddleware(t *testing.T) {
	var _ Middleware = (*IntentModelSelector)(nil)
}

func TestIntentModelSelector_GetModelPair(t *testing.T) {
	emb := &intentEmbedder{
		embeddings: map[string][]float64{
			"code":    {1, 0},
			"poetry":  {0, 1},
			"request": {0.95, 0.05},
		},
		fallback: []float64{0.5, 0.5},
	}

	det := NewIntentDetector(emb)
	ctx := context.Background()
	require.NoError(t, det.AddExamples(ctx, "coding", []string{"code"}))
	require.NoError(t, det.AddExamples(ctx, "creative", []string{"poetry"}))

	defaultPair := ModelPair{Strong: "gpt-4", Weak: "gpt-3.5"}
	codingPair := ModelPair{Strong: "claude-3", Weak: "claude-haiku"}

	sel := NewIntentModelSelector(det, defaultPair, []IntentMapping{
		{Intent: "coding", Pair: codingPair},
	})

	// Should match coding intent.
	pair, err := sel.GetModelPair(ctx, "request")
	require.NoError(t, err)
	assert.Equal(t, "claude-3", pair.Strong)
}

func TestIntentModelSelector_DefaultPair(t *testing.T) {
	emb := &intentEmbedder{
		embeddings: map[string][]float64{
			"code":    {1, 0},
			"poetry":  {0, 1},
			"request": {0.1, 0.9},
		},
		fallback: []float64{0.5, 0.5},
	}

	det := NewIntentDetector(emb)
	ctx := context.Background()
	require.NoError(t, det.AddExamples(ctx, "coding", []string{"code"}))
	require.NoError(t, det.AddExamples(ctx, "creative", []string{"poetry"}))

	defaultPair := ModelPair{Strong: "gpt-4", Weak: "gpt-3.5"}
	codingPair := ModelPair{Strong: "claude-3", Weak: "claude-haiku"}

	sel := NewIntentModelSelector(det, defaultPair, []IntentMapping{
		{Intent: "coding", Pair: codingPair},
	})

	// "request" is closer to creative, but no mapping for creative.
	pair, err := sel.GetModelPair(ctx, "request")
	require.NoError(t, err)
	assert.Equal(t, "gpt-4", pair.Strong) // falls back to default
}

func TestCosineSimilarity_Func(t *testing.T) {
	assert.InDelta(t, 1.0, cosineSimilarity(
		[]float64{1, 0}, []float64{1, 0}), 0.001)
	assert.InDelta(t, 0.0, cosineSimilarity(
		[]float64{1, 0}, []float64{0, 1}), 0.001)
	assert.InDelta(t, 0.0, cosineSimilarity(
		[]float64{0, 0}, []float64{1, 0}), 0.001)
}
