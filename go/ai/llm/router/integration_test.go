//go:build integration

// Integration tests for the router package.
//
// These tests exercise the full request flow without external dependencies.
// Build tag: go test -tags=integration ./llm/router/
package router_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/ai/llm/router"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// mockProvider is an in-memory llm.Provider + llm.Completer for tests.
type mockProvider struct {
	model    string
	response string
	err      error
	called   int
}

func (m *mockProvider) Close() error { return nil }

func (m *mockProvider) Complete(
	_ context.Context, req llm.Request,
) (llm.Response, error) {
	m.called++
	if m.err != nil {
		return llm.Response{}, m.err
	}
	return llm.Response{
		Content: fmt.Sprintf("[%s] %s", req.Model, m.response),
		Role:    "assistant",
	}, nil
}

// Ensure compile-time interface compliance.
var (
	_ llm.Provider  = (*mockProvider)(nil)
	_ llm.Completer = (*mockProvider)(nil)
)

// fixedRouter always returns a fixed score.
type fixedRouter struct{ score float64 }

func (r *fixedRouter) Score(_ context.Context, _ string) (float64, error) {
	return r.score, nil
}

// failingRouter always returns an error.
type failingRouter struct{ err error }

func (r *failingRouter) Score(_ context.Context, _ string) (float64, error) {
	return 0, r.err
}

// intentMiddleware overrides the model pair when the prompt contains a
// trigger keyword.
type intentMiddleware struct {
	trigger string
	pair    router.ModelPair
}

func (m *intentMiddleware) GetModelPair(
	_ context.Context, prompt string,
) (*router.ModelPair, error) {
	if strings.Contains(prompt, m.trigger) {
		return &m.pair, nil
	}
	return nil, nil
}

// buildController creates a controller with the given routers and a mock
// provider for testing.
func buildController(
	t *testing.T,
	routers map[string]router.Router,
	pair router.ModelPair,
	provider llm.Provider,
	opts ...router.ControllerOption,
) *router.Controller {
	t.Helper()
	reg := router.NewRegistry()
	for name, r := range routers {
		require.NoError(t, reg.Register(name, r))
	}
	allOpts := append([]router.ControllerOption{
		router.WithProvider(provider),
	}, opts...)
	return router.NewController(reg, pair, allOpts...)
}

// postCompletion sends a chat completion request to the test server.
func postCompletion(
	t *testing.T, srv *httptest.Server, model, prompt string,
) (*http.Response, map[string]any) {
	t.Helper()
	body := fmt.Sprintf(
		`{"model":"%s","messages":[{"role":"user","content":"%s"}]}`,
		model, prompt,
	)
	resp, err := http.Post(
		srv.URL+"/v1/chat/completions",
		"application/json",
		strings.NewReader(body),
	)
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	resp.Body.Close()
	return resp, result
}

// ---------------------------------------------------------------------------
// Test: full HTTP request flow
// ---------------------------------------------------------------------------

func TestFullRequestFlow(t *testing.T) {
	provider := &mockProvider{response: "hello world"}
	ctrl := buildController(t,
		map[string]router.Router{
			"fixed": &fixedRouter{score: 0.8},
		},
		router.ModelPair{Strong: "strong-model", Weak: "weak-model"},
		provider,
	)

	handler := router.Handler(ctrl)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// Score 0.8 >= threshold 0.5 -> strong model.
	resp, result := postCompletion(t, srv, "router-fixed-0.5", "test prompt")
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	choices, ok := result["choices"].([]any)
	require.True(t, ok)
	require.Len(t, choices, 1)

	choice := choices[0].(map[string]any)
	msg := choice["message"].(map[string]any)
	assert.Contains(t, msg["content"], "strong-model")
	assert.Contains(t, msg["content"], "hello world")
}

// ---------------------------------------------------------------------------
// Test: RandomRouter (no external deps)
// ---------------------------------------------------------------------------

func TestRandomRouterIntegration(t *testing.T) {
	// Use a seeded random for deterministic routing.
	rng := rand.New(rand.NewSource(42))
	provider := &mockProvider{response: "ok"}
	ctrl := buildController(t,
		map[string]router.Router{
			"random": router.NewRandomRouter(rng),
		},
		router.ModelPair{Strong: "strong", Weak: "weak"},
		provider,
	)

	handler := router.Handler(ctrl)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	strongCount := 0
	weakCount := 0
	n := 100

	for i := 0; i < n; i++ {
		_, result := postCompletion(t, srv,
			"router-random-0.5",
			fmt.Sprintf("prompt %d", i),
		)
		choices := result["choices"].([]any)
		choice := choices[0].(map[string]any)
		msg := choice["message"].(map[string]any)
		content := msg["content"].(string)
		if strings.Contains(content, "[strong]") {
			strongCount++
		} else {
			weakCount++
		}
	}

	// With random routing at 0.5 threshold, expect roughly even split.
	assert.Greater(t, strongCount, 10,
		"expected some strong model calls")
	assert.Greater(t, weakCount, 10,
		"expected some weak model calls")
	assert.Equal(t, n, provider.called,
		"all requests should reach the provider")
}

