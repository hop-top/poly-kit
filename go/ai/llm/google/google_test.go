package google_test

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
	"hop.top/kit/go/ai/llm/google"
)

// ---------------------------------------------------------------------------
// Factory / construction
// ---------------------------------------------------------------------------

func TestNew_HappyPath(t *testing.T) {
	p, err := google.New(llm.ResolvedConfig{
		Provider: llm.ProviderConfig{
			Model:  "gemini-2.0-flash",
			APIKey: "test-key",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, p)
	assert.NoError(t, p.Close())
}

func TestNew_MissingModel(t *testing.T) {
	_, err := google.New(llm.ResolvedConfig{
		Provider: llm.ProviderConfig{APIKey: "key"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model is required")
}

func TestNew_MissingAPIKey(t *testing.T) {
	// Ensure GEMINI_API_KEY is not set for this test.
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("LLM_API_KEY", "")

	_, err := google.New(llm.ResolvedConfig{
		Provider: llm.ProviderConfig{Model: "gemini-2.0-flash"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "API key is required")
}

func TestNew_APIKeyFromEnv(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "env-key")

	p, err := google.New(llm.ResolvedConfig{
		Provider: llm.ProviderConfig{Model: "gemini-2.0-flash"},
	})
	require.NoError(t, err)
	require.NotNil(t, p)
}

// ---------------------------------------------------------------------------
// Complete
// ---------------------------------------------------------------------------

func TestComplete_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Contains(t, r.URL.Path,
				"/models/gemini-2.0-flash:generateContent")
			assert.Equal(t, "test-key", r.URL.Query().Get("key"))
			assert.Equal(t, "application/json",
				r.Header.Get("Content-Type"))

			var req map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			contents := req["contents"].([]any)
			require.Len(t, contents, 1)

			writeJSON(w, geminiResponse(
				"Hello! How can I help?", "STOP", 8, 15,
			))
		},
	))
	defer srv.Close()

	p := mustNew(t, srv.URL, "gemini-2.0-flash")
	comp := p.(llm.Completer)

	resp, err := comp.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{
			{Role: "user", Content: "Hi"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "assistant", resp.Role)
	assert.Equal(t, "Hello! How can I help?", resp.Content)
	assert.Equal(t, 8, resp.Usage.PromptTokens)
	assert.Equal(t, 15, resp.Usage.CompletionTokens)
	assert.Equal(t, 23, resp.Usage.TotalTokens)
	assert.Equal(t, "stop", resp.FinishReason)
}

func TestComplete_WithTemperature(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			var req map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))

			gc, ok := req["generationConfig"].(map[string]any)
			require.True(t, ok, "generationConfig should be present")
			assert.InDelta(t, 0.7, gc["temperature"], 0.001)

			writeJSON(w, geminiResponse("ok", "STOP", 0, 0))
		},
	))
	defer srv.Close()

	p := mustNew(t, srv.URL, "gemini-2.0-flash")
	_, err := p.(llm.Completer).Complete(context.Background(), llm.Request{
		Messages:    []llm.Message{{Role: "user", Content: "hi"}},
		Temperature: 0.7,
	})
	require.NoError(t, err)
}

func TestComplete_RequestModelOverride(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			assert.Contains(t, r.URL.Path,
				"/models/gemini-2.5-pro:generateContent")
			writeJSON(w, geminiResponse("ok", "STOP", 0, 0))
		},
	))
	defer srv.Close()

	p := mustNew(t, srv.URL, "gemini-2.0-flash")
	_, err := p.(llm.Completer).Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
		Model:    "gemini-2.5-pro",
	})
	require.NoError(t, err)
}

func TestComplete_AssistantRoleMappedToModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			var req map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))

			contents := req["contents"].([]any)
			require.Len(t, contents, 2)

			msg1 := contents[0].(map[string]any)
			assert.Equal(t, "user", msg1["role"])

			msg2 := contents[1].(map[string]any)
			assert.Equal(t, "model", msg2["role"],
				"assistant should be mapped to model")

			writeJSON(w, geminiResponse("ok", "STOP", 0, 0))
		},
	))
	defer srv.Close()

	p := mustNew(t, srv.URL, "gemini-2.0-flash")
	_, err := p.(llm.Completer).Complete(context.Background(), llm.Request{
		Messages: []llm.Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi there"},
		},
	})
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Stream
// ---------------------------------------------------------------------------

