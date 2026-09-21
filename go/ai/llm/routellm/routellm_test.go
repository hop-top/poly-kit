package routellm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
)

func TestNew_ValidConfig(t *testing.T) {
	cfg := llm.ResolvedConfig{
		URI: llm.URI{Scheme: "routellm", Model: "mf:0.7"},
		Provider: llm.ProviderConfig{
			Model:  "mf:0.7",
			APIKey: "test-key",
		},
	}

	p, err := New(cfg)
	require.NoError(t, err)
	require.NotNil(t, p)

	a, ok := p.(*Adapter)
	require.True(t, ok)
	assert.Equal(t, "mf", a.routerName)
	assert.Equal(t, 0.7, a.threshold)
	assert.NotNil(t, a.inner)

	assert.NoError(t, p.Close())
}

func TestNew_ThresholdZero(t *testing.T) {
	cfg := llm.ResolvedConfig{
		URI: llm.URI{Scheme: "routellm", Model: "mf:0"},
		Provider: llm.ProviderConfig{
			Model: "mf:0",
		},
	}

	p, err := New(cfg)
	require.NoError(t, err)

	a := p.(*Adapter)
	assert.Equal(t, 0.0, a.threshold)
	assert.NoError(t, p.Close())
}

func TestNew_ThresholdOne(t *testing.T) {
	cfg := llm.ResolvedConfig{
		URI: llm.URI{Scheme: "routellm", Model: "bert:1"},
		Provider: llm.ProviderConfig{
			Model: "bert:1",
		},
	}

	p, err := New(cfg)
	require.NoError(t, err)

	a := p.(*Adapter)
	assert.Equal(t, 1.0, a.threshold)
	assert.NoError(t, p.Close())
}

