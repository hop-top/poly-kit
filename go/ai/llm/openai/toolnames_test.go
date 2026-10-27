package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
)

const namedCallJSON = `{"id":"c","object":"chat.completion","created":1,"model":"m",
	"choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","content":"",
	"tool_calls":[{"id":"call_9","type":"function","function":{"name":"%s","arguments":"{}"}}]}}]}`

// toolNameServer answers every request with a call to wire and records
// the tool and history names each request sent.
func toolNameServer(t *testing.T, wire string, hits *atomic.Int32, tools, history *[]string) string {
	t.Helper()
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		var body struct {
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
			Messages []struct {
				ToolCalls []struct {
					Function struct {
						Name string `json:"name"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"messages"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		for _, tl := range body.Tools {
			*tools = append(*tools, tl.Function.Name)
		}
		for _, m := range body.Messages {
			for _, c := range m.ToolCalls {
				*history = append(*history, c.Function.Name)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(strings.Replace(namedCallJSON, "%s", wire, 1)))
	})
	return srv.URL
}

func history() []llm.Message {
	return []llm.Message{
		{Role: "user", Content: "Hi"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "call_0", Name: "pane.open"}}},
		{Role: "tool", ToolCallID: "call_0", Content: "opened"},
	}
}

func TestToolNames_SanitizedOnWireOriginalBack(t *testing.T) {
	var hits atomic.Int32
	var tools, hist []string
	long := strings.Repeat("a", 65)
	wireLong, err := llm.OpenAIToolNames.Sanitize(long)
	require.NoError(t, err)
	url := toolNameServer(t, wireLong, &hits, &tools, &hist)

	tc := newAdapter(t, "openai", url, "gpt-4o").(llm.ToolCaller)
	defs := []llm.ToolDef{{Name: "pane.open"}, {Name: long}}
	resp, err := tc.CallWithTools(context.Background(), llm.Request{Messages: history()}, defs)
	require.NoError(t, err)

	assert.Equal(t, []string{"pane_open", wireLong}, tools)
	assert.Len(t, wireLong, 64)
	assert.Equal(t, []string{"pane_open"}, hist, "history call names go out as wire names")
	require.Len(t, resp.ToolCalls, 1)
	assert.Equal(t, long, resp.ToolCalls[0].Name, "tool call carries the original name")
	assert.Equal(t, "pane.open", defs[0].Name, "caller's defs are not modified")
}

func TestToolNames_HistoryMappedOnComplete(t *testing.T) {
	var hits atomic.Int32
	var tools, hist []string
	url := toolNameServer(t, "x", &hits, &tools, &hist)
	c := newAdapter(t, "openai", url, "gpt-4o").(llm.Completer)
	_, err := c.Complete(context.Background(), llm.Request{Messages: history()})
	require.NoError(t, err)
	assert.Equal(t, []string{"pane_open"}, hist)
}

func TestToolNames_CollisionFailsBeforeSending(t *testing.T) {
	var hits atomic.Int32
	var tools, hist []string
	url := toolNameServer(t, "x", &hits, &tools, &hist)
	tc := newAdapter(t, "openai", url, "gpt-4o").(llm.ToolCaller)
	_, err := tc.CallWithTools(context.Background(), llm.Request{Messages: history()},
		[]llm.ToolDef{{Name: "pane.open"}, {Name: "pane:open"}})
	var te *llmerrors.ErrToolName
	require.ErrorAs(t, err, &te)
	assert.Equal(t, llmerrors.ToolNameCollision, te.Reason)
	assert.Equal(t, "pane:open", te.Name)
	assert.Equal(t, "pane.open", te.Other)
	assert.Equal(t, "openai", te.Provider)
	assert.Zero(t, hits.Load())
}

func TestToolNames_StrictRejectsBeforeSending(t *testing.T) {
	var hits atomic.Int32
	var tools, hist []string
	url := toolNameServer(t, "x", &hits, &tools, &hist)
	cfg := resolvedCfg("openai", url, "gpt-4o")
	cfg.Provider.Params = map[string]string{"tool_names": "strict"}
	p, err := New(cfg)
	require.NoError(t, err)
	_, err = p.(llm.ToolCaller).CallWithTools(context.Background(), llm.Request{Messages: history()},
		[]llm.ToolDef{{Name: "pane.open"}})
	var te *llmerrors.ErrToolName
	require.ErrorAs(t, err, &te)
	assert.Equal(t, llmerrors.ToolNameInvalid, te.Reason)
	assert.Zero(t, hits.Load())
}

func TestToolNames_UnknownModeRefused(t *testing.T) {
	cfg := resolvedCfg("openai", "http://127.0.0.1:1", "gpt-4o")
	cfg.Provider.Params = map[string]string{"tool_names": "loose"}
	_, err := New(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tool_names")
}
