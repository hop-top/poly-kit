package google_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/ai/llm/internal/transporttest"
)

func geminiReply(text string) string {
	b, _ := json.Marshal(map[string]any{
		"candidates": []any{map[string]any{
			"content":      map[string]any{"role": "model", "parts": []any{map[string]any{"text": text}}},
			"finishReason": "STOP",
		}},
		"usageMetadata": map[string]int{"promptTokenCount": 2, "candidatesTokenCount": 3, "totalTokenCount": 5},
	})
	return string(b)
}

func TestStructured_WireGenerationConfig(t *testing.T) {
	fake := transporttest.NewFake(t, "application/json", geminiReply(transporttest.VerdictOK))
	p, err := llm.Resolve("google://gemini-2.0-flash?api_key=k&base_url=" + fake.URL)
	require.NoError(t, err)
	defer p.Close()

	temp := 0.0
	_, err = p.(llm.Completer).Complete(context.Background(), llm.Request{
		Messages:       []llm.Message{{Role: "user", Content: "judge"}},
		Temperature:    &temp,
		ResponseFormat: &llm.ResponseFormat{Schema: json.RawMessage(transporttest.VerdictSchema)},
	})
	require.NoError(t, err)

	var body struct {
		GenerationConfig map[string]json.RawMessage `json:"generationConfig"`
	}
	require.NoError(t, json.Unmarshal(fake.Body(), &body))
	assert.JSONEq(t, `"application/json"`, string(body.GenerationConfig["responseMimeType"]))
	assert.JSONEq(t, transporttest.VerdictSchema, string(body.GenerationConfig["responseJsonSchema"]))
	assert.NotContains(t, body.GenerationConfig, "responseSchema", "the JSON Schema field, not the OpenAPI subset")
	assert.Contains(t, body.GenerationConfig, "temperature", "other generation settings survive")

	_, err = p.(llm.Completer).Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "judge"}},
	})
	require.NoError(t, err)
	assert.NotContains(t, string(fake.Body()), "responseMimeType")
}

func TestStructured_BadFormatNotSent(t *testing.T) {
	fake := transporttest.NewFake(t, "application/json", geminiReply("x"))
	p, err := llm.Resolve("google://gemini-2.0-flash?api_key=k&base_url=" + fake.URL)
	require.NoError(t, err)
	defer p.Close()
	_, err = p.(llm.Completer).Complete(context.Background(), llm.Request{
		Messages:       []llm.Message{{Role: "user", Content: "judge"}},
		ResponseFormat: &llm.ResponseFormat{Name: "a b", Schema: json.RawMessage(`{}`)},
	})
	require.Error(t, err)
	assert.Nil(t, fake.Body())
}

func TestStructured_XRRReplay(t *testing.T) {
	var res llm.StructuredResult
	transporttest.Cassette{
		Dir:         filepath.Join("testdata", "xrr-structured"),
		Env:         "GOOGLE_STRUCTURED_XRR_RECORD",
		ContentType: "application/json",
		Reply:       geminiReply(transporttest.VerdictOK),
		URI:         func(base string) string { return "google://gemini-2.0-flash?api_key=k&base_url=" + base },
	}.Run(t, func(p llm.Provider) error {
		var err error
		res, err = llm.CompleteStructured(context.Background(), p.(llm.Completer),
			llm.Request{Messages: []llm.Message{{Role: "user", Content: "judge"}}},
			llm.ResponseFormat{Schema: json.RawMessage(transporttest.VerdictSchema)})
		return err
	})
	assert.JSONEq(t, transporttest.VerdictOK, string(res.Data))
	assert.Equal(t, 5, res.Usage.TotalTokens)
}
