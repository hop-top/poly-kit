package router

import (
	"context"
	"fmt"
	"math"
)

// Embedder produces a float64 embedding vector for a text string.
// Implementations typically call an OpenAI-compatible embeddings API.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float64, error)
}

// BattleRecord represents a single arena battle outcome.
type BattleRecord struct {
	ModelA string
	ModelB string
	Winner string // "model_a", "model_b", or "tie"
}

// SWRankingRouter implements similarity-weighted Elo ranking routing.
//
// It uses pre-computed arena battle embeddings and Elo ratings to estimate
// the strong model's win rate for a given prompt. The score is computed by
// embedding the prompt, computing cosine similarities with arena battles,
// weighting Elo calculations, and deriving a win rate.
type SWRankingRouter struct {
	embedder    Embedder
	strongModel string
	weakModel   string

	// Pre-computed data.
	battleEmbeddings [][]float64 // one embedding per battle
	battles          []BattleRecord
	model2tier       map[string]int
	eloRatings       map[int]float64 // tier -> Elo rating
}

// SWConfig holds configuration for creating a SWRankingRouter.
type SWConfig struct {
	Embedder    Embedder
	StrongModel string
	WeakModel   string
	NumTiers    int

	// Pre-loaded data.
	Battles          []BattleRecord
	BattleEmbeddings [][]float64
}

// NewSWRankingRouter creates a similarity-weighted ranking router.
func NewSWRankingRouter(cfg SWConfig) (*SWRankingRouter, error) {
	if cfg.Embedder == nil {
		return nil, fmt.Errorf("sw: embedder is required")
	}
	if cfg.StrongModel == "" || cfg.WeakModel == "" {
		return nil, fmt.Errorf("sw: strong and weak model names required")
	}
	if len(cfg.Battles) == 0 {
		return nil, fmt.Errorf("sw: battles data is required")
	}
	if len(cfg.Battles) != len(cfg.BattleEmbeddings) {
		return nil, fmt.Errorf(
			"sw: battles (%d) and embeddings (%d) length mismatch",
			len(cfg.Battles), len(cfg.BattleEmbeddings),
		)
	}

	numTiers := cfg.NumTiers
	if numTiers <= 0 {
		numTiers = 10
	}

	// Compute base Elo ratings and model tiers.
	eloRatings := computeEloMLE(cfg.Battles, nil)
	model2tier := computeTiers(eloRatings, numTiers)

	// Replace model names with tiers in battle records.
	tieredBattles := make([]BattleRecord, len(cfg.Battles))
	for i, b := range cfg.Battles {
		tierA := model2tier[b.ModelA]
		tierB := model2tier[b.ModelB]
		tieredBattles[i] = BattleRecord{
			ModelA: fmt.Sprintf("%d", tierA),
			ModelB: fmt.Sprintf("%d", tierB),
			Winner: b.Winner,
		}
	}

	// Convert tier ratings to int-keyed map.
	tierRatings := make(map[int]float64)
	for model, tier := range model2tier {
		if r, ok := eloRatings[model]; ok {
			tierRatings[tier] = r
		}
	}

	return &SWRankingRouter{
		embedder:         cfg.Embedder,
		strongModel:      cfg.StrongModel,
		weakModel:        cfg.WeakModel,
		battleEmbeddings: cfg.BattleEmbeddings,
		battles:          tieredBattles,
		model2tier:       model2tier,
		eloRatings:       tierRatings,
	}, nil
}

// Score embeds the prompt, computes similarity-weighted Elo ratings,
// and returns the strong model's win rate in [0,1].
func (r *SWRankingRouter) Score(
	ctx context.Context, prompt string,
) (float64, error) {
	promptEmb, err := r.embedder.Embed(ctx, prompt)
	if err != nil {
		return 0, fmt.Errorf("sw: embed prompt: %w", err)
	}

	similarities := make([]float64, len(r.battleEmbeddings))
	promptNorm := vecNorm(promptEmb)
	if promptNorm == 0 {
		promptNorm = 1
	}

	for i, emb := range r.battleEmbeddings {
		embNorm := vecNorm(emb)
		if embNorm == 0 {
			similarities[i] = 0
			continue
		}
		similarities[i] = dotProduct(emb, promptEmb) /
			(embNorm * promptNorm)
	}

	weights := computeWeightings(similarities)

	// Re-compute Elo with similarity weights.
	weighted := computeEloMLEBattles(r.battles, weights)

	strongTier := r.model2tier[r.strongModel]
	weakTier := r.model2tier[r.weakModel]

	strongKey := fmt.Sprintf("%d", strongTier)
	weakKey := fmt.Sprintf("%d", weakTier)

	strongScore := weighted[strongKey]
	weakScore := weighted[weakKey]

	// Elo win-rate formula.
	weakWinRate := 1.0 / (1.0 + math.Pow(10, (strongScore-weakScore)/400.0))
	strongWinRate := 1.0 - weakWinRate

	return strongWinRate, nil
}