// ---------------------------------------------------------------------------
// Test: mock Triton scorer (simulates MF/BERT)
// ---------------------------------------------------------------------------

func TestMockTritonScorer(t *testing.T) {
	// Simulate a Triton-backed scorer that returns high scores for
	// complex prompts and low scores for simple ones.
	scorer := &fixedRouter{score: 0.9}
	provider := &mockProvider{response: "result"}
	ctrl := buildController(t,
		map[string]router.Router{"mf": scorer},
		router.ModelPair{Strong: "gpt-4o", Weak: "gpt-4o-mini"},
		provider,
	)

	handler := router.Handler(ctrl)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// High score (0.9) >= threshold (0.7) -> strong model.
	_, result := postCompletion(t, srv, "router-mf-0.7", "complex math")
	choices := result["choices"].([]any)
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	assert.Contains(t, msg["content"], "gpt-4o")

	// Switch to a low scorer.
	lowScorer := &fixedRouter{score: 0.3}
	ctrl2 := buildController(t,
		map[string]router.Router{"mf": lowScorer},
		router.ModelPair{Strong: "gpt-4o", Weak: "gpt-4o-mini"},
		provider,
	)
	handler2 := router.Handler(ctrl2)
	srv2 := httptest.NewServer(handler2)
	defer srv2.Close()

	// Low score (0.3) < threshold (0.7) -> weak model.
	_, result2 := postCompletion(t, srv2, "router-mf-0.7", "hi")
	choices2 := result2["choices"].([]any)
	msg2 := choices2[0].(map[string]any)["message"].(map[string]any)
	assert.Contains(t, msg2["content"], "gpt-4o-mini")
}

// ---------------------------------------------------------------------------
// Test: intent middleware routing
// ---------------------------------------------------------------------------

func TestIntentMiddlewareRouting(t *testing.T) {
	provider := &mockProvider{response: "ok"}
	mw := &intentMiddleware{
		trigger: "code review",
		pair: router.ModelPair{
			Strong: "claude-opus",
			Weak:   "claude-haiku",
		},
	}

	ctrl := buildController(t,
		map[string]router.Router{
			"fixed": &fixedRouter{score: 0.8},
		},
		router.ModelPair{Strong: "gpt-4o", Weak: "gpt-4o-mini"},
		provider,
		router.WithMiddleware(mw),
	)

	handler := router.Handler(ctrl)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// Without trigger: uses default pair. Score 0.8 >= 0.5 -> strong.
	_, result := postCompletion(t, srv, "router-fixed-0.5", "hello world")
	choices := result["choices"].([]any)
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	assert.Contains(t, msg["content"], "gpt-4o")

	// With trigger: middleware overrides pair. Score 0.8 >= 0.5 -> strong.
	_, result2 := postCompletion(t, srv,
		"router-fixed-0.5", "please do a code review")
	choices2 := result2["choices"].([]any)
	msg2 := choices2[0].(map[string]any)["message"].(map[string]any)
	assert.Contains(t, msg2["content"], "claude-opus")
}

// ---------------------------------------------------------------------------
// Test: threshold calibration
// ---------------------------------------------------------------------------

func TestThresholdCalibration(t *testing.T) {
	provider := &mockProvider{response: "ok"}
	score := 0.6

	ctrl := buildController(t,
		map[string]router.Router{
			"cal": &fixedRouter{score: score},
		},
		router.ModelPair{Strong: "strong", Weak: "weak"},
		provider,
	)

	handler := router.Handler(ctrl)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// Threshold below score -> strong.
	_, r1 := postCompletion(t, srv, "router-cal-0.5", "test")
	c1 := r1["choices"].([]any)
	m1 := c1[0].(map[string]any)["message"].(map[string]any)
	assert.Contains(t, m1["content"], "[strong]")

	// Threshold at score -> strong (>=).
	_, r2 := postCompletion(t, srv, "router-cal-0.6", "test")
	c2 := r2["choices"].([]any)
	m2 := c2[0].(map[string]any)["message"].(map[string]any)
	assert.Contains(t, m2["content"], "[strong]")

	// Threshold above score -> weak.
	_, r3 := postCompletion(t, srv, "router-cal-0.7", "test")
	c3 := r3["choices"].([]any)
	m3 := c3[0].(map[string]any)["message"].(map[string]any)
	assert.Contains(t, m3["content"], "[weak]")
}