func TestStream_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			assert.Contains(t, r.URL.Path,
				":streamGenerateContent")
			assert.Equal(t, "sse", r.URL.Query().Get("alt"))

			flusher, ok := w.(http.Flusher)
			require.True(t, ok)

			chunks := []string{
				sseChunk("Hello", ""),
				sseChunk(" world", ""),
				sseChunk("", "STOP"),
			}

			for _, c := range chunks {
				fmt.Fprint(w, c)
				flusher.Flush()
			}
		},
	))
	defer srv.Close()

	p := mustNew(t, srv.URL, "gemini-2.0-flash")
	streamer := p.(llm.Streamer)

	iter, err := streamer.Stream(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "Hi"}},
	})
	require.NoError(t, err)
	defer iter.Close()

	// Token 1
	tok, err := iter.Next()
	require.NoError(t, err)
	assert.Equal(t, "Hello", tok.Content)
	assert.False(t, tok.Done)

	// Token 2
	tok, err = iter.Next()
	require.NoError(t, err)
	assert.Equal(t, " world", tok.Content)
	assert.False(t, tok.Done)

	// Final token
	tok, err = iter.Next()
	require.NoError(t, err)
	assert.True(t, tok.Done)

	// After done, Next returns EOF
	_, err = iter.Next()
	assert.ErrorIs(t, err, io.EOF)
}

// ---------------------------------------------------------------------------
// ToolCaller
// ---------------------------------------------------------------------------

func TestCallWithTools_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			var req map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))

			// Verify tools are sent.
			tools, ok := req["tools"].([]any)
			require.True(t, ok)
			require.Len(t, tools, 1)

			writeJSON(w, map[string]any{
				"candidates": []map[string]any{{
					"content": map[string]any{
						"role": "model",
						"parts": []map[string]any{{
							"functionCall": map[string]any{
								"name": "get_weather",
								"args": map[string]any{
									"location": "Paris",
								},
							},
						}},
					},
					"finishReason": "STOP",
				}},
			})
		},
	))
	defer srv.Close()

	p := mustNew(t, srv.URL, "gemini-2.0-flash")
	tc := p.(llm.ToolCaller)

	resp, err := tc.CallWithTools(
		context.Background(),
		llm.Request{
			Messages: []llm.Message{
				{Role: "user", Content: "What's the weather in Paris?"},
			},
		},
		[]llm.ToolDef{{
			Name:        "get_weather",
			Description: "Get weather for a location",
			Parameters: json.RawMessage(
				`{"type":"object","properties":{"location":` +
					`{"type":"string"}}}`,
			),
		}},
	)
	require.NoError(t, err)
	require.Len(t, resp.ToolCalls, 1)
	assert.Equal(t, "get_weather", resp.ToolCalls[0].Name)
}

// ---------------------------------------------------------------------------
// Error handling
// ---------------------------------------------------------------------------

func TestComplete_ModelNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			writeJSON(w, map[string]any{
				"error": map[string]any{
					"code":    404,
					"message": "Model not found",
					"status":  "NOT_FOUND",
				},
			})
		},
	))
	defer srv.Close()

	p := mustNew(t, srv.URL, "nope")
	_, err := p.(llm.Completer).Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	require.Error(t, err)

	var modelErr *llmerrors.ErrModel
	assert.ErrorAs(t, err, &modelErr)
	assert.Equal(t, "nope", modelErr.Model)
}

func TestComplete_AuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			writeJSON(w, map[string]any{
				"error": map[string]any{
					"code":    401,
					"message": "API key not valid",
					"status":  "UNAUTHENTICATED",
				},
			})
		},
	))
	defer srv.Close()

	p := mustNew(t, srv.URL, "gemini-2.0-flash")
	_, err := p.(llm.Completer).Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	require.Error(t, err)

	var authErr *llmerrors.ErrAuth
	assert.ErrorAs(t, err, &authErr)
	assert.False(t, llmerrors.IsFallbackable(err))
}

func TestComplete_RateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			writeJSON(w, map[string]any{
				"error": map[string]any{
					"code":    429,
					"message": "Rate limit exceeded",
					"status":  "RESOURCE_EXHAUSTED",
				},
			})
		},
	))
	defer srv.Close()

	p := mustNew(t, srv.URL, "gemini-2.0-flash")
	_, err := p.(llm.Completer).Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	require.Error(t, err)

	var rlErr *llmerrors.ErrRateLimit
	assert.ErrorAs(t, err, &rlErr)
	assert.True(t, llmerrors.IsFallbackable(err))
}