// computeWeightings converts similarities to exponential weights.
func computeWeightings(similarities []float64) []float64 {
	maxSim := similarities[0]
	for _, s := range similarities[1:] {
		if s > maxSim {
			maxSim = s
		}
	}
	if maxSim <= 0 {
		// All similarities non-positive; use uniform weights.
		weights := make([]float64, len(similarities))
		for i := range weights {
			weights[i] = 10
		}
		return weights
	}

	weights := make([]float64, len(similarities))
	for i, s := range similarities {
		weights[i] = 10 * math.Pow(10, s/maxSim)
	}
	return weights
}

// computeEloMLE computes Elo ratings using Maximum Likelihood Estimation.
// This is a simplified Go port of the Python sklearn-based approach.
func computeEloMLE(
	battles []BattleRecord, weights []float64,
) map[string]float64 {
	return computeEloMLEBattles(battles, weights)
}

// computeEloMLEBattles estimates Elo ratings via gradient descent MLE.
func computeEloMLEBattles(
	battles []BattleRecord, weights []float64,
) map[string]float64 {
	const (
		scale      = 400.0
		base       = 10.0
		initRating = 1000.0
		lr         = 0.01
		iterations = 200
	)

	// Collect unique models.
	modelSet := make(map[string]bool)
	for _, b := range battles {
		modelSet[b.ModelA] = true
		modelSet[b.ModelB] = true
	}
	models := make([]string, 0, len(modelSet))
	for m := range modelSet {
		models = append(models, m)
	}
	modelIdx := make(map[string]int, len(models))
	for i, m := range models {
		modelIdx[m] = i
	}

	p := len(models)
	coefs := make([]float64, p)

	logBase := math.Log(base)

	// Gradient descent to approximate logistic regression.
	for iter := 0; iter < iterations; iter++ {
		grad := make([]float64, p)
		for i, b := range battles {
			idxA := modelIdx[b.ModelA]
			idxB := modelIdx[b.ModelB]

			x := logBase * (coefs[idxA] - coefs[idxB])
			pred := 1.0 / (1.0 + math.Exp(-x))

			var y float64
			switch b.Winner {
			case "model_a":
				y = 1.0
			case "tie", "tie (bothbad)":
				y = 0.5
			}

			w := 1.0
			if weights != nil && i < len(weights) {
				w = weights[i]
			}

			err := (y - pred) * w * logBase
			grad[idxA] += err
			grad[idxB] -= err
		}

		for j := range coefs {
			coefs[j] += lr * grad[j]
		}
	}

	ratings := make(map[string]float64, p)
	for i, m := range models {
		ratings[m] = scale*coefs[i] + initRating
	}
	return ratings
}

// computeTiers partitions models into tiers based on Elo ratings.
// Models are sorted by rating and grouped to minimize within-tier variance.
func computeTiers(
	ratings map[string]float64, numTiers int,
) map[string]int {
	if len(ratings) == 0 || numTiers <= 0 {
		return make(map[string]int)
	}

	// Sort models by rating descending.
	type modelRating struct {
		model  string
		rating float64
	}
	sorted := make([]modelRating, 0, len(ratings))
	for m, r := range ratings {
		sorted = append(sorted, modelRating{m, r})
	}
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].rating > sorted[i].rating {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	n := len(sorted)
	if numTiers >= n {
		result := make(map[string]int, n)
		for i, mr := range sorted {
			result[mr.model] = i
		}
		return result
	}

	// Simple equal-split tier assignment.
	result := make(map[string]int, n)
	perTier := n / numTiers
	extra := n % numTiers
	idx := 0
	for tier := 0; tier < numTiers; tier++ {
		count := perTier
		if tier < extra {
			count++
		}
		for j := 0; j < count && idx < n; j++ {
			result[sorted[idx].model] = tier
			idx++
		}
	}
	return result
}

// dotProduct computes the dot product of two vectors.
func dotProduct(a, b []float64) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var sum float64
	for i := 0; i < n; i++ {
		sum += a[i] * b[i]
	}
	return sum
}

// vecNorm computes the L2 norm of a vector.
func vecNorm(v []float64) float64 {
	var sum float64
	for _, x := range v {
		sum += x * x
	}
	return math.Sqrt(sum)
}
