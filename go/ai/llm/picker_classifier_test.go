package llm_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/aim"
	"hop.top/kit/go/ai/llm"
)

// classifierFixture builds a registry where the deterministic chain
// (BudgetCheap, both models pooled) always picks gpt-3.5-turbo, so any test
// observing claude-sonnet-4-5 proves the classifier steered the pick.
func classifierFixture(t *testing.T) (*aim.Registry, []llm.PoolEntry) {
	t.Helper()
	reg := newRegistry(
		t,
		model("openai", "gpt-3.5-turbo", withCost(1, 1)),
		model("anthropic", "claude-sonnet-4-5", withCost(5, 5)),
	)
	pool := []llm.PoolEntry{
		{Scheme: "openai", Model: "gpt-3.5-turbo", Enabled: true, Weight: 1.0},
		{Scheme: "anthropic", Model: "claude-sonnet-4-5", Enabled: true, Weight: 1.0},
	}
	return reg, pool
}

func staticClassifier(label string, confidence float64) llm.PromptClassifier {
	return llm.ClassifierFunc(func(context.Context, string) (llm.Classification, error) {
		return llm.Classification{Label: label, Confidence: confidence}, nil
	})
}

func failingClassifier(err error) llm.PromptClassifier {
	return llm.ClassifierFunc(func(context.Context, string) (llm.Classification, error) {
		return llm.Classification{}, err
	})
}

func premiumTier() *llm.BudgetTier {
	b := llm.BudgetPremium
	return &b
}

func TestPickProviderClassified(t *testing.T) {
	codePool := []llm.PoolEntry{
		{Scheme: "anthropic", Model: "claude-sonnet-4-5", Enabled: true, Weight: 1.0},
	}
	routes := map[string]llm.ClassifiedRoute{
		"code": {Pool: codePool},
		"hard": {Budget: premiumTier()},
	}

	tests := []struct {
		name         string
		routing      llm.ClassifierRouting
		wantProvider string
		wantModel    string
	}{
		{
			name: "label steers pool selection",
			routing: llm.ClassifierRouting{
				Classifier:    staticClassifier("code", 0.9),
				Routes:        routes,
				MinConfidence: 0.6,
			},
			wantProvider: "anthropic",
			wantModel:    "claude-sonnet-4-5",
		},
		{
			name: "label steers budget tier",
			routing: llm.ClassifierRouting{
				Classifier:    staticClassifier("hard", 0.9),
				Routes:        routes,
				MinConfidence: 0.6,
			},
			// BudgetPremium over the base pool ranks priced-up models first.
			wantProvider: "anthropic",
			wantModel:    "claude-sonnet-4-5",
		},
		{
			name: "classifier error falls back to deterministic chain",
			routing: llm.ClassifierRouting{
				Classifier:    failingClassifier(errors.New("scorer unreachable")),
				Routes:        routes,
				MinConfidence: 0.6,
			},
			wantProvider: "openai",
			wantModel:    "gpt-3.5-turbo",
		},
		{
			name: "low confidence falls back to deterministic chain",
			routing: llm.ClassifierRouting{
				Classifier:    staticClassifier("code", 0.3),
				Routes:        routes,
				MinConfidence: 0.6,
			},
			wantProvider: "openai",
			wantModel:    "gpt-3.5-turbo",
		},
		{
			name: "unknown label falls back to deterministic chain",
			routing: llm.ClassifierRouting{
				Classifier:    staticClassifier("poetry", 0.99),
				Routes:        routes,
				MinConfidence: 0.6,
			},
			wantProvider: "openai",
			wantModel:    "gpt-3.5-turbo",
		},
		{
			name: "empty label falls back to deterministic chain",
			routing: llm.ClassifierRouting{
				Classifier:    staticClassifier("", 0.99),
				Routes:        routes,
				MinConfidence: 0.6,
			},
			wantProvider: "openai",
			wantModel:    "gpt-3.5-turbo",
		},
		{
			name: "nil classifier falls back to deterministic chain",
			routing: llm.ClassifierRouting{
				Routes:        routes,
				MinConfidence: 0.6,
			},
			wantProvider: "openai",
			wantModel:    "gpt-3.5-turbo",
		},
		{
			name: "zero-value route inherits base pool and budget",
			routing: llm.ClassifierRouting{
				Classifier: staticClassifier("general", 0.9),
				Routes: map[string]llm.ClassifiedRoute{
					"general": {},
				},
				MinConfidence: 0.6,
			},
			wantProvider: "openai",
			wantModel:    "gpt-3.5-turbo",
		},
		{
			name: "zero min confidence accepts any verdict",
			routing: llm.ClassifierRouting{
				Classifier: staticClassifier("code", 0.01),
				Routes:     routes,
			},
			wantProvider: "anthropic",
			wantModel:    "claude-sonnet-4-5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg, pool := classifierFixture(t)
			got, err := llm.PickProviderClassified(
				context.Background(), reg, tt.routing, "prompt",
				llm.RequestProfile{}, llm.BudgetCheap, pool,
			)
			require.NoError(t, err)
			assert.Equal(t, tt.wantProvider, got.Provider)
			assert.Equal(t, tt.wantModel, got.ID)
		})
	}
}

