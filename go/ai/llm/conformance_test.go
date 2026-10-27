package llm_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
)

// Runs the provider-neutral sections of contracts/llm-v1. The vectors
// are the source of truth: a failure means this implementation drifted
// from the contract, not that a vector should be edited to fit. The
// anthropic_* sections run in go/ai/llm/anthropic.

const llmConfDir = "../../../contracts/llm-v1"

type llmConfFile struct {
	Version     string `json:"version"`
	Description string `json:"description"`
	RedactURI   []struct {
		Name string `json:"name"`
		In   string `json:"in"`
		Want string `json:"want"`
	} `json:"redact_uri"`
	ParseURI []struct {
		Name   string            `json:"name"`
		URI    string            `json:"uri"`
		Scheme string            `json:"scheme"`
		Model  string            `json:"model"`
		Host   string            `json:"host"`
		Params map[string]string `json:"params"`
		Error  string            `json:"error"`
	} `json:"parse_uri"`

	// Consumed by the anthropic runner; decoded here only so the
	// strict decode accepts the file.
	AnthropicRequest      json.RawMessage `json:"anthropic_request"`
	AnthropicRequestError json.RawMessage `json:"anthropic_request_error"`
	AnthropicHeaders      json.RawMessage `json:"anthropic_headers"`
	AnthropicStream       json.RawMessage `json:"anthropic_stream"`
	AnthropicAccumulate   json.RawMessage `json:"anthropic_accumulate"`
	AnthropicError        json.RawMessage `json:"anthropic_error"`
	KeyEndpoint           json.RawMessage `json:"key_endpoint"`
	CredentialHeaders     json.RawMessage `json:"credential_headers"`
	AnthropicLimits       json.RawMessage `json:"anthropic_limits"`
}

func loadLLMConf(t *testing.T) llmConfFile {
	t.Helper()
	entries, err := os.ReadDir(llmConfDir)
	require.NoError(t, err)
	for _, e := range entries {
		if e.Name() != "vectors.json" && e.Name() != "README.md" {
			t.Fatalf("contracts/llm-v1/%s: no runner consumes it", e.Name())
		}
	}
	raw, err := os.ReadFile(filepath.Join(llmConfDir, "vectors.json"))
	require.NoError(t, err)
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var f llmConfFile
	require.NoError(t, dec.Decode(&f))
	require.Equal(t, "v1", f.Version)
	require.NotEmpty(t, f.Description)
	return f
}

func TestConformanceLLMRedactURI(t *testing.T) {
	f := loadLLMConf(t)
	require.NotEmpty(t, f.RedactURI)
	for _, v := range f.RedactURI {
		t.Run(v.Name, func(t *testing.T) {
			assert.Equal(t, v.Want, llm.RedactURI(v.In))
		})
	}
}

func TestConformanceLLMParseURI(t *testing.T) {
	f := loadLLMConf(t)
	require.NotEmpty(t, f.ParseURI)
	for _, v := range f.ParseURI {
		t.Run(v.Name, func(t *testing.T) {
			got, err := llm.ParseURI(v.URI)
			if v.Error != "" {
				require.Error(t, err)
				assert.Equal(t, v.Error, err.Error())
				return
			}
			require.NoError(t, err)
			assert.Equal(t, v.Scheme, got.Scheme)
			assert.Equal(t, v.Model, got.Model)
			assert.Equal(t, v.Host, got.Host)
			assert.Equal(t, v.Params, got.Params)
		})
	}
}
