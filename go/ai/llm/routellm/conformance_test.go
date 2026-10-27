package routellm

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
)

// Runs the routellm_base_url section of contracts/llm-v1: the API root
// New resolves from the provider base URL, the router config (extras,
// ROUTELLM_BASE_URL) and the default. The file's other sections run in
// go/ai/llm and go/ai/llm/anthropic.

type confBaseURL struct {
	Name     string            `json:"name"`
	BaseURL  string            `json:"base_url"`
	Extras   map[string]any    `json:"extras"`
	Env      map[string]string `json:"env"`
	Hermetic bool              `json:"hermetic"`
	Want     string            `json:"want"`
}

func TestConformanceRouteLLMBaseURL(t *testing.T) {
	raw, err := os.ReadFile("../../../../contracts/llm-v1/vectors.json")
	require.NoError(t, err)
	var f struct {
		Version string          `json:"version"`
		Section json.RawMessage `json:"routellm_base_url"`
	}
	require.NoError(t, json.Unmarshal(raw, &f))
	require.Equal(t, "v1", f.Version)
	dec := json.NewDecoder(bytes.NewReader(f.Section))
	dec.DisallowUnknownFields()
	var vs []confBaseURL
	require.NoError(t, dec.Decode(&vs))
	require.NotEmpty(t, vs)

	for _, v := range vs {
		t.Run(v.Name, func(t *testing.T) {
			t.Setenv("ROUTELLM_BASE_URL", "")
			for k, val := range v.Env {
				t.Setenv(k, val)
			}
			p, err := New(llm.ResolvedConfig{
				URI: llm.URI{Scheme: "routellm", Model: "mf:0.5"},
				Provider: llm.ProviderConfig{
					Model: "mf:0.5", BaseURL: v.BaseURL, Extras: v.Extras,
				},
				Hermetic: v.Hermetic,
			})
			require.NoError(t, err)
			require.Equal(t, v.Want, p.(*Adapter).baseURL)
		})
	}
}
