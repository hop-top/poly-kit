package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
)

func TestServer_ChatCompletion_ToolLinkage(t *testing.T) {
	provider := &mockProvider{resp: llm.Response{Content: "ok"}}
	srv := newTestServer(0.8, provider)

	body := `{
		"model": "router-test-0.5",
		"messages": [
			{"role": "user", "content": "Weather?"},
			{"role": "assistant", "content": null, "tool_calls": [
				{"id": "call_1", "type": "function",
				 "function": {"name": "get_weather", "arguments": "{\"city\":\"NYC\"}"},
				 "extra_content": {"google": {"thought_signature": "SIG_A"}}}
			]},
			{"role": "tool", "tool_call_id": "call_1", "content": "sunny"}
		]
	}`
	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	msgs := provider.got.Messages
	require.Len(t, msgs, 3)
	require.Len(t, msgs[1].ToolCalls, 1, "assistant tool_calls must reach the provider")
	assert.Equal(t, "call_1", msgs[1].ToolCalls[0].ID)
	assert.Equal(t, "get_weather", msgs[1].ToolCalls[0].Name)
	assert.JSONEq(t, `{"city":"NYC"}`, string(msgs[1].ToolCalls[0].Arguments))
	assert.JSONEq(t, `{"thought_signature":"SIG_A"}`,
		string(msgs[1].ToolCalls[0].ProviderData["google"]),
		"extra_content must reach the provider as ProviderData")
	assert.Equal(t, "tool", msgs[2].Role)
	assert.Equal(t, "call_1", msgs[2].ToolCallID)
	assert.Equal(t, "sunny", msgs[2].Content)
}
