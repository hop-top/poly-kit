package google_test

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
		{Role: "tool", ToolCallID: "call_2", Content: `{"temp":12}`},
	}
}

// captureContents runs one CallWithTools round against a fake server and
// returns the "contents" array kit serialized.
func captureContents(t *testing.T, msgs []llm.Message) ([]any, error) {
	t.Helper()
	var got []any
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			got, _ = body["contents"].([]any)
			writeJSON(w, geminiResponse("done", "STOP", 1, 1))
		},
	))
	t.Cleanup(srv.Close)

	tc := mustNew(t, srv.URL, "gemini-2.0-flash").(llm.ToolCaller)
	_, err := tc.CallWithTools(context.Background(),
		llm.Request{Messages: msgs},
		[]llm.ToolDef{{Name: "get_weather"}},
	)
	return got, err
}

func parts(t *testing.T, c any) (string, []map[string]any) {
	t.Helper()
	m := c.(map[string]any)
	raw := m["parts"].([]any)
	out := make([]map[string]any, len(raw))
	for i, p := range raw {
		out[i] = p.(map[string]any)
	}
	return m["role"].(string), out
}

func TestToolLinkage_WireShape(t *testing.T) {
	got, err := captureContents(t, toolRound())
	require.NoError(t, err)
	require.Len(t, got, 3, "tool results must merge into one turn")

	role, model := parts(t, got[1])
	assert.Equal(t, "model", role)
	require.Len(t, model, 3)
	assert.Equal(t, "Checking.", model[0]["text"])
	for i, city := range []string{"NYC", "LA"} {
		fc, ok := model[1+i]["functionCall"].(map[string]any)
		require.True(t, ok, "model turn must carry functionCall parts: %v", model)
		assert.Equal(t, "get_weather", fc["name"])
		assert.Equal(t, map[string]any{"city": city}, fc["args"])
	}

	role, res := parts(t, got[2])
	assert.Equal(t, "user", role)
	require.Len(t, res, 2)
	fr0, ok := res[0]["functionResponse"].(map[string]any)
	require.True(t, ok, "tool result must be a functionResponse part: %v", res)
	assert.Equal(t, "get_weather", fr0["name"], "name resolved from the linked call")
	assert.Equal(t, map[string]any{"output": "sunny"}, fr0["response"])
	fr1 := res[1]["functionResponse"].(map[string]any)
	assert.Equal(t, map[string]any{"temp": float64(12)}, fr1["response"],
		"a JSON-object result passes through as the response struct")
}

func TestToolLinkage_EmptyArguments(t *testing.T) {
	got, err := captureContents(t, []llm.Message{
		{Role: "user", Content: "ping"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "ping"}}},
		{Role: "tool", ToolCallID: "c1", Content: "pong"},
	})
	require.NoError(t, err)
	_, model := parts(t, got[1])
	require.Len(t, model, 1, "no empty text part beside functionCall")
	fc := model[0]["functionCall"].(map[string]any)
	assert.Equal(t, map[string]any{}, fc["args"])
}

func TestToolLinkage_PlainMessagesUnchanged(t *testing.T) {
	got, err := captureContents(t, []llm.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	})
	require.NoError(t, err)
	require.Len(t, got, 2)
	role, p := parts(t, got[1])
	assert.Equal(t, "model", role)
	assert.Equal(t, []map[string]any{{"text": "hello"}}, p)
}

func TestToolLinkage_Rejects(t *testing.T) {
	call := llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c", Name: "f"}}}
	cases := map[string][]llm.Message{
		"tool result without ToolCallID": {call, {Role: "tool", Content: "x"}},
		"tool result for unknown call":   {call, {Role: "tool", ToolCallID: "nope", Content: "x"}},
		"tool result with Parts": {call, {Role: "tool", ToolCallID: "c", Parts: []llm.ContentPart{
			{Type: llm.PartTypeText, Text: "x"},
		}}},
		"ToolCalls on user":           {{Role: "user", ToolCalls: []llm.ToolCall{{ID: "c", Name: "f"}}}},
		"ToolCallID on user":          {{Role: "user", Content: "x", ToolCallID: "c"}},
		"tool call with invalid JSON": {{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c", Name: "f", Arguments: json.RawMessage(`{`)}}}},
	}
	for name, msgs := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := captureContents(t, msgs)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "gemini:")
		})
	}
}

func TestToolLinkage_ResponseIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, map[string]any{
				"candidates": []map[string]any{{
					"content": map[string]any{
						"role": "model",
						"parts": []map[string]any{
							{"functionCall": map[string]any{
								"id": "fc-1", "name": "a", "args": map[string]any{},
							}},
							{"functionCall": map[string]any{"name": "b", "args": map[string]any{}}},
							{"functionCall": map[string]any{"name": "b", "args": map[string]any{}}},
						},
					},
					"finishReason": "STOP",
				}},
			})
		},
	))
	t.Cleanup(srv.Close)

	tc := mustNew(t, srv.URL, "gemini-2.0-flash").(llm.ToolCaller)
	resp, err := tc.CallWithTools(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "go"}},
	}, []llm.ToolDef{{Name: "a"}, {Name: "b"}})
	require.NoError(t, err)
	require.Len(t, resp.ToolCalls, 3)
	assert.Equal(t, "fc-1", resp.ToolCalls[0].ID, "a provider ID is kept")
	assert.NotEmpty(t, resp.ToolCalls[1].ID, "a missing ID is synthesized")
	assert.NotEmpty(t, resp.ToolCalls[2].ID)
	assert.NotEqual(t, resp.ToolCalls[1].ID, resp.ToolCalls[2].ID)
}
