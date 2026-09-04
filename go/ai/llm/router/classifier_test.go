package router

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
)

// scoreStub returns a fixed win rate or error.
type scoreStub struct {
	score float64
	err   error
}

func (s scoreStub) Score(context.Context, string) (float64, error) {
	return s.score, s.err
}

func TestScoreClassifier_Classify(t *testing.T) {
	tests := []struct {
		name           string
		classifier     ScoreClassifier
		wantLabel      string
		wantConfidence float64
	}{
		{
			name:           "high score labels strong",
			classifier:     ScoreClassifier{Router: scoreStub{score: 0.9}},
			wantLabel:      LabelStrong,
			wantConfidence: 0.9,
		},
		{
			name:           "low score labels weak with inverted confidence",
			classifier:     ScoreClassifier{Router: scoreStub{score: 0.2}},
			wantLabel:      LabelWeak,
			wantConfidence: 0.8,
		},
		{
			name:           "score at default threshold labels strong",
			classifier:     ScoreClassifier{Router: scoreStub{score: 0.5}},
			wantLabel:      LabelStrong,
			wantConfidence: 0.5,
		},
		{
			name: "custom threshold shifts the boundary",
			classifier: ScoreClassifier{
				Router:    scoreStub{score: 0.6},
				Threshold: 0.7,
			},
			wantLabel:      LabelWeak,
			wantConfidence: 0.4,
		},
		{
			name: "custom labels replace defaults",
			classifier: ScoreClassifier{
				Router:      scoreStub{score: 0.9},
				StrongLabel: "premium",
				WeakLabel:   "cheap",
			},
			wantLabel:      "premium",
			wantConfidence: 0.9,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.classifier.Classify(context.Background(), "prompt")
			require.NoError(t, err)
			assert.Equal(t, tt.wantLabel, got.Label)
			assert.InDelta(t, tt.wantConfidence, got.Confidence, 1e-9)
		})
	}
}

func TestScoreClassifier_ErrorPropagates(t *testing.T) {
	wantErr := errors.New("triton unreachable")
	c := ScoreClassifier{Router: scoreStub{err: wantErr}}

	_, err := c.Classify(context.Background(), "prompt")
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
}

func TestIntentClassifier_BestIntentWins(t *testing.T) {
	emb := &intentEmbedder{
		embeddings: map[string][]float64{
			"write code":   {1, 0, 0},
			"fix bug":      {0.9, 0.1, 0},
			"write essay":  {0, 1, 0},
			"code request": {0.95, 0.05, 0},
		},
		fallback: []float64{0.5, 0.5, 0},
	}
	det := NewIntentDetector(emb)
	ctx := context.Background()
	require.NoError(t, det.AddExamples(ctx, "coding", []string{"write code", "fix bug"}))
	require.NoError(t, det.AddExamples(ctx, "creative", []string{"write essay"}))

	got, err := IntentClassifier{Detector: det}.Classify(ctx, "code request")
	require.NoError(t, err)
	assert.Equal(t, "coding", got.Label)
	assert.Greater(t, got.Confidence, 0.5, "winning intent holds the confidence majority")
	assert.LessOrEqual(t, got.Confidence, 1.0)
}

func TestIntentClassifier_NoIntentsYieldsNoVerdict(t *testing.T) {
	det := NewIntentDetector(&stubEmbedder{embedding: []float64{1, 0}})

	got, err := IntentClassifier{Detector: det}.Classify(context.Background(), "anything")
	require.NoError(t, err)
	assert.Empty(t, got.Label, "no registered intents means no verdict, so pickers fall back")
	assert.Zero(t, got.Confidence)
}

// errAfterAddEmbedder succeeds while examples are added, then fails.
type errAfterAddEmbedder struct {
	calls int
}

func (e *errAfterAddEmbedder) Embed(context.Context, string) ([]float64, error) {
	e.calls++
	if e.calls > 1 {
		return nil, errors.New("embedder down")
	}
	return []float64{1, 0}, nil
}

func TestIntentClassifier_DetectErrorPropagates(t *testing.T) {
	emb := &errAfterAddEmbedder{}
	det := NewIntentDetector(emb)
	ctx := context.Background()
	require.NoError(t, det.AddExamples(ctx, "coding", []string{"write code"}))

	_, err := IntentClassifier{Detector: det}.Classify(ctx, "prompt")
	require.Error(t, err)
}

func TestClassifiers_SatisfyPromptClassifier(t *testing.T) {
	var _ llm.PromptClassifier = ScoreClassifier{}
	var _ llm.PromptClassifier = IntentClassifier{}
}
