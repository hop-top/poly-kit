package ollama_test

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
	"hop.top/kit/go/ai/llm/ollama"
)

// ---------------------------------------------------------------------------
// Factory / construction
// ---------------------------------------------------------------------------

func TestNew_DefaultBaseURL(t *testing.T) {
	p, err := ollama.New(llm.ResolvedConfig{
		Provider: llm.ProviderConfig{Model: "llama3"},
	})
	require.NoError(t, err)
	require.NotNil(t, p)
	assert.NoError(t, p.Close())
}

func TestNew_MissingModel(t *testing.T) {
	_, err := ollama.New(llm.ResolvedConfig{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model is required")
}

func TestNew_CustomBaseURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"message": map[string]string{
				"role":    "assistant",
				"content": "hi",
			},
			"done": true,
		})
	}))
	defer srv.Close()

	p, err := ollama.New(llm.ResolvedConfig{
		Provider: llm.ProviderConfig{
			BaseURL: srv.URL,
			Model:   "llama3",
		},
	})
	require.NoError(t, err)

	comp := p.(llm.Completer)
	resp, err := comp.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "hello"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "hi", resp.Content)
}

// ---------------------------------------------------------------------------
// Complete
// ---------------------------------------------------------------------------

func TestComplete_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/chat", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var req map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "llama3", req["model"])
		assert.Equal(t, false, req["stream"])

		writeJSON(w, map[string]any{
			"message": map[string]string{
				"role":    "assistant",
				"content": "Hello! How can I help?",
			},
			"done":              true,
			"eval_count":        15,
			"prompt_eval_count": 8,
		})
	}))
	defer srv.Close()

	p := mustNew(t, srv.URL, "llama3")
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))

		opts, ok := req["options"].(map[string]any)
		require.True(t, ok, "options should be present")
		assert.InDelta(t, 0.7, opts["temperature"], 0.001)

		writeJSON(w, map[string]any{
			"message": map[string]string{
				"role": "assistant", "content": "ok",
			},
			"done": true,
		})
	}))
	defer srv.Close()

	p := mustNew(t, srv.URL, "llama3")
	_, err := p.(llm.Completer).Complete(context.Background(), llm.Request{
		Messages:    []llm.Message{{Role: "user", Content: "hi"}},
		Temperature: 0.7,
	})
	require.NoError(t, err)
}

func TestComplete_RequestModelOverride(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "mistral", req["model"])

		writeJSON(w, map[string]any{
			"message": map[string]string{
				"role": "assistant", "content": "ok",
			},
			"done": true,
		})
	}))
	defer srv.Close()

	p := mustNew(t, srv.URL, "llama3")
	_, err := p.(llm.Completer).Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
		Model:    "mistral",
	})
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Stream
// ---------------------------------------------------------------------------

func TestStream_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, true, req["stream"])

		flusher, ok := w.(http.Flusher)
		require.True(t, ok)

		chunks := []map[string]any{
			{
				"message": map[string]string{
					"role": "assistant", "content": "Hello",
				},
				"done": false,
			},
			{
				"message": map[string]string{
					"role": "assistant", "content": " world",
				},
				"done": false,
			},
			{
				"message": map[string]string{
					"role": "assistant", "content": "",
				},
				"done":           true,
				"total_duration": 1000000,
				"eval_count":     5,
			},
		}

		for _, c := range chunks {
			b, _ := json.Marshal(c)
			fmt.Fprintf(w, "%s\n", b)
			flusher.Flush()
		}
	}))
	defer srv.Close()

	p := mustNew(t, srv.URL, "llama3")
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
// Error handling
// ---------------------------------------------------------------------------

func TestComplete_ModelNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, map[string]string{
			"error": "model \"nope\" not found",
		})
	}))
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

func TestComplete_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal"}`))
	}))
	defer srv.Close()

	p := mustNew(t, srv.URL, "llama3")
	_, err := p.(llm.Completer).Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	require.Error(t, err)

	var httpErr *llmerrors.HTTPStatusError
	assert.ErrorAs(t, err, &httpErr)
	assert.Equal(t, 500, httpErr.StatusCode)
	// 5xx should be fallbackable
	assert.True(t, llmerrors.IsFallbackable(err))
}

func TestComplete_ConnectionRefused(t *testing.T) {
	// Point at a port nothing listens on.
	p := mustNew(t, "http://127.0.0.1:1", "llama3")
	_, err := p.(llm.Completer).Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	require.Error(t, err)
	// net.OpError is fallbackable per IsFallbackable
	assert.True(t, llmerrors.IsFallbackable(err))
}

