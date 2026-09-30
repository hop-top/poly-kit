package routellm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
)

// The adapter hands the request to its inner openai adapter; tool-call
// linkage must survive that hop onto the wire.
func TestCallWithTools_ToolLinkageReachesWire(t *testing.T) {
	var msgs []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Messages []map[string]any `json:"messages"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			msgs = body.Messages
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion",
				"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},
				"finish_reason":"stop"}]}`))
		}))
	defer srv.Close()

	p, err := New(llm.ResolvedConfig{
		URI: llm.URI{Scheme: "routellm", Model: "mf:0.7"},
		Provider: llm.ProviderConfig{
			Model: "mf:0.7", APIKey: "test-key", BaseURL: srv.URL,
		},
	})
	require.NoError(t, err)
	defer func() { assert.NoError(t, p.Close()) }()

	_, err = p.(llm.ToolCaller).CallWithTools(context.Background(), llm.Request{
		Messages: []llm.Message{
			{Role: "user", Content: "Weather?"},
			{Role: "assistant", ToolCalls: []llm.ToolCall{
				{ID: "call_1", Name: "get_weather", Arguments: json.RawMessage(`{"city":"NYC"}`)},
			}},
			{Role: "tool", ToolCallID: "call_1", Content: "sunny"},
		},
	}, []llm.ToolDef{{Name: "get_weather"}})
	require.NoError(t, err)

	require.Len(t, msgs, 3)
	calls, ok := msgs[1]["tool_calls"].([]any)
	require.True(t, ok, "assistant tool_calls dropped: %v", msgs[1])
	assert.Equal(t, "call_1", calls[0].(map[string]any)["id"])
	assert.Equal(t, "tool", msgs[2]["role"])
	assert.Equal(t, "call_1", msgs[2]["tool_call_id"])
}
