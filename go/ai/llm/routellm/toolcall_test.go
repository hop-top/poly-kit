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

// The tool_names param reaches the inner adapter, and names round-trip
// through it.
func TestCallWithTools_ToolNamesThroughInner(t *testing.T) {
	var tools []string
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Tools []struct {
					Function struct {
						Name string `json:"name"`
					} `json:"function"`
				} `json:"tools"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			for _, tl := range body.Tools {
				tools = append(tools, tl.Function.Name)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","choices":[{"index":0,
				"message":{"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function",
				"function":{"name":"pane_open","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`))
		}))
	defer srv.Close()

	newP := func(params map[string]string) llm.Provider {
		p, err := New(llm.ResolvedConfig{
			URI: llm.URI{Scheme: "routellm", Model: "mf:0.7"},
			Provider: llm.ProviderConfig{
				Model: "mf:0.7", APIKey: "test-key", BaseURL: srv.URL, Params: params,
			},
		})
		require.NoError(t, err)
		return p
	}
	req := llm.Request{Messages: []llm.Message{{Role: "user", Content: "Hi"}}}
	defs := []llm.ToolDef{{Name: "pane.open"}}

	resp, err := newP(nil).(llm.ToolCaller).CallWithTools(context.Background(), req, defs)
	require.NoError(t, err)
	assert.Equal(t, []string{"pane_open"}, tools)
	require.Len(t, resp.ToolCalls, 1)
	assert.Equal(t, "pane.open", resp.ToolCalls[0].Name)

	_, err = newP(map[string]string{"tool_names": "strict"}).(llm.ToolCaller).CallWithTools(context.Background(), req, defs)
	require.Error(t, err)
	assert.Len(t, tools, 1, "strict mode sends nothing")
}
