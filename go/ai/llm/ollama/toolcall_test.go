package ollama_test

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

// captureMessages runs one Complete against a fake server and returns
// the "messages" array kit serialized.
func captureMessages(t *testing.T, msgs []llm.Message) ([]any, error) {
	t.Helper()
	var got []any
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			got, _ = body["messages"].([]any)
			writeJSON(w, map[string]any{
				"message": map[string]any{"role": "assistant", "content": "done"},
				"done":    true,
			})
		},
	))
	t.Cleanup(srv.Close)

	_, err := mustNew(t, srv.URL, "llama3").(llm.Completer).Complete(
		context.Background(), llm.Request{Messages: msgs},
	)
	return got, err
}

func TestToolLinkage_WireShape(t *testing.T) {
	got, err := captureMessages(t, []llm.Message{
		{Role: "user", Content: "Weather in NYC and LA?"},
		{Role: "assistant", Content: "Checking.", ToolCalls: []llm.ToolCall{
			{ID: "call_1", Name: "get_weather", Arguments: json.RawMessage(`{"city":"NYC"}`)},
			{ID: "call_2", Name: "get_weather"},
		}},
		{Role: "tool", ToolCallID: "call_1", Content: "sunny"},
		{Role: "tool", ToolCallID: "call_2", Content: "rainy"},
	})
	require.NoError(t, err)
	require.Len(t, got, 4)

	asst := got[1].(map[string]any)
	assert.Equal(t, "assistant", asst["role"])
	assert.Equal(t, "Checking.", asst["content"])
	calls, ok := asst["tool_calls"].([]any)
	require.True(t, ok, "assistant message must carry tool_calls: %v", asst)
	require.Len(t, calls, 2)
	c0 := calls[0].(map[string]any)
	assert.Equal(t, "call_1", c0["id"])
	fn := c0["function"].(map[string]any)
	assert.Equal(t, "get_weather", fn["name"])
	assert.Equal(t, map[string]any{"city": "NYC"}, fn["arguments"],
		"ollama takes arguments as an object, not a string")
	fn1 := calls[1].(map[string]any)["function"].(map[string]any)
	assert.Equal(t, map[string]any{}, fn1["arguments"])

	for i, want := range []struct{ id, content string }{
		{"call_1", "sunny"}, {"call_2", "rainy"},
	} {
		m := got[2+i].(map[string]any)
		assert.Equal(t, "tool", m["role"])
		assert.Equal(t, want.id, m["tool_call_id"])
		assert.Equal(t, "get_weather", m["tool_name"])
		assert.Equal(t, want.content, m["content"])
	}
}

func TestToolLinkage_PlainMessagesUnchanged(t *testing.T) {
	got, err := captureMessages(t, []llm.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
		// A bare tool message was passed through before; it still is.
		{Role: "tool", Content: "legacy"},
	})
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, map[string]any{"role": "assistant", "content": "hello"}, got[1])
	assert.Equal(t, map[string]any{"role": "tool", "content": "legacy"}, got[2])
}

func TestToolLinkage_Rejects(t *testing.T) {
	cases := map[string]llm.Message{
		"tool result with Parts": {Role: "tool", ToolCallID: "c", Parts: []llm.ContentPart{
			{Type: llm.PartTypeText, Text: "x"},
		}},
		"ToolCalls on user":           {Role: "user", ToolCalls: []llm.ToolCall{{ID: "c", Name: "f"}}},
		"ToolCallID on user":          {Role: "user", Content: "x", ToolCallID: "c"},
		"tool call with invalid JSON": {Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c", Name: "f", Arguments: json.RawMessage(`{`)}}},
	}
	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := captureMessages(t, []llm.Message{m})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "ollama:")
		})
	}
}
