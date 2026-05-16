package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
)

// ---------- helpers ----------

func fakeServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func completionJSON(
	content, role, finishReason string,
) string {
	return fmt.Sprintf(`{
		"id": "chatcmpl-test",
		"object": "chat.completion",
		"created": 1700000000,
		"model": "gpt-4o",
		"choices": [{
			"index": 0,
			"message": {"role": %q, "content": %q},
			"finish_reason": %q
		}],
		"usage": {
			"prompt_tokens": 10,
			"completion_tokens": 20,
			"total_tokens": 30
		}
	}`, role, content, finishReason)
}

func toolCallJSON(
	content string, toolCalls []map[string]any,
) string {
	tcs, _ := json.Marshal(toolCalls)
	return fmt.Sprintf(`{
		"id": "chatcmpl-tools",
		"object": "chat.completion",
		"created": 1700000000,
		"model": "gpt-4o",
		"choices": [{
			"index": 0,
			"message": {
				"role": "assistant",
				"content": %q,
				"tool_calls": %s
			},
			"finish_reason": "tool_calls"
		}],
		"usage": {
			"prompt_tokens": 5,
			"completion_tokens": 15,
			"total_tokens": 20
		}
	}`, content, tcs)
}

func streamChunks(contents ...string) string {
	var b strings.Builder
	for _, c := range contents {
		chunk := fmt.Sprintf(
			`{"id":"chunk","object":"chat.completion.chunk",`+
				`"created":1700000000,"model":"gpt-4o",`+
				`"choices":[{"index":0,"delta":{"content":%q},`+
				`"finish_reason":null}]}`, c,
		)
		fmt.Fprintf(&b, "data: %s\n\n", chunk)
	}
	// Final chunk with finish_reason.
	b.WriteString(
		"data: {\"id\":\"chunk\",\"object\":\"chat.completion.chunk\"," +
			"\"created\":1700000000,\"model\":\"gpt-4o\"," +
			"\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n",
	)
	b.WriteString("data: [DONE]\n\n")
	return b.String()
}

func resolvedCfg(
	scheme, baseURL, model string,
) llm.ResolvedConfig {
	return llm.ResolvedConfig{
		URI: llm.URI{Scheme: scheme, Model: model},
		Provider: llm.ProviderConfig{
			APIKey:  "test-key",
			BaseURL: baseURL,
			Model:   model,
		},
	}
}

func newAdapter(
	t *testing.T, scheme, baseURL, model string,
) llm.Provider {
	t.Helper()
	cfg := resolvedCfg(scheme, baseURL, model)
	p, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	return p
}

// ---------- Complete ----------

func TestComplete_HappyPath(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "Bearer test-key",
			r.Header.Get("Authorization"))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(
			completionJSON("Hello!", "assistant", "stop"),
		))
	})

	p := newAdapter(t, "openai", srv.URL, "gpt-4o")
	comp := p.(llm.Completer)

	resp, err := comp.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{
			{Role: "user", Content: "Hi"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "Hello!", resp.Content)
	assert.Equal(t, "assistant", resp.Role)
	assert.Equal(t, "stop", resp.FinishReason)
	assert.Equal(t, 10, resp.Usage.PromptTokens)
	assert.Equal(t, 20, resp.Usage.CompletionTokens)
	assert.Equal(t, 30, resp.Usage.TotalTokens)
}

// ---------- Stream ----------

func TestStream_HappyPath(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		_, _ = w.Write([]byte(streamChunks("Hel", "lo", "!")))
	})

	p := newAdapter(t, "openai", srv.URL, "gpt-4o")
	streamer := p.(llm.Streamer)

	iter, err := streamer.Stream(context.Background(), llm.Request{
		Messages: []llm.Message{
			{Role: "user", Content: "Hi"},
		},
	})
	require.NoError(t, err)
	defer iter.Close()

	var collected strings.Builder
	for {
		tok, err := iter.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if tok.Done {
			break
		}
		collected.WriteString(tok.Content)
	}
	assert.Equal(t, "Hello!", collected.String())
}

// ---------- CallWithTools ----------

func TestCallWithTools_HappyPath(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		tools, ok := body["tools"].([]any)
		assert.True(t, ok, "tools should be present")
		assert.Len(t, tools, 1)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(toolCallJSON("", []map[string]any{
			{
				"id":   "call_123",
				"type": "function",
				"function": map[string]any{
					"name":      "get_weather",
					"arguments": `{"city":"NYC"}`,
				},
			},
		})))
	})

	p := newAdapter(t, "openai", srv.URL, "gpt-4o")
	tc := p.(llm.ToolCaller)

	tools := []llm.ToolDef{{
		Name:        "get_weather",
		Description: "Get weather for a city",
		Parameters: json.RawMessage(
			`{"type":"object","properties":{"city":{"type":"string"}}}`,
		),
	}}

	resp, err := tc.CallWithTools(context.Background(), llm.Request{
		Messages: []llm.Message{
			{Role: "user", Content: "Weather in NYC?"},
		},
	}, tools)
	require.NoError(t, err)
	require.Len(t, resp.ToolCalls, 1)
	assert.Equal(t, "call_123", resp.ToolCalls[0].ID)
	assert.Equal(t, "get_weather", resp.ToolCalls[0].Name)
	assert.JSONEq(t,
		`{"city":"NYC"}`, string(resp.ToolCalls[0].Arguments),
	)
}

