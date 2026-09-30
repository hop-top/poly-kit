package anthropic_test

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

// captureMessages runs one CallWithTools round against a mock server
// and returns the "messages" array kit serialized.
func captureMessages(t *testing.T, msgs []llm.Message) ([]any, error) {
	t.Helper()
	var got []any
	_, cfg := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		got, _ = body["messages"].([]any)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(anthropicResponse("done", "end_turn", 1, 1))
	})
	p, err := anthropicFactory(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })

	_, err = p.(llm.ToolCaller).CallWithTools(context.Background(),
		llm.Request{Messages: msgs},
		[]llm.ToolDef{{Name: "get_weather"}},
	)
	return got, err
}

func blocks(t *testing.T, msg any) (string, []map[string]any) {
	t.Helper()
	m := msg.(map[string]any)
	raw := m["content"].([]any)
	out := make([]map[string]any, len(raw))
	for i, b := range raw {
		out[i] = b.(map[string]any)
	}
	return m["role"].(string), out
}

func TestToolLinkage_WireShape(t *testing.T) {
	got, err := captureMessages(t, toolRound())
	require.NoError(t, err)
	require.Len(t, got, 3, "tool results must merge into one user turn")

	role, asst := blocks(t, got[1])
	assert.Equal(t, "assistant", role)
	require.Len(t, asst, 3)
	assert.Equal(t, "text", asst[0]["type"])
	assert.Equal(t, "Checking.", asst[0]["text"])
	for i, want := range []struct{ id, city string }{
		{"call_1", "NYC"}, {"call_2", "LA"},
	} {
		b := asst[1+i]
		assert.Equal(t, "tool_use", b["type"])
		assert.Equal(t, want.id, b["id"])
		assert.Equal(t, "get_weather", b["name"])
		assert.Equal(t, map[string]any{"city": want.city}, b["input"])
	}

	role, res := blocks(t, got[2])
	assert.Equal(t, "user", role, "API accepts only user/assistant roles")
	require.Len(t, res, 2)
	for i, want := range []struct{ id, text string }{
		{"call_1", "sunny"}, {"call_2", "rainy"},
	} {
		b := res[i]
		assert.Equal(t, "tool_result", b["type"])
		assert.Equal(t, want.id, b["tool_use_id"])
		content := b["content"].([]any)
		require.Len(t, content, 1)
		assert.Equal(t, want.text, content[0].(map[string]any)["text"])
	}
}

func TestToolLinkage_EmptyContentAndArguments(t *testing.T) {
	got, err := captureMessages(t, []llm.Message{
		{Role: "user", Content: "ping"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "ping"}}},
		{Role: "tool", ToolCallID: "call_1"},
		{Role: "user", Content: "and now?"},
	})
	require.NoError(t, err)
	require.Len(t, got, 4, "a user turn after results stays its own message")

	_, asst := blocks(t, got[1])
	require.Len(t, asst, 1, "no empty text block beside tool_use")
	assert.Equal(t, "tool_use", asst[0]["type"])
	assert.Equal(t, map[string]any{}, asst[0]["input"])

	_, res := blocks(t, got[2])
	require.Len(t, res, 1)
	assert.Equal(t, "tool_result", res[0]["type"])
	assert.NotContains(t, res[0], "content", "empty result sends no empty text block")
}

func TestToolLinkage_PlainMessagesUnchanged(t *testing.T) {
	got, err := captureMessages(t, []llm.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
		{Role: "user", Content: "bye"},
	})
	require.NoError(t, err)
	require.Len(t, got, 3)
	for i, want := range []string{"user", "assistant", "user"} {
		role, b := blocks(t, got[i])
		assert.Equal(t, want, role)
		require.Len(t, b, 1)
		assert.Equal(t, "text", b[0]["type"])
	}
}

func TestToolLinkage_Rejects(t *testing.T) {
	cases := map[string]llm.Message{
		"tool result without ToolCallID": {Role: "tool", Content: "x"},
		"tool result with Parts": {Role: "tool", ToolCallID: "c", Parts: []llm.ContentPart{
			{Type: llm.PartTypeText, Text: "x"},
		}},
		"ToolCalls on user":           {Role: "user", ToolCalls: []llm.ToolCall{{ID: "c", Name: "f"}}},
		"ToolCallID on user":          {Role: "user", Content: "x", ToolCallID: "c"},
		"tool call without ID":        {Role: "assistant", ToolCalls: []llm.ToolCall{{Name: "f"}}},
		"tool call with invalid JSON": {Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c", Name: "f", Arguments: json.RawMessage(`{`)}}},
	}
	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := captureMessages(t, []llm.Message{m})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "anthropic:")
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
