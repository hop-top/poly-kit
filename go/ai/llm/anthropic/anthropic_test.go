package anthropic_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	_ "hop.top/kit/go/ai/llm/anthropic" // register adapter
	llmerrors "hop.top/kit/go/ai/llm/errors"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// mockServer returns a test server that responds with the given
// handler and a ResolvedConfig pointing at it.
func mockServer(
	t *testing.T, handler http.HandlerFunc,
) (*httptest.Server, llm.ResolvedConfig) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return srv, llm.ResolvedConfig{
		Provider: llm.ProviderConfig{
			APIKey:  "test-key",
			BaseURL: srv.URL,
			Model:   "claude-sonnet-4-20250514",
		},
	}
}

// anthropicResponse builds a JSON Messages API response body.
func anthropicResponse(
	text string, stopReason string,
	inputTokens, outputTokens int,
) []byte {
	resp := map[string]any{
		"id":            "msg_test123",
		"type":          "message",
		"role":          "assistant",
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"model":         "claude-sonnet-4-20250514",
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
		"usage": map[string]any{
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestSchemeRegistration(t *testing.T) {
	_, err := llm.DefaultRegistry.Resolve("anthropic://claude-sonnet-4-20250514?api_key=k")
	// Should not return "provider not found" — the init registered it.
	var pnf *llmerrors.ErrProviderNotFound
	assert.False(t, errors.As(err, &pnf), "anthropic scheme should be registered")
}

func TestNew_MissingAPIKey(t *testing.T) {
	_, err := llm.DefaultRegistry.Resolve("anthropic://claude-sonnet-4-20250514")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "API key required")
}

func TestComplete_HappyPath(t *testing.T) {
	_, cfg := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		// Verify request shape.
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)

		assert.Equal(t, "claude-sonnet-4-20250514", req["model"])
		assert.Equal(t, float64(1024), req["max_tokens"])

		w.Header().Set("Content-Type", "application/json")
		w.Write(anthropicResponse("Hello!", "end_turn", 10, 5))
	})

	p, err := llm.DefaultRegistry.Resolve(
		"anthropic://claude-sonnet-4-20250514?api_key=" + cfg.Provider.APIKey +
			"&base_url=" + cfg.Provider.BaseURL,
	)
	// Resolve won't have the base_url in the right place; use factory directly.
	require.NoError(t, err)
	p.Close()

	// Use factory directly for precise control.
	provider, err := anthropicFactory(cfg)
	require.NoError(t, err)
	defer provider.Close()

	comp := provider.(llm.Completer)
	resp, err := comp.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{
			{Role: "user", Content: "Hi"},
		},
	})

	require.NoError(t, err)
	assert.Equal(t, "Hello!", resp.Content)
	assert.Equal(t, "assistant", resp.Role)
	assert.Equal(t, "end_turn", resp.FinishReason)
	assert.Equal(t, 10, resp.Usage.PromptTokens)
	assert.Equal(t, 5, resp.Usage.CompletionTokens)
	assert.Equal(t, 15, resp.Usage.TotalTokens)
}

func TestComplete_SystemMessageExtraction(t *testing.T) {
	var gotSystem any
	var gotMessages []any

	_, cfg := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)

		gotSystem = req["system"]
		gotMessages, _ = req["messages"].([]any)

		w.Header().Set("Content-Type", "application/json")
		w.Write(anthropicResponse("Ok", "end_turn", 5, 3))
	})

	provider, err := anthropicFactory(cfg)
	require.NoError(t, err)
	defer provider.Close()

	comp := provider.(llm.Completer)
	_, err = comp.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{
			{Role: "system", Content: "You are helpful"},
			{Role: "user", Content: "Hello"},
		},
	})
	require.NoError(t, err)

	// System should be a top-level array of text blocks.
	systemArr, ok := gotSystem.([]any)
	require.True(t, ok, "system should be an array")
	require.Len(t, systemArr, 1)
	block, _ := systemArr[0].(map[string]any)
	assert.Equal(t, "You are helpful", block["text"])

	// Messages should NOT contain the system message.
	require.Len(t, gotMessages, 1)
	msg, _ := gotMessages[0].(map[string]any)
	assert.Equal(t, "user", msg["role"])
}