// ---------------------------------------------------------------------------
// Test: error cases
// ---------------------------------------------------------------------------

func TestInvalidModelString(t *testing.T) {
	provider := &mockProvider{response: "ok"}
	ctrl := buildController(t,
		map[string]router.Router{"test": &fixedRouter{score: 0.5}},
		router.ModelPair{Strong: "s", Weak: "w"},
		provider,
	)

	handler := router.Handler(ctrl)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	tests := []struct {
		name  string
		model string
	}{
		{"no_prefix", "mf-0.5"},
		{"missing_threshold", "router-mf"},
		{"empty_model", ""},
		{"invalid_threshold", "router-mf-abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, result := postCompletion(t, srv, tt.model, "test")
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
			// server.go returns {object: "error", message: "..."}
			msg, ok := result["message"].(string)
			require.True(t, ok, "expected message in error response")
			assert.NotEmpty(t, msg)
		})
	}
}

func TestUnknownRouter(t *testing.T) {
	provider := &mockProvider{response: "ok"}
	ctrl := buildController(t,
		map[string]router.Router{"known": &fixedRouter{score: 0.5}},
		router.ModelPair{Strong: "s", Weak: "w"},
		provider,
	)

	handler := router.Handler(ctrl)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, result := postCompletion(t, srv, "router-unknown-0.5", "test")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	msg := result["message"].(string)
	assert.Contains(t, msg, "unknown router")
}

func TestThresholdOutOfRange(t *testing.T) {
	provider := &mockProvider{response: "ok"}
	ctrl := buildController(t,
		map[string]router.Router{"test": &fixedRouter{score: 0.5}},
		router.ModelPair{Strong: "s", Weak: "w"},
		provider,
	)

	handler := router.Handler(ctrl)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// Threshold > 1 should fail validation.
	resp, result := postCompletion(t, srv, "router-test-1.5", "test")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	msg := result["message"].(string)
	assert.Contains(t, msg, "out of")
}

func TestScoringError(t *testing.T) {
	provider := &mockProvider{response: "ok"}
	ctrl := buildController(t,
		map[string]router.Router{
			"broken": &failingRouter{err: fmt.Errorf("triton connection failed")},
		},
		router.ModelPair{Strong: "s", Weak: "w"},
		provider,
	)

	handler := router.Handler(ctrl)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, result := postCompletion(t, srv, "router-broken-0.5", "test")
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	msg := result["message"].(string)
	assert.Contains(t, msg, "triton connection failed")
}

// ---------------------------------------------------------------------------
// Test: health endpoint
// ---------------------------------------------------------------------------

func TestHealthEndpoint(t *testing.T) {
	provider := &mockProvider{response: "ok"}
	ctrl := buildController(t,
		map[string]router.Router{"random": router.NewRandomRouter(nil)},
		router.ModelPair{Strong: "s", Weak: "w"},
		provider,
	)

	handler := router.Handler(ctrl)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "online", body["status"])
}

// ---------------------------------------------------------------------------
// Test: model counts tracking
// ---------------------------------------------------------------------------

func TestModelCountsTracking(t *testing.T) {
	provider := &mockProvider{response: "ok"}
	ctrl := buildController(t,
		map[string]router.Router{
			"high": &fixedRouter{score: 0.9},
			"low":  &fixedRouter{score: 0.1},
		},
		router.ModelPair{Strong: "strong", Weak: "weak"},
		provider,
	)

	ctx := context.Background()

	// Route via high scorer -> strong.
	_, err := ctrl.Route(ctx, router.UserSignal{Text: "test"}, "high", 0.5)
	require.NoError(t, err)

	// Route via low scorer -> weak.
	_, err = ctrl.Route(ctx, router.UserSignal{Text: "test"}, "low", 0.5)
	require.NoError(t, err)

	counts := ctrl.ModelCounts()
	assert.Equal(t, 1, counts["high"]["strong"])
	assert.Equal(t, 1, counts["low"]["weak"])
}

// ---------------------------------------------------------------------------
// Test: streaming request still completes (non-streaming response)
// ---------------------------------------------------------------------------

func TestStreamFlagStillCompletes(t *testing.T) {
	provider := &mockProvider{response: "ok"}
	ctrl := buildController(t,
		map[string]router.Router{"test": &fixedRouter{score: 0.5}},
		router.ModelPair{Strong: "s", Weak: "w"},
		provider,
	)

	handler := router.Handler(ctrl)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// Server ignores stream flag and returns a normal completion.
	body := `{"model":"router-test-0.5","messages":[{"role":"user","content":"hi"}],"stream":true}`
	resp, err := http.Post(
		srv.URL+"/v1/chat/completions",
		"application/json",
		strings.NewReader(body),
	)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
