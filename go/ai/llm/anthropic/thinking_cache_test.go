package anthropic_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
)

// captureBody sends req (with tools when non-nil) and returns the JSON
// body the adapter sent.
func captureBody(t *testing.T, req llm.Request, tools []llm.ToolDef, reply []byte) (map[string]any, any, error) {
	t.Helper()
	var body map[string]any
	_, cfg := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(reply)
	})
	p, err := anthropicFactory(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	if tools != nil {
		resp, err := p.(llm.ToolCaller).CallWithTools(context.Background(), req, tools)
		return body, resp, err
	}
	resp, err := p.(llm.Completer).Complete(context.Background(), req)
	return body, resp, err
}

func TestThinking_RequestAndReplay(t *testing.T) {
	req := llm.Request{
		MaxTokens: 4096,
		Thinking:  &llm.ThinkingConfig{BudgetTokens: 2048},
		Messages: []llm.Message{
			{Role: "user", Content: "Weather?"},
			{Role: "assistant", Thinking: []llm.ThinkingBlock{
				{Thinking: "need a tool", Signature: "sig-1"},
				{Redacted: "opaque"},
			}, ToolCalls: []llm.ToolCall{{ID: "t1", Name: "get_weather", Arguments: json.RawMessage(`{}`)}}},
			{Role: "tool", ToolCallID: "t1", Content: "sunny"},
		},
	}
	body, _, err := captureBody(t, req, []llm.ToolDef{{Name: "get_weather"}}, anthropicResponse("ok", "end_turn", 1, 1))
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"type": "enabled", "budget_tokens": float64(2048)}, body["thinking"])
	msgs := body["messages"].([]any)
	asst := msgs[1].(map[string]any)["content"].([]any)
	require.Len(t, asst, 3)
	assert.Equal(t, map[string]any{"type": "thinking", "thinking": "need a tool", "signature": "sig-1"}, asst[0])
	assert.Equal(t, map[string]any{"type": "redacted_thinking", "data": "opaque"}, asst[1])
	assert.Equal(t, "tool_use", asst[2].(map[string]any)["type"])
}

func TestThinking_Validation(t *testing.T) {
	cases := map[string]struct {
		req  llm.Request
		want string
	}{
		"budget not below max_tokens": {
			llm.Request{MaxTokens: 2048, Thinking: &llm.ThinkingConfig{BudgetTokens: 2048}},
			"anthropic: thinking budget_tokens (2048) must be less than max_tokens (2048)",
		},
		"budget under the minimum": {
			llm.Request{MaxTokens: 4096, Thinking: &llm.ThinkingConfig{BudgetTokens: 512}},
			"anthropic: thinking budget_tokens must be at least 1024, got 512",
		},
		"default max_tokens": {
			llm.Request{Thinking: &llm.ThinkingConfig{BudgetTokens: 1024}},
			"anthropic: thinking budget_tokens (1024) must be less than max_tokens (1024)",
		},
		"thinking on a user turn": {
			llm.Request{Messages: []llm.Message{{Role: "user", Content: "x", Thinking: []llm.ThinkingBlock{{Thinking: "t"}}}}},
			`anthropic: Thinking is valid only on role assistant, got "user"`,
		},
		"bad cache ttl": {
			llm.Request{Messages: []llm.Message{{Role: "user", Content: "x", CacheControl: &llm.CacheControl{TTL: "2h"}}}},
			`anthropic: cache_control ttl must be "5m" or "1h", got "2h"`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if len(tc.req.Messages) == 0 {
				tc.req.Messages = []llm.Message{{Role: "user", Content: "Hi"}}
			}
			_, _, err := captureBody(t, tc.req, nil, anthropicResponse("ok", "end_turn", 1, 1))
			require.Error(t, err)
			assert.Equal(t, tc.want, err.Error())
		})
	}
}

func TestCacheControl_RequestShape(t *testing.T) {
	req := llm.Request{Messages: []llm.Message{
		{Role: "system", Content: "long system", CacheControl: &llm.CacheControl{}},
		{Role: "user", Content: "context", CacheControl: &llm.CacheControl{TTL: "1h"}},
		{Role: "user", Content: "question"},
	}}
	tools := []llm.ToolDef{{Name: "a"}, {Name: "b", CacheControl: &llm.CacheControl{TTL: "5m"}}}
	body, _, err := captureBody(t, req, tools, anthropicResponse("ok", "end_turn", 1, 1))
	require.NoError(t, err)
	sys := body["system"].([]any)[0].(map[string]any)
	assert.Equal(t, map[string]any{"type": "ephemeral"}, sys["cache_control"])
	msgs := body["messages"].([]any)
	first := msgs[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	assert.Equal(t, map[string]any{"type": "ephemeral", "ttl": "1h"}, first["cache_control"])
	second := msgs[1].(map[string]any)["content"].([]any)[0].(map[string]any)
	assert.NotContains(t, second, "cache_control")
	tl := body["tools"].([]any)
	assert.NotContains(t, tl[0].(map[string]any), "cache_control")
	assert.Equal(t, map[string]any{"type": "ephemeral", "ttl": "5m"}, tl[1].(map[string]any)["cache_control"])
}

func TestResponse_ThinkingAndCacheUsage(t *testing.T) {
	reply := []byte(`{"id":"m","type":"message","role":"assistant","model":"m",
		"content":[{"type":"thinking","thinking":"hmm","signature":"s1"},{"type":"redacted_thinking","data":"d1"},
		{"type":"text","text":"Hi"},{"type":"tool_use","id":"t1","name":"f","input":{}}],
		"stop_reason":"tool_use",
		"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":100,"cache_creation_input_tokens":20}}`)
	_, resp, err := captureBody(t, llm.Request{Messages: []llm.Message{{Role: "user", Content: "Hi"}}},
		[]llm.ToolDef{{Name: "f"}}, reply)
	require.NoError(t, err)
	tr := resp.(llm.ToolResponse)
	assert.Equal(t, []llm.ThinkingBlock{{Thinking: "hmm", Signature: "s1"}, {Redacted: "d1"}}, tr.Thinking)
	assert.Equal(t, "tool_use", tr.FinishReason)
	assert.Equal(t, llm.Usage{PromptTokens: 10, CompletionTokens: 5, CacheReadTokens: 100, CacheCreationTokens: 20, TotalTokens: 135}, tr.Usage)

	_, resp, err = captureBody(t, llm.Request{Messages: []llm.Message{{Role: "user", Content: "Hi"}}}, nil, reply)
	require.NoError(t, err)
	r := resp.(llm.Response)
	assert.Len(t, r.Thinking, 2)
	assert.Equal(t, 100, r.Usage.CacheReadTokens)
}

func TestUsageCost(t *testing.T) {
	u := llm.Usage{PromptTokens: 1_000_000, CompletionTokens: 500_000, CacheReadTokens: 2_000_000, CacheCreationTokens: 100_000}
	got := u.Cost(llm.Price{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75})
	assert.InDelta(t, 3+7.5+0.6+0.375, got, 1e-9)
}