func TestComplete_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":{"code":500,"message":"internal"}}`))
		},
	))
	defer srv.Close()

	p := mustNew(t, srv.URL, "gemini-2.0-flash")
	_, err := p.(llm.Completer).Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	require.Error(t, err)

	var httpErr *llmerrors.HTTPStatusError
	assert.ErrorAs(t, err, &httpErr)
	assert.Equal(t, 500, httpErr.StatusCode)
	assert.True(t, llmerrors.IsFallbackable(err))
}

func TestComplete_ContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		},
	))
	defer srv.Close()

	p := mustNew(t, srv.URL, "gemini-2.0-flash")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := p.(llm.Completer).Complete(ctx, llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// Multimodal
// ---------------------------------------------------------------------------

func TestComplete_MultimodalImageParts(t *testing.T) {
	var gotContents []any

	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			var req map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			gotContents, _ = req["contents"].([]any)

			writeJSON(w, geminiResponse("I see an image", "STOP", 0, 0))
		},
	))
	defer srv.Close()

	p := mustNew(t, srv.URL, "gemini-2.0-flash")
	comp := p.(llm.Completer)

	imgData := []byte{0x89, 0x50, 0x4E, 0x47}
	_, err := comp.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{
			Role: "user",
			Parts: []llm.ContentPart{
				{Type: llm.PartTypeText, Text: "describe"},
				{
					Type:   llm.PartTypeImage,
					Source: llm.InlineSource(imgData, "image/png"),
				},
			},
		}},
	})
	require.NoError(t, err)
	require.Len(t, gotContents, 1)

	msg := gotContents[0].(map[string]any)
	parts, ok := msg["parts"].([]any)
	require.True(t, ok)
	require.Len(t, parts, 2)

	// First part: text
	p0 := parts[0].(map[string]any)
	assert.Equal(t, "describe", p0["text"])

	// Second part: inlineData
	p1 := parts[1].(map[string]any)
	id, ok := p1["inlineData"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "image/png", id["mimeType"])
	assert.NotEmpty(t, id["data"])
}

// ---------------------------------------------------------------------------
// Scheme registration
// ---------------------------------------------------------------------------

func TestSchemeRegistration(t *testing.T) {
	reg := llm.NewRegistry()
	reg.Register("gemini", google.New)
	reg.Register("google", google.New)

	// Should panic on duplicate.
	assert.Panics(t, func() {
		reg.Register("gemini", google.New)
	})
}

func TestRegistryResolve(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, geminiResponse("resolved", "STOP", 0, 0))
		},
	))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")

	reg := llm.NewRegistry()
	reg.Register("gemini", google.New)

	p, err := reg.Resolve(fmt.Sprintf(
		"gemini://%s/gemini-2.0-flash?api_key=test-key", host,
	))
	require.NoError(t, err)
	defer p.Close()

	comp, ok := p.(llm.Completer)
	require.True(t, ok)

	resp, err := comp.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "resolved", resp.Content)
}

// ---------------------------------------------------------------------------
// Interface compliance
// ---------------------------------------------------------------------------

func TestInterfaceCompliance(t *testing.T) {
	p, err := google.New(llm.ResolvedConfig{
		Provider: llm.ProviderConfig{
			Model:  "gemini-2.0-flash",
			APIKey: "test-key",
		},
	})
	require.NoError(t, err)

	var _ = p
	var _ = p.(llm.Completer)
	var _ = p.(llm.Streamer)
	var _ = p.(llm.ToolCaller)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func mustNew(
	t *testing.T, baseURL, model string,
) llm.Provider {
	t.Helper()
	p, err := google.New(llm.ResolvedConfig{
		Provider: llm.ProviderConfig{
			BaseURL: baseURL,
			Model:   model,
			APIKey:  "test-key",
		},
	})
	require.NoError(t, err)
	return p
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func geminiResponse(
	text, finishReason string,
	promptTokens, completionTokens int,
) map[string]any {
	resp := map[string]any{
		"candidates": []map[string]any{{
			"content": map[string]any{
				"role": "model",
				"parts": []map[string]any{{
					"text": text,
				}},
			},
			"finishReason": finishReason,
		}},
	}

	total := promptTokens + completionTokens
	if promptTokens > 0 || completionTokens > 0 {
		resp["usageMetadata"] = map[string]any{
			"promptTokenCount":     promptTokens,
			"candidatesTokenCount": completionTokens,
			"totalTokenCount":      total,
		}
	}

	return resp
}

func sseChunk(text, finishReason string) string {
	resp := geminiResponse(text, finishReason, 0, 0)
	b, _ := json.Marshal(resp)
	return fmt.Sprintf("data: %s\n\n", b)
}