func TestStream_HappyPath(t *testing.T) {
	_, cfg := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		events := []string{
			`event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-20250514","stop_reason":null,"usage":{"input_tokens":10,"output_tokens":0}}}`,
			`event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
			`event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}`,
			`event: content_block_stop
data: {"type":"content_block_stop","index":0}`,
			`event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
			`event: message_stop
data: {"type":"message_stop"}`,
		}

		for _, e := range events {
			w.Write([]byte(e + "\n\n"))
			flusher.Flush()
		}
	})

	provider, err := anthropicFactory(cfg)
	require.NoError(t, err)
	defer provider.Close()

	streamer := provider.(llm.Streamer)
	iter, err := streamer.Stream(context.Background(), llm.Request{
		Messages: []llm.Message{
			{Role: "user", Content: "Hi"},
		},
	})
	require.NoError(t, err)
	defer iter.Close()

	var chunks []string
	for {
		tok, err := iter.Next()
		require.NoError(t, err)
		if tok.Done {
			break
		}
		chunks = append(chunks, tok.Content)
	}

	assert.Equal(t, []string{"Hello", " world"}, chunks)
}

func TestCallWithTools_HappyPath(t *testing.T) {
	_, cfg := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)

		// Verify tools were sent.
		tools, _ := req["tools"].([]any)
		assert.Len(t, tools, 1)

		resp := map[string]any{
			"id":          "msg_test456",
			"type":        "message",
			"role":        "assistant",
			"stop_reason": "tool_use",
			"model":       "claude-sonnet-4-20250514",
			"content": []map[string]any{
				{"type": "text", "text": "Let me check."},
				{
					"type":  "tool_use",
					"id":    "call_123",
					"name":  "get_weather",
					"input": map[string]any{"city": "NYC"},
				},
			},
			"usage": map[string]any{
				"input_tokens":  20,
				"output_tokens": 15,
			},
		}
		b, _ := json.Marshal(resp)
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	})

	provider, err := anthropicFactory(cfg)
	require.NoError(t, err)
	defer provider.Close()

	tc := provider.(llm.ToolCaller)
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {"city": {"type": "string"}},
		"required": ["city"]
	}`)

	resp, err := tc.CallWithTools(context.Background(), llm.Request{
		Messages: []llm.Message{
			{Role: "user", Content: "Weather in NYC?"},
		},
	}, []llm.ToolDef{
		{
			Name:        "get_weather",
			Description: "Get weather for a city",
			Parameters:  schema,
		},
	})
	require.NoError(t, err)

	assert.Equal(t, "Let me check.", resp.Content)
	require.Len(t, resp.ToolCalls, 1)
	assert.Equal(t, "call_123", resp.ToolCalls[0].ID)
	assert.Equal(t, "get_weather", resp.ToolCalls[0].Name)

	var args map[string]string
	_ = json.Unmarshal(resp.ToolCalls[0].Arguments, &args)
	assert.Equal(t, "NYC", args["city"])
}

func TestError_Auth(t *testing.T) {
	_, cfg := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid key"}}`))
	})

	provider, err := anthropicFactory(cfg)
	require.NoError(t, err)
	defer provider.Close()

	comp := provider.(llm.Completer)
	_, err = comp.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "Hi"}},
	})
	require.Error(t, err)

	var authErr *llmerrors.ErrAuth
	assert.True(t, errors.As(err, &authErr), "expected ErrAuth, got %T: %v", err, err)
}

func TestError_RateLimit(t *testing.T) {
	_, cfg := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(429)
		w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`))
	})

	provider, err := anthropicFactory(cfg)
	require.NoError(t, err)
	defer provider.Close()

	comp := provider.(llm.Completer)
	_, err = comp.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "Hi"}},
	})
	require.Error(t, err)

	var rlErr *llmerrors.ErrRateLimit
	assert.True(t, errors.As(err, &rlErr), "expected ErrRateLimit, got %T: %v", err, err)
}

func TestError_ServerError(t *testing.T) {
	_, cfg := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"internal"}}`))
	})

	provider, err := anthropicFactory(cfg)
	require.NoError(t, err)
	defer provider.Close()

	comp := provider.(llm.Completer)
	_, err = comp.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "Hi"}},
	})
	require.Error(t, err)

	var httpErr *llmerrors.HTTPStatusError
	assert.True(t, errors.As(err, &httpErr), "expected HTTPStatusError, got %T: %v", err, err)
	assert.Equal(t, 500, httpErr.StatusCode)
}