// A successfully classified route whose pool matches nothing is a
// configuration error, not classifier ambiguity — it must surface
// ErrNoProviderMatches instead of silently retrying the base pool.
func TestPickProviderClassified_ClassifiedNoMatchDoesNotFallBack(t *testing.T) {
	reg, pool := classifierFixture(t)
	routing := llm.ClassifierRouting{
		Classifier: staticClassifier("code", 0.9),
		Routes: map[string]llm.ClassifiedRoute{
			"code": {Pool: []llm.PoolEntry{
				{Scheme: "openai", Model: "nonexistent", Enabled: true, Weight: 1.0},
			}},
		},
	}

	_, err := llm.PickProviderClassified(
		context.Background(), reg, routing, "prompt",
		llm.RequestProfile{}, llm.BudgetCheap, pool,
	)
	require.Error(t, err)
	assert.True(t, errors.Is(err, llm.ErrNoProviderMatches))
}

func TestPickProviderClassified_Trace_Classified(t *testing.T) {
	t.Setenv("LLM_PICKER_TRACE", "1")
	buf := captureSlog(t, slog.LevelInfo)

	reg, pool := classifierFixture(t)
	routing := llm.ClassifierRouting{
		Classifier: staticClassifier("code", 0.9),
		Routes: map[string]llm.ClassifiedRoute{
			"code": {Pool: []llm.PoolEntry{
				{Scheme: "anthropic", Model: "claude-sonnet-4-5", Enabled: true, Weight: 1.0},
			}},
		},
		MinConfidence: 0.6,
	}

	_, err := llm.PickProviderClassified(
		context.Background(), reg, routing, "prompt",
		llm.RequestProfile{}, llm.BudgetCheap, pool,
	)
	require.NoError(t, err)

	logged := buf.String()
	assert.Contains(t, logged, "msg=llm.classify")
	assert.Contains(t, logged, "classifier.outcome=classified")
	assert.Contains(t, logged, "classifier.label=code")
	assert.Contains(t, logged, "classifier.confidence=0.9")
	assert.NotContains(t, logged, "classifier.fallback_reason")
	// The delegated pick still traces.
	assert.Contains(t, logged, "msg=llm.pick")
}

func TestPickProviderClassified_Trace_Fallback(t *testing.T) {
	t.Setenv("LLM_PICKER_TRACE", "1")
	buf := captureSlog(t, slog.LevelInfo)

	reg, pool := classifierFixture(t)
	routing := llm.ClassifierRouting{
		Classifier: failingClassifier(errors.New("scorer unreachable")),
	}

	got, err := llm.PickProviderClassified(
		context.Background(), reg, routing, "prompt",
		llm.RequestProfile{}, llm.BudgetCheap, pool,
	)
	require.NoError(t, err)
	require.Equal(t, "gpt-3.5-turbo", got.ID)

	logged := buf.String()
	assert.Contains(t, logged, "msg=llm.classify")
	assert.Contains(t, logged, "classifier.outcome=fallback")
	assert.Contains(t, logged, "classifier.fallback_reason="+llm.ClassifyFallbackError)
}

func TestPickProviderClassified_Trace_Off(t *testing.T) {
	// Default env (unset) ⇒ no info-level trace line.
	buf := captureSlog(t, slog.LevelInfo)

	reg, pool := classifierFixture(t)
	routing := llm.ClassifierRouting{
		Classifier: staticClassifier("code", 0.9),
		Routes: map[string]llm.ClassifiedRoute{
			"code": {},
		},
	}

	_, err := llm.PickProviderClassified(
		context.Background(), reg, routing, "prompt",
		llm.RequestProfile{}, llm.BudgetCheap, pool,
	)
	require.NoError(t, err)

	if strings.Contains(buf.String(), "msg=llm.classify") {
		t.Fatalf("classifier path emitted trace with LLM_PICKER_TRACE unset\nlog:\n%s", buf.String())
	}
}
