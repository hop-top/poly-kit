package router

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
)

// mockProvider implements llm.Provider and llm.Completer.
type mockProvider struct {
	resp llm.Response
	err  error
	got  llm.Request
}

func (m *mockProvider) Close() error { return nil }
func (m *mockProvider) Complete(
	_ context.Context, req llm.Request,
) (llm.Response, error) {
	m.got = req
	return m.resp, m.err
}

func newTestServer(
	score float64, provider *mockProvider,
) *Server {
	reg := NewRegistry()
	_ = reg.Register("test", &stubRouter{score: score})

	ctrl := NewController(reg, ModelPair{
		Strong: "gpt-4",
		Weak:   "gpt-3.5",
	}, WithProvider(provider))

	return NewServer(ctrl)
}

func TestServer_Health(t *testing.T) {
	srv := newTestServer(0.5, &mockProvider{})
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]string
	err := json.NewDecoder(w.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, "online", body["status"])
}

func TestServer_ChatCompletion(t *testing.T) {
	provider := &mockProvider{
		resp: llm.Response{
			Content:      "Hello there!",
			Role:         "assistant",
			FinishReason: "stop",
			Usage: llm.Usage{
				PromptTokens:     10,
				CompletionTokens: 5,
				TotalTokens:      15,
			},
		},
	}

	srv := newTestServer(0.8, provider) // score 0.8 >= 0.5 => strong

	body := chatCompletionRequest{
		Model: "router-test-0.5",
		Messages: []chatMessage{
			{Role: "user", Content: "Hi"},
		},
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/v1/chat/completions",
		bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp chatCompletionResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "chat.completion", resp.Object)
	assert.Len(t, resp.Choices, 1)
	assert.Equal(t, "Hello there!", resp.Choices[0].Message.Content)
	assert.Equal(t, "stop", resp.Choices[0].FinishReason)
	assert.Equal(t, 15, resp.Usage.TotalTokens)
}

func TestServer_ChatCompletion_ExplicitZeroTemperature(t *testing.T) {
	provider := &mockProvider{resp: llm.Response{Content: "ok"}}
	srv := newTestServer(0.8, provider)

	zero := 0.0
	body := chatCompletionRequest{
		Model: "router-test-0.5",
		Messages: []chatMessage{
			{Role: "user", Content: "Hi"},
		},
		Temperature: &zero,
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/v1/chat/completions",
		bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, provider.got.Temperature,
		"explicit zero temperature should propagate to llm.Request")
	assert.Zero(t, *provider.got.Temperature)
}

func TestServer_ChatCompletion_NilTemperature(t *testing.T) {
	provider := &mockProvider{resp: llm.Response{Content: "ok"}}
	srv := newTestServer(0.8, provider)

	body := chatCompletionRequest{
		Model: "router-test-0.5",
		Messages: []chatMessage{
			{Role: "user", Content: "Hi"},
		},
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/v1/chat/completions",
		bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Nil(t, provider.got.Temperature,
		"unset temperature should stay nil in llm.Request")
}

func TestServer_ChatCompletion_InvalidModel(t *testing.T) {
	srv := newTestServer(0.5, &mockProvider{})

	body := chatCompletionRequest{
		Model: "invalid-model",
		Messages: []chatMessage{
			{Role: "user", Content: "Hi"},
		},
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/v1/chat/completions",
		bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestServer_ChatCompletion_InvalidBody(t *testing.T) {
	srv := newTestServer(0.5, &mockProvider{})

	req := httptest.NewRequest("POST", "/v1/chat/completions",
		bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGenerateID(t *testing.T) {
	id := generateID()
	assert.Contains(t, id, "chatcmpl-")
	assert.Greater(t, len(id), 10)
}

func TestCoalesce(t *testing.T) {
	assert.Equal(t, "a", coalesce("a", "b"))
	assert.Equal(t, "b", coalesce("", "b"))
}
