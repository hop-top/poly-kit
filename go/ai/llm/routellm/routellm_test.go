package routellm

import (
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

func TestNew_MissingThreshold(t *testing.T) {
	cfg := llm.ResolvedConfig{
		URI: llm.URI{Scheme: "routellm", Model: "mf"},
		Provider: llm.ProviderConfig{
			Model: "mf",
		},
	}

	_, err := New(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected format")
}

func TestNew_InvalidThreshold(t *testing.T) {
	cfg := llm.ResolvedConfig{
		URI: llm.URI{Scheme: "routellm", Model: "mf:abc"},
		Provider: llm.ProviderConfig{
			Model: "mf:abc",
		},
	}

	_, err := New(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid threshold")
}

func TestNew_ColonOnlyModel(t *testing.T) {
	cfg := llm.ResolvedConfig{
		URI: llm.URI{Scheme: "routellm", Model: ":0.5"},
		Provider: llm.ProviderConfig{
			Model: ":0.5",
		},
	}

	_, err := New(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected format")
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
		wantName  string
		wantThres float64
		wantErr   bool
	}{
		{"valid", "mf:0.7", "mf", 0.7, false},
		{"zero", "bert:0", "bert", 0, false},
		{"one", "causal_llm:1", "causal_llm", 1, false},
		{"precise", "mf:0.123456", "mf", 0.123456, false},
		{"empty", "", "", 0, true},
		{"no_colon", "mf", "", 0, true},
		{"empty_name", ":0.5", "", 0, true},
		{"empty_threshold", "mf:", "", 0, true},
		{"non_numeric", "mf:abc", "", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, threshold, err := parseModelField(tt.model)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantName, name)
			assert.Equal(t, tt.wantThres, threshold)
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
