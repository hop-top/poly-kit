package router

import (
	"context"
	"fmt"

	"hop.top/kit/go/ai/llm/triton"
)

// MFRouter implements matrix-factorization-based routing.
//
// It embeds the prompt via an Embedder (e.g. OpenAI), then delegates to
// a Triton Scorer to compute the strong model's win rate. The Scorer
// runs the MF model which takes the embedding as input and outputs a
// scalar win probability.
type MFRouter struct {
	embedder    Embedder
	scorer      triton.Scorer
	strongModel string
	weakModel   string
}

// MFConfig holds configuration for creating an MFRouter.
type MFConfig struct {
	Embedder    Embedder
	Scorer      triton.Scorer
	StrongModel string
	WeakModel   string
}

// NewMFRouter creates a matrix factorization router.
func NewMFRouter(cfg MFConfig) (*MFRouter, error) {
	if cfg.Embedder == nil {
		return nil, fmt.Errorf("mf: embedder is required")
	}
	if cfg.Scorer == nil {
		return nil, fmt.Errorf("mf: scorer is required")
	}
	if cfg.StrongModel == "" {
		cfg.StrongModel = "gpt-4-1106-preview"
	}
	if cfg.WeakModel == "" {
		cfg.WeakModel = "mixtral-8x7b-instruct-v0.1"
	}
	return &MFRouter{
		embedder:    cfg.Embedder,
		scorer:      cfg.Scorer,
		strongModel: cfg.StrongModel,
		weakModel:   cfg.WeakModel,
	}, nil
}

// Score embeds the prompt and delegates to the Triton Scorer to get the
// strong model's win rate.
func (r *MFRouter) Score(
	ctx context.Context, prompt string,
) (float64, error) {
	emb, err := r.embedder.Embed(ctx, prompt)
	if err != nil {
		return 0, fmt.Errorf("mf: embed prompt: %w", err)
	}

	// Convert float64 embedding to float32 for Triton.
	input := make([]float32, len(emb))
	for i, v := range emb {
		input[i] = float32(v)
	}

	winRate, err := r.scorer.Score(ctx, input)
	if err != nil {
		return 0, fmt.Errorf("mf: scorer: %w", err)
	}

	return winRate, nil
}
