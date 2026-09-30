package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
)

// toolRound is the second request of an agentic loop: the model asked
// for two tool calls and both results come back.
func toolRound() []llm.Message {
	return []llm.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "Weather in NYC and LA?"},
		{Role: "assistant", Content: "Checking.", ToolCalls: []llm.ToolCall{
			{ID: "call_1", Name: "get_weather", Arguments: json.RawMessage(`{"city":"NYC"}`)},
			{ID: "call_2", Name: "get_weather", Arguments: json.RawMessage(`{"city":"LA"}`)},
		}},
		{Role: "tool", ToolCallID: "call_1", Content: "sunny"},
		{Role: "tool", ToolCallID: "call_2", Content: "rainy"},
	}
}

// captureMessages runs one CallWithTools round against a fake server and
// returns the "messages" array kit serialized.
func captureMessages(t *testing.T, msgs []llm.Message) ([]any, error) {
	t.Helper()
	var got []any
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		got, _ = body["messages"].([]any)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(completionJSON("done", "assistant", "stop")))
	})
	tc := newAdapter(t, "openai", srv.URL, "gpt-4o").(llm.ToolCaller)
	_, err := tc.CallWithTools(context.Background(),
		llm.Request{Messages: msgs},
		[]llm.ToolDef{{Name: "get_weather"}},
	)
	return got, err
}

func TestToolLinkage_WireShape(t *testing.T) {
	got, err := captureMessages(t, toolRound())
	require.NoError(t, err)
	require.Len(t, got, 5)

	asst := got[2].(map[string]any)
	assert.Equal(t, "assistant", asst["role"])
	assert.Equal(t, "Checking.", asst["content"])
	calls, ok := asst["tool_calls"].([]any)
	require.True(t, ok, "assistant message must carry tool_calls: %v", asst)
	require.Len(t, calls, 2)
	c0 := calls[0].(map[string]any)
	assert.Equal(t, "call_1", c0["id"])
	assert.Equal(t, "function", c0["type"])
	fn := c0["function"].(map[string]any)
	assert.Equal(t, "get_weather", fn["name"])
	assert.JSONEq(t, `{"city":"NYC"}`, fn["arguments"].(string))
	assert.Equal(t, "call_2", calls[1].(map[string]any)["id"])

	for i, want := range []struct{ id, content string }{
		{"call_1", "sunny"}, {"call_2", "rainy"},
	} {
		m := got[3+i].(map[string]any)
		assert.Equal(t, "tool", m["role"], "tool result must keep role tool")
		assert.Equal(t, want.id, m["tool_call_id"])
		assert.Equal(t, want.content, m["content"])
	}
}

func TestToolLinkage_EmptyContentAndArguments(t *testing.T) {
	got, err := captureMessages(t, []llm.Message{
		{Role: "user", Content: "ping"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{
			{ID: "call_1", Name: "ping"},
		}},
		{Role: "tool", ToolCallID: "call_1", Content: "pong"},
	})
	require.NoError(t, err)
	require.Len(t, got, 3)

	asst := got[1].(map[string]any)
	_, hasContent := asst["content"]
	assert.False(t, hasContent, "empty content is omitted beside tool_calls")
	fn := asst["tool_calls"].([]any)[0].(map[string]any)["function"].(map[string]any)
	assert.Equal(t, "{}", fn["arguments"])
}

func TestToolLinkage_PlainMessagesUnchanged(t *testing.T) {
	got, err := captureMessages(t, []llm.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	})
	require.NoError(t, err)
	require.Len(t, got, 3)
	for _, m := range got {
		mm := m.(map[string]any)
		assert.NotContains(t, mm, "tool_calls")
		assert.NotContains(t, mm, "tool_call_id")
	}
	assert.Equal(t, "hello", got[2].(map[string]any)["content"])
}

func TestToolLinkage_Rejects(t *testing.T) {
	cases := map[string]llm.Message{
		"tool result without ToolCallID": {Role: "tool", Content: "x"},
		"tool result with Parts": {Role: "tool", ToolCallID: "c", Parts: []llm.ContentPart{
			{Type: llm.PartTypeText, Text: "x"},
		}},
		"ToolCalls on user":            {Role: "user", ToolCalls: []llm.ToolCall{{ID: "c", Name: "f"}}},
		"ToolCallID on user":           {Role: "user", Content: "x", ToolCallID: "c"},
		"tool call without ID":         {Role: "assistant", ToolCalls: []llm.ToolCall{{Name: "f"}}},
		"tool call with invalid JSON":  {Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c", Name: "f", Arguments: json.RawMessage(`{`)}}},
		"assistant ToolCalls in Parts": {Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c", Name: "f"}}, Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: "x"}}},
	}
	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := captureMessages(t, []llm.Message{m})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "openai:")
		})
	}
}

func TestToolLinkage_ProviderDataIgnored(t *testing.T) {
	plain, err := captureMessages(t, toolRound())
	require.NoError(t, err)

	msgs := toolRound()
	for i := range msgs[2].ToolCalls {
		msgs[2].ToolCalls[i].ProviderData = llm.ProviderData{
			"google": json.RawMessage(`{"thought_signature":"SIG_A"}`),
		}
	}
	withData, err := captureMessages(t, msgs)
	require.NoError(t, err)
	assert.Equal(t, plain, withData, "another provider's opaque data never reaches the wire")
}
