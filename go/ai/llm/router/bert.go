package router

import (
	"context"
	"fmt"
	"math"

	"hop.top/kit/go/ai/llm/triton"
)

// Tokenizer converts text into token IDs for BERT-style models.
// Implementations should handle padding and truncation.
type Tokenizer interface {
	// Tokenize returns input IDs for the given text.
	Tokenize(text string) ([]int32, error)
}

// BERTRouter implements BERT-based classification routing.
//
// It tokenizes the prompt locally, sends token IDs to a Triton Scorer,
// and applies softmax locally to compute the strong model's win rate.
// The model outputs 3 logits: [strong_wins, tie, weak_wins].
type BERTRouter struct {
	tokenizer Tokenizer
	scorer    triton.Scorer
	numLabels int
}

// BERTConfig holds configuration for creating a BERTRouter.
type BERTConfig struct {
	Tokenizer Tokenizer
	Scorer    triton.Scorer
	NumLabels int // default: 3
}

// NewBERTRouter creates a BERT classification router.
func NewBERTRouter(cfg BERTConfig) (*BERTRouter, error) {
	if cfg.Tokenizer == nil {
		return nil, fmt.Errorf("bert: tokenizer is required")
	}
	if cfg.Scorer == nil {
		return nil, fmt.Errorf("bert: scorer is required")
	}
	if cfg.NumLabels <= 0 {
		cfg.NumLabels = 3
	}
	return &BERTRouter{
		tokenizer: cfg.Tokenizer,
		scorer:    cfg.Scorer,
		numLabels: cfg.NumLabels,
	}, nil
}

// Score tokenizes the prompt, calls the Triton Scorer for logits,
// applies softmax, and returns the strong model's win rate.
//
// The model outputs logits for [strong_wins, tie, weak_wins].
// Win rate = 1 - P(tie) - P(weak_wins) = P(strong_wins).
// Following the Python implementation: binary_prob = softmax[-2:]
// so strong_win_rate = 1 - binary_prob.
func (r *BERTRouter) Score(
	ctx context.Context, prompt string,
) (float64, error) {
	tokenIDs, err := r.tokenizer.Tokenize(prompt)
	if err != nil {
		return 0, fmt.Errorf("bert: tokenize: %w", err)
	}

	// Convert int32 token IDs to float32 for Triton.
	input := make([]float32, len(tokenIDs))
	for i, id := range tokenIDs {
		input[i] = float32(id)
	}

	// Call Triton — expects logits back via the Score method.
	// For BERT, we get a single score from Triton; the full logits
	// would require a custom inference protocol. Here we interpret
	// the scalar output as the win rate directly when Triton
	// handles softmax internally.
	//
	// When Triton returns raw logits, use LogitsScore instead.
	winRate, err := r.scorer.Score(ctx, input)
	if err != nil {
		return 0, fmt.Errorf("bert: scorer: %w", err)
	}

	return winRate, nil
}

// ScoreFromLogits computes the strong model win rate from raw logits.
// This implements the Python BERTRouter softmax logic:
//
//	softmax(logits) -> binary_prob = sum(softmax[-2:])
//	strong_win_rate = 1 - binary_prob
func ScoreFromLogits(logits []float64) float64 {
	if len(logits) == 0 {
		return 0.5
	}

	softmax := Softmax(logits)

	// binary_prob = sum of last 2 classes (tie + weak wins).
	var binaryProb float64
	start := len(softmax) - 2
	if start < 0 {
		start = 0
	}
	for i := start; i < len(softmax); i++ {
		binaryProb += softmax[i]
	}

	return 1.0 - binaryProb
}

// Softmax computes the softmax of a slice of logits.
func Softmax(logits []float64) []float64 {
	if len(logits) == 0 {
		return nil
	}

	// Numerical stability: subtract max.
	maxVal := logits[0]
	for _, v := range logits[1:] {
		if v > maxVal {
			maxVal = v
		}
	}

	expScores := make([]float64, len(logits))
	var sum float64
	for i, v := range logits {
		expScores[i] = math.Exp(v - maxVal)
		sum += expScores[i]
	}

	result := make([]float64, len(logits))
	for i := range expScores {
		result[i] = expScores[i] / sum
	}
	return result
}