func TestNew_ThresholdBelowRange(t *testing.T) {
	cfg := llm.ResolvedConfig{
		URI: llm.URI{Scheme: "routellm", Model: "mf:-0.1"},
		Provider: llm.ProviderConfig{
			Model: "mf:-0.1",
		},
	}

	_, err := New(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of range")
}

func TestNew_ThresholdAboveRange(t *testing.T) {
	cfg := llm.ResolvedConfig{
		URI: llm.URI{Scheme: "routellm", Model: "mf:1.5"},
		Provider: llm.ProviderConfig{
			Model: "mf:1.5",
		},
	}

	_, err := New(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of range")
}

func TestNew_EmptyModel(t *testing.T) {
	cfg := llm.ResolvedConfig{
		URI:      llm.URI{Scheme: "routellm"},
		Provider: llm.ProviderConfig{},
	}

	_, err := New(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model field is required")
}

func TestNew_BareNameIsTier(t *testing.T) {
	cfg := llm.ResolvedConfig{
		URI: llm.URI{Scheme: "routellm", Model: "private"},
		Provider: llm.ProviderConfig{
			Model: "private",
		},
	}

	p, err := New(cfg)
	require.NoError(t, err)

	a := p.(*Adapter)
	assert.True(t, a.IsTier())
	assert.Equal(t, "private", a.tier)
	assert.Empty(t, a.routerName)
	assert.NoError(t, p.Close())
}

func TestNew_NonNumericSuffixIsTier(t *testing.T) {
	// "mf:abc" used to fail threshold parsing. A colon is legal in a
	// tier name, so an unparseable suffix now makes the whole string a
	// tier and the server decides whether it exists.
	cfg := llm.ResolvedConfig{
		URI: llm.URI{Scheme: "routellm", Model: "mf:abc"},
		Provider: llm.ProviderConfig{
			Model: "mf:abc",
		},
	}

	p, err := New(cfg)
	require.NoError(t, err)

	a := p.(*Adapter)
	assert.True(t, a.IsTier())
	assert.Equal(t, "mf:abc", a.tier)
	assert.NoError(t, p.Close())
}

func TestNew_LeadingColonIsTier(t *testing.T) {
	// Empty router name, so not a router pair; falls through to tier.
	cfg := llm.ResolvedConfig{
		URI: llm.URI{Scheme: "routellm", Model: ":0.5"},
		Provider: llm.ProviderConfig{
			Model: ":0.5",
		},
	}

	p, err := New(cfg)
	require.NoError(t, err)

	a := p.(*Adapter)
	assert.True(t, a.IsTier())
	assert.Equal(t, ":0.5", a.tier)
	assert.NoError(t, p.Close())
}

func TestNew_FallsBackToURIModel(t *testing.T) {
	cfg := llm.ResolvedConfig{
		URI:      llm.URI{Scheme: "routellm", Model: "bert:0.5"},
		Provider: llm.ProviderConfig{
			// Model deliberately empty; should fall back to URI.Model.
		},
	}

	p, err := New(cfg)
	require.NoError(t, err)

	a := p.(*Adapter)
	assert.Equal(t, "bert", a.routerName)
	assert.Equal(t, 0.5, a.threshold)
	assert.NoError(t, p.Close())
}

func TestParseModelField(t *testing.T) {
	tests := []struct {
		name      string
		model     string
		wantTier  string // non-empty means the tier shape is expected
		wantName  string
		wantThres float64
		wantErr   bool
	}{
		// Router pairs: suffix after the last colon parses as a float.
		{"valid", "mf:0.7", "", "mf", 0.7, false},
		{"zero", "bert:0", "", "bert", 0, false},
		{"one", "causal_llm:1", "", "causal_llm", 1, false},
		{"precise", "mf:0.123456", "", "mf", 0.123456, false},
		{"exponent", "mf:5e-1", "", "mf", 0.5, false},
		{"colon_in_router_name", "a:b:0.5", "", "a:b", 0.5, false},

		// Tiers: everything else non-empty.
		{"bare_tier", "private", "private", "", 0, false},
		{"underscore_tier", "coding_fast", "coding_fast", "", 0, false},
		{"server_router_id", "router-mf-0.5", "router-mf-0.5", "", 0, false},
		{"non_numeric_suffix", "mf:abc", "mf:abc", "", 0, false},
		{"colon_in_tier_name", "a:b", "a:b", "", 0, false},
		{"empty_name", ":0.5", ":0.5", "", 0, false},
		{"empty_threshold", "mf:", "mf:", "", 0, false},
		{"nan_suffix", "mf:NaN", "mf:NaN", "", 0, false},
		{"inf_suffix", "mf:Inf", "mf:Inf", "", 0, false},

		{"empty", "", "", "", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseModelField(tt.model)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)

			if tt.wantTier != "" {
				assert.True(t, got.isTier, "expected tier shape")
				assert.Equal(t, tt.wantTier, got.tier)
				assert.Empty(t, got.routerName)
				return
			}

			assert.False(t, got.isTier, "expected router pair shape")
			assert.Empty(t, got.tier)
			assert.Equal(t, tt.wantName, got.routerName)
			assert.Equal(t, tt.wantThres, got.threshold)
		})
	}
}

func TestNew_ConfigBaseURLOverride(t *testing.T) {
	cfg := llm.ResolvedConfig{
		URI: llm.URI{Scheme: "routellm", Model: "mf:0.5"},
		Provider: llm.ProviderConfig{
			Model: "mf:0.5",
			Extras: map[string]any{
				"routellm": map[string]any{
					"base_url": "http://custom:9090",
				},
			},
		},
	}

	p, err := New(cfg)
	require.NoError(t, err)
	require.NotNil(t, p)

	a := p.(*Adapter)
	assert.Equal(t, "http://custom:9090", a.config.BaseURL)
	assert.NoError(t, p.Close())
}

func TestNew_InterfaceCompliance(t *testing.T) {
	cfg := llm.ResolvedConfig{
		URI: llm.URI{Scheme: "routellm", Model: "mf:0.5"},
		Provider: llm.ProviderConfig{
			Model: "mf:0.5",
		},
	}

	p, err := New(cfg)
	require.NoError(t, err)

	_, ok := p.(llm.Completer)
	assert.True(t, ok, "should implement Completer")

	_, ok = p.(llm.Streamer)
	assert.True(t, ok, "should implement Streamer")

	_, ok = p.(llm.ToolCaller)
	assert.True(t, ok, "should implement ToolCaller")

	assert.NoError(t, p.Close())
}

// TestWireModel asserts the model string that actually reaches the
// server for each shape. The inner openai adapter's model field is
// unexported and in another package, so the only honest check is the
// request body itself.
func TestWireModel(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  string
	}{
		// Router pair: translated to the server's router id. This path
		// must stay byte-identical to the pre-tier behavior.
		{"router_pair", "mf:0.7", "router-mf-0.7"},
		{"threshold_zero", "bert:0", "router-bert-0"},
		{"threshold_one", "causal_llm:1", "router-causal_llm-1"},
		{"trailing_zeros_trimmed", "mf:0.50", "router-mf-0.5"},

		// Tier: verbatim, server resolves router and threshold.
		{"tier", "private", "private"},
		{"tier_underscore", "coding_fast", "coding_fast"},
		{"tier_server_router_id", "router-mf-0.5", "router-mf-0.5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			srv := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					var body struct {
						Model string `json:"model"`
					}
					require.NoError(t,
						json.NewDecoder(r.Body).Decode(&body))
					got = body.Model

					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{
						"id": "x",
						"object": "chat.completion",
						"choices": [{
							"index": 0,
							"message": {
								"role": "assistant",
								"content": "ok"
							},
							"finish_reason": "stop"
						}]
					}`))
				}))
			defer srv.Close()

			cfg := llm.ResolvedConfig{
				URI: llm.URI{Scheme: "routellm", Model: tt.model},
				Provider: llm.ProviderConfig{
					Model:   tt.model,
					APIKey:  "test-key",
					BaseURL: srv.URL,
				},
			}

			p, err := New(cfg)
			require.NoError(t, err)
			defer func() { assert.NoError(t, p.Close()) }()

			_, err = p.(llm.Completer).Complete(
				context.Background(),
				llm.Request{
					Messages: []llm.Message{
						{Role: "user", Content: "hi"},
					},
				},
			)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