func TestComplete_ContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// hang forever; context will cancel
		<-r.Context().Done()
	}))
	defer srv.Close()

	p := mustNew(t, srv.URL, "llama3")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := p.(llm.Completer).Complete(ctx, llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	require.Error(t, err)
}

func TestStream_ModelNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, map[string]string{
			"error": "model \"bad\" not found",
		})
	}))
	defer srv.Close()

	p := mustNew(t, srv.URL, "bad")
	_, err := p.(llm.Streamer).Stream(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	require.Error(t, err)

	var modelErr *llmerrors.ErrModel
	assert.ErrorAs(t, err, &modelErr)
}

// ---------------------------------------------------------------------------
// Scheme registration
// ---------------------------------------------------------------------------

func TestSchemeRegistration(t *testing.T) {
	reg := llm.NewRegistry()
	reg.Register("ollama", ollama.New)

	// Should panic on duplicate.
	assert.Panics(t, func() {
		reg.Register("ollama", ollama.New)
	})
}

func TestRegistryResolve(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"message": map[string]string{
				"role": "assistant", "content": "resolved",
			},
			"done": true,
		})
	}))
	defer srv.Close()

	// Extract host:port from test server URL.
	host := strings.TrimPrefix(srv.URL, "http://")

	reg := llm.NewRegistry()
	reg.Register("ollama", ollama.New)

	p, err := reg.Resolve(fmt.Sprintf("ollama://%s/llama3", host))
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
// Multimodal
// ---------------------------------------------------------------------------

func TestComplete_MultimodalImageParts(t *testing.T) {
	var gotMessages []any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		gotMessages, _ = req["messages"].([]any)

		writeJSON(w, map[string]any{
			"message": map[string]string{
				"role": "assistant", "content": "I see an image",
			},
			"done": true,
		})
	}))
	defer srv.Close()

	p := mustNew(t, srv.URL, "llava")
	comp := p.(llm.Completer)

	imgData := []byte{0x89, 0x50, 0x4E, 0x47} // PNG header bytes
	_, err := comp.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{
			Role: "user",
			Parts: []llm.ContentPart{
				{Type: llm.PartTypeText, Text: "describe"},
				{Type: llm.PartTypeImage, Source: llm.InlineSource(imgData, "image/png")},
			},
		}},
	})
	require.NoError(t, err)
	require.Len(t, gotMessages, 1)

	msg := gotMessages[0].(map[string]any)
	assert.Equal(t, "user", msg["role"])
	assert.Equal(t, "describe", msg["content"])

	images, ok := msg["images"].([]any)
	require.True(t, ok, "images should be present")
	require.Len(t, images, 1)

	// Should be plain base64, no data URI prefix.
	imgStr := images[0].(string)
	assert.False(t, strings.HasPrefix(imgStr, "data:"), "should be plain base64")
	assert.NotEmpty(t, imgStr)
}

func TestComplete_TextOnlyParts(t *testing.T) {
	var gotMessages []any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		gotMessages, _ = req["messages"].([]any)

		writeJSON(w, map[string]any{
			"message": map[string]string{
				"role": "assistant", "content": "ok",
			},
			"done": true,
		})
	}))
	defer srv.Close()

	p := mustNew(t, srv.URL, "llama3")
	comp := p.(llm.Completer)

	_, err := comp.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{
			Role: "user",
			Parts: []llm.ContentPart{
				{Type: llm.PartTypeText, Text: "hello "},
				{Type: llm.PartTypeText, Text: "world"},
			},
		}},
	})
	require.NoError(t, err)
	require.Len(t, gotMessages, 1)

	msg := gotMessages[0].(map[string]any)
	assert.Equal(t, "hello world", msg["content"])
	_, hasImages := msg["images"]
	assert.False(t, hasImages, "images key should be absent for text-only")
}

// ---------------------------------------------------------------------------
// Interface compliance
// ---------------------------------------------------------------------------

func TestInterfaceCompliance(t *testing.T) {
	p := mustNewDefault(t)
	var _ = p
	var _ = p.(llm.Completer)
	var _ = p.(llm.Streamer)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func mustNew(t *testing.T, baseURL, model string) llm.Provider {
	t.Helper()
	p, err := ollama.New(llm.ResolvedConfig{
		Provider: llm.ProviderConfig{
			BaseURL: baseURL,
			Model:   model,
		},
	})
	require.NoError(t, err)
	return p
}

func mustNewDefault(t *testing.T) llm.Provider {
	t.Helper()
	p, err := ollama.New(llm.ResolvedConfig{
		Provider: llm.ProviderConfig{Model: "llama3"},
	})
	require.NoError(t, err)
	return p
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
