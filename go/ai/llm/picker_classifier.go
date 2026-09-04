// Classifier stage in front of the provider picker.
//
// The llm package owns only the seam: [PromptClassifier], plus the routing
// table mapping classifier labels to pool/budget overrides. Concrete
// classifiers (BERT scorers, embedding-based intent detectors, ...) live in
// subpackages that already depend on llm and are wired by the caller — llm
// never imports them, preserving the existing dependency direction.
//
// The deterministic chain (PickProviderInPool over the caller's base pool
// and budget) remains authoritative: a nil classifier, a classifier error,
// an empty or unmapped label, or a verdict below MinConfidence all route to
// it unchanged. Only a confident, mapped verdict overrides pool or budget.
//
// # Tracing
//
// PickProviderClassified emits one llm.classify event per call under the
// same LLM_PICKER_TRACE gate as PickProvider (see picker.go). Stable keys:
//
//   - classifier.outcome — "classified" or "fallback".
//   - classifier.label / classifier.confidence — present when the
//     classifier returned a verdict (even one that fell back).
//   - classifier.fallback_reason — one of the ClassifyFallback* constants;
//     present only on fallback.
//
// The delegated pick then emits its own llm.pick event as usual.

package llm

import (
	"context"
	"log/slog"

	"hop.top/aim"
)

// PromptClassifier classifies a prompt into a routing label with a
// confidence in [0,1]. Implementations may call remote scorers; errors are
// never fatal to picking — [PickProviderClassified] treats them as
// "classifier unavailable" and falls back to the deterministic chain.
type PromptClassifier interface {
	Classify(ctx context.Context, prompt string) (Classification, error)
}

// ClassifierFunc adapts a plain function to [PromptClassifier].
type ClassifierFunc func(ctx context.Context, prompt string) (Classification, error)

// Classify implements [PromptClassifier].
func (f ClassifierFunc) Classify(ctx context.Context, prompt string) (Classification, error) {
	return f(ctx, prompt)
}

// Classification is a classifier verdict.
type Classification struct {
	// Label is the routing label (intent name, tier label, ...). Empty
	// means "no verdict" and routes to the deterministic fallback.
	Label string
	// Confidence in [0,1], compared against ClassifierRouting.MinConfidence.
	Confidence float64
}

// ClassifiedRoute is the override applied when its label wins. Zero fields
// inherit the caller's base pool and budget, so a label can steer only the
// pool, only the budget, both, or (as a pure annotation) neither.
type ClassifiedRoute struct {
	// Pool replaces the base pool when non-empty.
	Pool []PoolEntry
	// Budget replaces the base tier when non-nil.
	Budget *BudgetTier
}

// ClassifierRouting binds a classifier to its label routes.
type ClassifierRouting struct {
	// Classifier produces the verdict. Nil disables the stage entirely —
	// every pick takes the deterministic chain.
	Classifier PromptClassifier
	// Routes maps verdict labels to overrides. Labels absent from the map
	// are treated as ambiguous and fall back.
	Routes map[string]ClassifiedRoute
	// MinConfidence rejects verdicts below it as ambiguous. Zero accepts
	// any successful classification.
	MinConfidence float64
}

// Fallback reasons emitted under the classifier.fallback_reason trace key.
const (
	ClassifyFallbackNoClassifier  = "no_classifier"
	ClassifyFallbackError         = "classifier_error"
	ClassifyFallbackLowConfidence = "low_confidence"
	ClassifyFallbackUnknownLabel  = "unknown_label"
)

// PickProviderClassified runs the classifier stage in front of
// [PickProviderInPool]. A confident, mapped verdict swaps in the route's
// pool and/or budget; anything else delegates to the deterministic chain
// with the caller's base pool and budget untouched.
//
// A successful classification whose route matches no provider surfaces
// [ErrNoProviderMatches] like any other pick — that is a route
// misconfiguration, not classifier ambiguity, and silently retrying the
// base pool would hide it from operators.
func PickProviderClassified(
	ctx context.Context,
	reg *aim.Registry,
	routing ClassifierRouting,
	prompt string,
	profile RequestProfile,
	budget BudgetTier,
	pool []PoolEntry,
) (*aim.Model, error) {
	verdict, route, fallbackReason := resolveClassifiedRoute(ctx, routing, prompt)
	if tracingEnabled() {
		emitClassifyTrace(ctx, verdict, fallbackReason)
	}
	if fallbackReason != "" {
		return PickProviderInPool(ctx, reg, profile, budget, pool)
	}

	effPool := pool
	if len(route.Pool) > 0 {
		effPool = route.Pool
	}
	effBudget := budget
	if route.Budget != nil {
		effBudget = *route.Budget
	}
	return PickProviderInPool(ctx, reg, profile, effBudget, effPool)
}

// resolveClassifiedRoute evaluates the classifier and routing table. A
// non-empty third return value names the fallback reason; empty means the
// returned route applies.
func resolveClassifiedRoute(
	ctx context.Context, routing ClassifierRouting, prompt string,
) (Classification, ClassifiedRoute, string) {
	if routing.Classifier == nil {
		return Classification{}, ClassifiedRoute{}, ClassifyFallbackNoClassifier
	}
	verdict, err := routing.Classifier.Classify(ctx, prompt)
	if err != nil {
		return verdict, ClassifiedRoute{}, ClassifyFallbackError
	}
	if verdict.Label == "" {
		return verdict, ClassifiedRoute{}, ClassifyFallbackUnknownLabel
	}
	if verdict.Confidence < routing.MinConfidence {
		return verdict, ClassifiedRoute{}, ClassifyFallbackLowConfidence
	}
	route, ok := routing.Routes[verdict.Label]
	if !ok {
		return verdict, ClassifiedRoute{}, ClassifyFallbackUnknownLabel
	}
	return verdict, route, ""
}

// emitClassifyTrace logs one llm.classify event. An empty fallbackReason
// records the classified outcome; anything else records the fallback.
func emitClassifyTrace(ctx context.Context, verdict Classification, fallbackReason string) {
	attrs := make([]slog.Attr, 0, 4)
	if fallbackReason == "" {
		attrs = append(attrs, slog.String("classifier.outcome", "classified"))
	} else {
		attrs = append(
			attrs,
			slog.String("classifier.outcome", "fallback"),
			slog.String("classifier.fallback_reason", fallbackReason),
		)
	}
	if verdict.Label != "" {
		attrs = append(
			attrs,
			slog.String("classifier.label", verdict.Label),
			slog.Float64("classifier.confidence", verdict.Confidence),
		)
	}
	slog.Default().LogAttrs(ctx, slog.LevelInfo, "llm.classify", attrs...)
}