// ---------- Error mapping ----------

func TestError_Auth401(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{
			"error": {
				"message": "Incorrect API key",
				"type": "invalid_request_error",
				"param": null,
				"code": "invalid_api_key"
			}
		}`))
	})

	p := newAdapter(t, "openai", srv.URL, "gpt-4o")
	comp := p.(llm.Completer)

	_, err := comp.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{
			{Role: "user", Content: "Hi"},
		},
	})
	require.Error(t, err)

	var authErr *llmerrors.ErrAuth
	assert.ErrorAs(t, err, &authErr)
	// Auth errors are NOT fallbackable.
	assert.False(t, llmerrors.IsFallbackable(err))
}

func TestError_RateLimit429(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{
			"error": {
				"message": "Rate limit exceeded",
				"type": "rate_limit_error",
				"param": null,
				"code": "rate_limit_exceeded"
			}
		}`))
	})

	p := newAdapter(t, "openai", srv.URL, "gpt-4o")
	comp := p.(llm.Completer)

	_, err := comp.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{
			{Role: "user", Content: "Hi"},
		},
	})
	require.Error(t, err)

	var rlErr *llmerrors.ErrRateLimit
	assert.ErrorAs(t, err, &rlErr)
	assert.True(t, llmerrors.IsFallbackable(err))
}

