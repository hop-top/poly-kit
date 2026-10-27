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
	llmerrors "hop.top/kit/go/ai/llm/errors"
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
	AnthropicResponse     json.RawMessage `json:"anthropic_response"`
	AnthropicToolNames    json.RawMessage `json:"anthropic_tool_names"`
	ToolNames             []struct {
		Name      string   `json:"name"`
		Rule      string   `json:"rule"`
		Mode      string   `json:"mode"`
		Tools     []string `json:"tools"`
		Want      []string `json:"want"`
		Calls     []string `json:"calls"`
		WantCalls []string `json:"want_calls"`
		Error     *struct {
			Reason string `json:"reason"`
			Name   string `json:"name"`
			Other  string `json:"other"`
		} `json:"error"`
	} `json:"tool_names"`
	UsageCost []struct {
		Name  string    `json:"name"`
		Usage confUsage `json:"usage"`
		Price struct {
			Input      float64 `json:"input"`
			Output     float64 `json:"output"`
			CacheRead  float64 `json:"cache_read"`
			CacheWrite float64 `json:"cache_write"`
		} `json:"price"`
		Want float64 `json:"want"`
	} `json:"usage_cost"`
}

func loadLLMConf(t *testing.T) llmConfFile {
	t.Helper()
	entries, err := os.ReadDir(llmConfDir)
	require.NoError(t, err)
	for _, e := range entries {
		if e.Name() != "vectors.json" && e.Name() != "README.md" && e.Name() != "tool-names.md" {
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

func TestConformanceLLMUsageCost(t *testing.T) {
	f := loadLLMConf(t)
	require.NotEmpty(t, f.UsageCost)
	for _, v := range f.UsageCost {
		t.Run(v.Name, func(t *testing.T) {
			p := llm.Price{Input: v.Price.Input, Output: v.Price.Output, CacheRead: v.Price.CacheRead, CacheWrite: v.Price.CacheWrite}
			assert.InDelta(t, v.Want, v.Usage.toLLM().Cost(p), 1e-9)
		})
	}
}

type confUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	CacheReadTokens     int `json:"cache_read_tokens"`
	CacheCreationTokens int `json:"cache_creation_tokens"`
	TotalTokens         int `json:"total_tokens"`
}

func (u confUsage) toLLM() llm.Usage { return llm.Usage(u) }

var confToolNameRules = map[string]llm.ToolNameRule{
	"anthropic": llm.AnthropicToolNames,
	"openai":    llm.OpenAIToolNames,
	"gemini":    llm.GeminiToolNames,
}

func TestConformanceLLMToolNames(t *testing.T) {
	f := loadLLMConf(t)
	require.NotEmpty(t, f.ToolNames)
	for _, v := range f.ToolNames {
		t.Run(v.Name, func(t *testing.T) {
			rule, ok := confToolNameRules[v.Rule]
			require.True(t, ok, "unknown rule %q", v.Rule)
			mode := llm.ToolNamesSanitize
			if v.Mode != "" {
				mode = llm.ToolNameMode(v.Mode)
			}
			var defs []llm.ToolDef
			for _, n := range v.Tools {
				defs = append(defs, llm.ToolDef{Name: n})
			}
			names, err := llm.NewToolNames(rule, mode, defs)
			if v.Error != nil {
				var te *llmerrors.ErrToolName
				require.ErrorAs(t, err, &te)
				assert.Equal(t, v.Error.Reason, te.Reason)
				assert.Equal(t, v.Error.Name, te.Name)
				assert.Equal(t, v.Error.Other, te.Other)
				assert.Equal(t, rule.Provider, te.Provider)
				return
			}
			require.NoError(t, err)
			var got []string
			for _, n := range v.Tools {
				got = append(got, names.Wire(n))
			}
			assert.Equal(t, v.Want, got)
			for _, w := range got {
				assert.True(t, rule.Valid(w), "wire name %q invalid for %s", w, v.Rule)
			}
			if mode == llm.ToolNamesSanitize {
				for i, n := range v.Tools {
					s, err := rule.Sanitize(n)
					require.NoError(t, err)
					assert.Equal(t, v.Want[i], s, "Sanitize(%q)", n)
				}
			}
			var back []string
			for _, c := range v.Calls {
				back = append(back, names.Original(c))
			}
			assert.Equal(t, v.WantCalls, back)
		})
	}
}