func TestComplete_CustomMaxTokensAndTemp(t *testing.T) {
	var gotMaxTokens float64
	var gotTemp float64

	_, cfg := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)

		gotMaxTokens, _ = req["max_tokens"].(float64)
		gotTemp, _ = req["temperature"].(float64)

		w.Header().Set("Content-Type", "application/json")
		w.Write(anthropicResponse("ok", "end_turn", 1, 1))
	})

	provider, err := anthropicFactory(cfg)
	require.NoError(t, err)
	defer provider.Close()

	comp := provider.(llm.Completer)
	_, err = comp.Complete(context.Background(), llm.Request{
		Messages:    []llm.Message{{Role: "user", Content: "Hi"}},
		MaxTokens:   2048,
		Temperature: 0.7,
	})
	require.NoError(t, err)

	assert.Equal(t, float64(2048), gotMaxTokens)
	assert.InDelta(t, 0.7, gotTemp, 0.001)
}

func TestComplete_ModelOverride(t *testing.T) {
	var gotModel string

	_, cfg := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		gotModel, _ = req["model"].(string)

		w.Header().Set("Content-Type", "application/json")
		w.Write(anthropicResponse("ok", "end_turn", 1, 1))
	})

	provider, err := anthropicFactory(cfg)
	require.NoError(t, err)
	defer provider.Close()

	comp := provider.(llm.Completer)
	_, err = comp.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "Hi"}},
		Model:    "claude-opus-4-20250514",
	})
	require.NoError(t, err)
	assert.Equal(t, "claude-opus-4-20250514", gotModel)
}

// ---------------------------------------------------------------------------
// Multimodal
// ---------------------------------------------------------------------------

func TestComplete_MultimodalImageBase64(t *testing.T) {
	var gotMessages []any

	_, cfg := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		gotMessages, _ = req["messages"].([]any)

		w.Header().Set("Content-Type", "application/json")
		w.Write(anthropicResponse("ok", "end_turn", 1, 1))
	})

	provider, err := anthropicFactory(cfg)
	require.NoError(t, err)
	defer provider.Close()

	imgData := []byte{0x89, 0x50, 0x4E, 0x47} // PNG header
	comp := provider.(llm.Completer)
	_, err = comp.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{
			Role: "user",
			Parts: []llm.ContentPart{
				{Type: llm.PartTypeText, Text: "describe"},
				{Type: llm.PartTypeImage, Source: llm.InlineSource(imgData, "image/png"), MimeType: "image/png"},
			},
		}},
	})
	require.NoError(t, err)
	require.Len(t, gotMessages, 1)

	msg := gotMessages[0].(map[string]any)
	content := msg["content"].([]any)
	require.Len(t, content, 2)

	textBlock := content[0].(map[string]any)
	assert.Equal(t, "text", textBlock["type"])
	assert.Equal(t, "describe", textBlock["text"])

	imgBlock := content[1].(map[string]any)
	assert.Equal(t, "image", imgBlock["type"])
	source := imgBlock["source"].(map[string]any)
	assert.Equal(t, "base64", source["type"])
	assert.Equal(t, "image/png", source["media_type"])
	assert.NotEmpty(t, source["data"])
}

func TestComplete_MultimodalPDF(t *testing.T) {
	var gotMessages []any

	_, cfg := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		gotMessages, _ = req["messages"].([]any)

		w.Header().Set("Content-Type", "application/json")
		w.Write(anthropicResponse("ok", "end_turn", 1, 1))
	})

	provider, err := anthropicFactory(cfg)
	require.NoError(t, err)
	defer provider.Close()

	pdfData := []byte("%PDF-1.4 fake")
	comp := provider.(llm.Completer)
	_, err = comp.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{
			Role: "user",
			Parts: []llm.ContentPart{
				{Type: llm.PartTypeImage, Source: llm.InlineSource(pdfData, "application/pdf"), MimeType: "application/pdf"},
			},
		}},
	})
	require.NoError(t, err)
	require.Len(t, gotMessages, 1)

	msg := gotMessages[0].(map[string]any)
	content := msg["content"].([]any)
	require.Len(t, content, 1)

	docBlock := content[0].(map[string]any)
	assert.Equal(t, "document", docBlock["type"])
	source := docBlock["source"].(map[string]any)
	assert.Equal(t, "base64", source["type"])
	assert.Equal(t, "application/pdf", source["media_type"])
	assert.NotEmpty(t, source["data"])
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// anthropicFactory resolves a provider through the default registry
// using the given config's API key, base URL, and model.
func anthropicFactory(cfg llm.ResolvedConfig) (llm.Provider, error) {
	return llm.Resolve(
		"anthropic://" + cfg.Provider.Model +
			"?api_key=" + cfg.Provider.APIKey +
			"&base_url=" + cfg.Provider.BaseURL,
	)
}