func TestError_Server500(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{
			"error": {
				"message": "Internal server error",
				"type": "server_error",
				"param": null,
				"code": null
			}
		}`))
	})

	p := newAdapter(t, "openai", srv.URL, "gpt-4o")
	comp := p.(llm.Completer)

	_, err := comp.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{
			{Role: "user", Content: "Hi"},
		},
	})
	require.Error(t, err)

	var httpErr *llmerrors.HTTPStatusError
	assert.ErrorAs(t, err, &httpErr)
	assert.Equal(t, 500, httpErr.StatusCode)
	assert.True(t, llmerrors.IsFallbackable(err))
}

// ---------- Scheme registration ----------

func TestSchemeRegistration(t *testing.T) {
	for _, scheme := range schemes {
		t.Run(scheme, func(t *testing.T) {
			srv := fakeServer(t, func(
				w http.ResponseWriter, _ *http.Request,
			) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(
					completionJSON("ok", "assistant", "stop"),
				))
			})

			p := newAdapter(t, scheme, srv.URL, "test-model")
			comp := p.(llm.Completer)

			resp, err := comp.Complete(
				context.Background(),
				llm.Request{
					Messages: []llm.Message{
						{Role: "user", Content: "Hi"},
					},
				},
			)
			require.NoError(t, err)
			assert.Equal(t, "ok", resp.Content)
		})
	}
}

// ---------- BaseURL override ----------

func TestBaseURL_Override(t *testing.T) {
	called := false
	srv := fakeServer(t, func(
		w http.ResponseWriter, _ *http.Request,
	) {
		called = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(
			completionJSON("custom", "assistant", "stop"),
		))
	})

	p := newAdapter(t, "lmstudio", srv.URL, "local-model")
	comp := p.(llm.Completer)

	resp, err := comp.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{
			{Role: "user", Content: "Hi"},
		},
	})
	require.NoError(t, err)
	assert.True(t, called, "should hit custom base URL")
	assert.Equal(t, "custom", resp.Content)
}

// ---------- Multimodal ----------

func TestComplete_MultimodalImageURL(t *testing.T) {
	var gotMessages []any
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotMessages, _ = body["messages"].([]any)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(completionJSON("ok", "assistant", "stop")))
	})

	p := newAdapter(t, "openai", srv.URL, "gpt-4o")
	comp := p.(llm.Completer)

	_, err := comp.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{
			Role: "user",
			Parts: []llm.ContentPart{
				{Type: llm.PartTypeText, Text: "describe this"},
				{Type: llm.PartTypeImage, Source: llm.URLSource("https://example.com/img.png"), MimeType: "image/png"},
			},
		}},
	})
	require.NoError(t, err)
	require.Len(t, gotMessages, 1)

	msg := gotMessages[0].(map[string]any)
	content := msg["content"].([]any)
	require.Len(t, content, 2)

	textPart := content[0].(map[string]any)
	assert.Equal(t, "text", textPart["type"])
	assert.Equal(t, "describe this", textPart["text"])

	imgPart := content[1].(map[string]any)
	assert.Equal(t, "image_url", imgPart["type"])
	imgURL := imgPart["image_url"].(map[string]any)
	assert.Equal(t, "https://example.com/img.png", imgURL["url"])
}

func TestComplete_MultimodalImageInline(t *testing.T) {
	var gotMessages []any
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotMessages, _ = body["messages"].([]any)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(completionJSON("ok", "assistant", "stop")))
	})

	p := newAdapter(t, "openai", srv.URL, "gpt-4o")
	comp := p.(llm.Completer)

	imgData := []byte{0xFF, 0xD8, 0xFF} // fake JPEG header
	_, err := comp.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{
			Role: "user",
			Parts: []llm.ContentPart{
				{Type: llm.PartTypeImage, Source: llm.InlineSource(imgData, "image/jpeg"), MimeType: "image/jpeg"},
			},
		}},
	})
	require.NoError(t, err)

	msg := gotMessages[0].(map[string]any)
	content := msg["content"].([]any)
	require.Len(t, content, 1)

	imgPart := content[0].(map[string]any)
	assert.Equal(t, "image_url", imgPart["type"])
	imgURL := imgPart["image_url"].(map[string]any)
	assert.True(t, strings.HasPrefix(imgURL["url"].(string), "data:image/jpeg;base64,"))
}

func TestComplete_MultimodalPDF(t *testing.T) {
	var gotMessages []any
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotMessages, _ = body["messages"].([]any)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(completionJSON("ok", "assistant", "stop")))
	})

	p := newAdapter(t, "openai", srv.URL, "gpt-4o")
	comp := p.(llm.Completer)

	pdfData := []byte("%PDF-1.4 fake")
	_, err := comp.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{
			Role: "user",
			Parts: []llm.ContentPart{
				{Type: llm.PartTypeImage, Source: llm.InlineSource(pdfData, "application/pdf"), MimeType: "application/pdf"},
			},
		}},
	})
	require.NoError(t, err)

	msg := gotMessages[0].(map[string]any)
	content := msg["content"].([]any)
	require.Len(t, content, 1)

	filePart := content[0].(map[string]any)
	assert.Equal(t, "file", filePart["type"])
	fileObj := filePart["file"].(map[string]any)
	assert.NotEmpty(t, fileObj["file_data"])
}

// ---------- Default base URLs ----------

func TestDefaultBaseURLs(t *testing.T) {
	expected := map[string]string{
		"openrouter": "https://openrouter.ai/api/v1",
		"xai":        "https://api.x.ai/v1",
		"groq":       "https://api.groq.com/openai/v1",
		"together":   "https://api.together.xyz/v1",
		"fireworks":  "https://api.fireworks.ai/inference/v1",
		"deepseek":   "https://api.deepseek.com",
		"mistral":    "https://api.mistral.ai/v1",
	}

	for scheme, wantURL := range expected {
		got, ok := defaultBaseURLs[scheme]
		assert.True(t, ok, "missing scheme %s", scheme)
		assert.Equal(t, wantURL, got, "scheme %s", scheme)
	}

	// openai and lmstudio use the hardcoded fallback, not the map.
	for _, s := range []string{"openai", "lmstudio"} {
		_, ok := defaultBaseURLs[s]
		assert.False(t, ok, "%s should not be in defaultBaseURLs", s)
	}
}

func TestNewSchemeDefaultBaseURL(t *testing.T) {
	// Verify each scheme creates an adapter without error when
	// no explicit base URL is provided.
	for _, scheme := range schemes {
		t.Run(scheme, func(t *testing.T) {
			cfg := llm.ResolvedConfig{
				URI: llm.URI{Scheme: scheme, Model: "test-model"},
				Provider: llm.ProviderConfig{
					APIKey: "test-key",
				},
			}
			p, err := New(cfg)
			require.NoError(t, err)
			a := p.(*Adapter)
			assert.Equal(t, scheme, a.scheme)
			assert.Equal(t, "test-model", a.model)
		})
	}
}

func TestNewExplicitBaseURLOverridesDefault(t *testing.T) {
	// When explicit BaseURL is set, it should be used regardless
	// of scheme defaults.
	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(
			completionJSON("ok", "assistant", "stop"),
		))
	})

	p := newAdapter(t, "groq", srv.URL, "llama-3-70b")
	comp := p.(llm.Completer)

	resp, err := comp.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{
			{Role: "user", Content: "Hi"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Content)
}

func TestAllSchemesRegistered(t *testing.T) {
	want := []string{
		"openai", "openrouter", "xai", "lmstudio",
		"groq", "together", "fireworks", "deepseek", "mistral",
	}
	assert.ElementsMatch(t, want, schemes)
}

// ---------- Interface checks ----------

func TestAdapter_ImplementsAllInterfaces(t *testing.T) {
	a := &Adapter{}
	var _ llm.Provider = a
	var _ llm.Completer = a
	var _ llm.Streamer = a
	var _ llm.ToolCaller = a
}
