// Adapters that expose this package's routing machinery through the
// llm.PromptClassifier seam, so callers can wire BERT/MF/SW scores or
// embedding-based intents into llm.PickProviderClassified. The dependency
// direction stays router -> llm; llm sees only the interface.

package router

import (
	"context"
	"fmt"
	"sort"

	"hop.top/kit/go/ai/llm"
)

// Default labels emitted by [ScoreClassifier].
const (
	LabelStrong = "strong"
	LabelWeak   = "weak"
)

// DefaultScoreThreshold is the strong/weak boundary used when
// [ScoreClassifier].Threshold is zero.
const DefaultScoreThreshold = 0.5

// ScoreClassifier adapts a [Router] win-rate score to [llm.PromptClassifier].
//
// Scores at or above Threshold classify as StrongLabel with the score as
// confidence; scores below classify as WeakLabel with 1-score. Scores near
// the boundary therefore carry low confidence, so a caller-side
// MinConfidence naturally sends ambiguous prompts to the deterministic
// fallback chain.
type ScoreClassifier struct {
	// Router produces the strong-model win rate in [0,1].
	Router Router
	// Threshold is the strong/weak boundary; zero means
	// [DefaultScoreThreshold].
	Threshold float64
	// StrongLabel and WeakLabel override the emitted labels; empty means
	// [LabelStrong] / [LabelWeak].
	StrongLabel string
	WeakLabel   string
}

// Classify implements [llm.PromptClassifier].
func (c ScoreClassifier) Classify(ctx context.Context, prompt string) (llm.Classification, error) {
	score, err := c.Router.Score(ctx, prompt)
	if err != nil {
		return llm.Classification{}, fmt.Errorf("router: score prompt: %w", err)
	}

	threshold := c.Threshold
	if threshold == 0 {
		threshold = DefaultScoreThreshold
	}
	if score >= threshold {
		label := c.StrongLabel
		if label == "" {
			label = LabelStrong
		}
		return llm.Classification{Label: label, Confidence: score}, nil
	}
	label := c.WeakLabel
	if label == "" {
		label = LabelWeak
	}
	return llm.Classification{Label: label, Confidence: 1 - score}, nil
}

// IntentClassifier adapts an [IntentDetector] to [llm.PromptClassifier]
// using its normalized confidence distribution. The best-scoring intent
// wins; ties break alphabetically so output stays deterministic. With no
// registered intents it returns an empty verdict, which pickers treat as
// "no verdict" and route to the deterministic fallback.
type IntentClassifier struct {
	Detector *IntentDetector
}

// Classify implements [llm.PromptClassifier].
func (c IntentClassifier) Classify(ctx context.Context, prompt string) (llm.Classification, error) {
	scores, err := c.Detector.Confidence(ctx, prompt)
	if err != nil {
		return llm.Classification{}, fmt.Errorf("router: intent confidence: %w", err)
	}
	if len(scores) == 0 {
		return llm.Classification{}, nil
	}

	labels := make([]string, 0, len(scores))
	for label := range scores {
		labels = append(labels, label)
	}
	sort.Strings(labels)

	best := labels[0]
	for _, label := range labels[1:] {
		if scores[label] > scores[best] {
			best = label
		}
	}
	return llm.Classification{Label: best, Confidence: scores[best]}, nil
}

// Compile-time seam conformance.
var (
	_ llm.PromptClassifier = ScoreClassifier{}
	_ llm.PromptClassifier = IntentClassifier{}
)
