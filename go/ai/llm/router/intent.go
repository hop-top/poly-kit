package router

import (
	"context"
	"fmt"
	"math"
	"sync"
)

// IntentMapping maps an intent name to a model pair override.
type IntentMapping struct {
	Intent      string
	Description string
	Pair        ModelPair
}

// IntentDetector classifies a prompt into an intent using embedding-based
// cosine similarity against labeled examples.
type IntentDetector struct {
	mu       sync.RWMutex
	embedder Embedder
	intents  map[string]intentData
}

type intentData struct {
	examples   []string
	embeddings [][]float64
}

// NewIntentDetector creates an IntentDetector with the given embedder.
func NewIntentDetector(embedder Embedder) *IntentDetector {
	return &IntentDetector{
		embedder: embedder,
		intents:  make(map[string]intentData),
	}
}

// AddExamples adds labeled example prompts for the given intent.
// Embeddings are computed eagerly.
func (d *IntentDetector) AddExamples(
	ctx context.Context, intent string, examples []string,
) error {
	embeddings := make([][]float64, len(examples))
	for i, ex := range examples {
		emb, err := d.embedder.Embed(ctx, ex)
		if err != nil {
			return fmt.Errorf("intent: embed example %d: %w", i, err)
		}
		embeddings[i] = emb
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	existing := d.intents[intent]
	existing.examples = append(existing.examples, examples...)
	existing.embeddings = append(existing.embeddings, embeddings...)
	d.intents[intent] = existing
	return nil
}

// Detect returns the best-matching intent for the prompt using cosine
// similarity. Returns "general" if no intents are registered or if no
// examples match.
func (d *IntentDetector) Detect(
	ctx context.Context, prompt string,
) (string, error) {
	// Snapshot under lock, release before I/O.
	d.mu.RLock()
	snapshot := make(map[string]intentData, len(d.intents))
	for k, v := range d.intents {
		snapshot[k] = v
	}
	d.mu.RUnlock()

	if len(snapshot) == 0 {
		return "general", nil
	}

	promptEmb, err := d.embedder.Embed(ctx, prompt)
	if err != nil {
		return "general", fmt.Errorf("intent: embed prompt: %w", err)
	}

	bestIntent := "general"
	bestScore := -1.0

	for intent, data := range snapshot {
		if len(data.embeddings) == 0 {
			continue
		}

		var totalSim float64
		for _, emb := range data.embeddings {
			totalSim += cosineSimilarity(promptEmb, emb)
		}
		avgSim := totalSim / float64(len(data.embeddings))

		if avgSim > bestScore {
			bestScore = avgSim
			bestIntent = intent
		}
	}

	return bestIntent, nil
}

// Confidence returns normalized similarity scores for each intent.
func (d *IntentDetector) Confidence(
	ctx context.Context, prompt string,
) (map[string]float64, error) {
	// Snapshot under lock, release before I/O.
	d.mu.RLock()
	snapshot := make(map[string]intentData, len(d.intents))
	for k, v := range d.intents {
		snapshot[k] = v
	}
	d.mu.RUnlock()

	if len(snapshot) == 0 {
		return map[string]float64{}, nil
	}

	promptEmb, err := d.embedder.Embed(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("intent: embed prompt: %w", err)
	}

	scores := make(map[string]float64)
	var total float64

	for intent, data := range snapshot {
		if len(data.embeddings) == 0 {
			continue
		}
		var sum float64
		for _, emb := range data.embeddings {
			sum += cosineSimilarity(promptEmb, emb)
		}
		avg := math.Max(0, sum/float64(len(data.embeddings)))
		scores[intent] = avg
		total += avg
	}

	// Normalize to proper confidence distribution.
	if total > 0 {
		for k := range scores {
			scores[k] /= total
		}
	} else if len(scores) > 0 {
		fallback := 1.0 / float64(len(scores))
		for k := range scores {
			scores[k] = fallback
		}
	}

	return scores, nil
}

// IntentModelSelector is a Middleware that selects model pairs based on
// detected intent. It wraps an IntentDetector and a set of intent-to-model
// mappings.
type IntentModelSelector struct {
	detector    *IntentDetector
	mappings    map[string]ModelPair
	defaultPair ModelPair
}

// NewIntentModelSelector creates an IntentModelSelector.
func NewIntentModelSelector(
	detector *IntentDetector,
	defaultPair ModelPair,
	mappings []IntentMapping,
) *IntentModelSelector {
	m := make(map[string]ModelPair, len(mappings))
	for _, mapping := range mappings {
		m[mapping.Intent] = mapping.Pair
	}
	return &IntentModelSelector{
		detector:    detector,
		mappings:    m,
		defaultPair: defaultPair,
	}
}

// GetModelPair detects the intent and returns the matching model pair.
// If the intent is "general" or unknown, returns the default pair.
func (s *IntentModelSelector) GetModelPair(
	ctx context.Context, prompt string,
) (*ModelPair, error) {
	intent, err := s.detector.Detect(ctx, prompt)
	if err != nil {
		return &s.defaultPair, nil
	}

	if pair, ok := s.mappings[intent]; ok {
		return &pair, nil
	}
	return &s.defaultPair, nil
}

// cosineSimilarity computes the cosine similarity between two vectors.
func cosineSimilarity(a, b []float64) float64 {
	dot := dotProduct(a, b)
	normA := vecNorm(a)
	normB := vecNorm(b)
	if normA == 0 || normB == 0 {
		return 0
	}
	return math.Max(-1, math.Min(1, dot/(normA*normB)))
}
