package google_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
	"hop.top/kit/go/ai/llm/google"
)

type sentNames struct {
	decls, calls, results []string
}

// toolNameServer answers with a functionCall named wire and records the
// declaration, history call and functionResponse names sent.
func toolNameServer(t *testing.T, wire string, hits *atomic.Int32, got *sentNames) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		var body struct {
			Tools []struct {
				FunctionDeclarations []struct {
					Name string `json:"name"`
				} `json:"functionDeclarations"`
			} `json:"tools"`
			Contents []struct {
				Parts []struct {
					FunctionCall *struct {
						Name string `json:"name"`
					} `json:"functionCall"`
					FunctionResponse *struct {
						Name string `json:"name"`
					} `json:"functionResponse"`
				} `json:"parts"`
			} `json:"contents"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		for _, tl := range body.Tools {
			for _, d := range tl.FunctionDeclarations {
				got.decls = append(got.decls, d.Name)
			}
		}
		for _, c := range body.Contents {
			for _, p := range c.Parts {
				if p.FunctionCall != nil {
					got.calls = append(got.calls, p.FunctionCall.Name)
				}
				if p.FunctionResponse != nil {
					got.results = append(got.results, p.FunctionResponse.Name)
				}
			}
		}
		writeJSON(w, map[string]any{"candidates": []map[string]any{{
			"content": map[string]any{"role": "model", "parts": []map[string]any{
				{"functionCall": map[string]any{"id": "fc_1", "name": wire, "args": map[string]any{}}},
			}},
			"finishReason": "STOP",
		}}})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func toolHistory(name string) []llm.Message {
	return []llm.Message{
		{Role: "user", Content: "Hi"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "fc_0", Name: name}}},
		{Role: "tool", ToolCallID: "fc_0", Content: "done"},
	}
}

func TestToolNames_GeminiKeepsDotsSanitizesTheRest(t *testing.T) {
	var hits atomic.Int32
	var got sentNames
	url := toolNameServer(t, "ns_tool", &hits, &got)
	tc := mustNew(t, url, "gemini-2.0-flash").(llm.ToolCaller)
	resp, err := tc.CallWithTools(context.Background(), llm.Request{Messages: toolHistory("ns/tool")},
		[]llm.ToolDef{{Name: "pane.open"}, {Name: "ns/tool"}, {Name: "1st"}})
	require.NoError(t, err)

	assert.Equal(t, []string{"pane.open", "ns_tool", "_1st"}, got.decls)
	assert.Equal(t, []string{"ns_tool"}, got.calls, "history functionCall goes out as the wire name")
	assert.Equal(t, []string{"ns_tool"}, got.results, "functionResponse names the wire name")
	require.Len(t, resp.ToolCalls, 1)
	assert.Equal(t, "ns/tool", resp.ToolCalls[0].Name)
}

func TestToolNames_GeminiCollisionFailsBeforeSending(t *testing.T) {
	var hits atomic.Int32
	var got sentNames
	url := toolNameServer(t, "x", &hits, &got)
	tc := mustNew(t, url, "gemini-2.0-flash").(llm.ToolCaller)
	_, err := tc.CallWithTools(context.Background(), llm.Request{Messages: toolHistory("a")},
		[]llm.ToolDef{{Name: "a/b"}, {Name: "a b"}})
	var te *llmerrors.ErrToolName
	require.ErrorAs(t, err, &te)
	assert.Equal(t, llmerrors.ToolNameCollision, te.Reason)
	assert.Equal(t, "gemini", te.Provider)
	assert.Zero(t, hits.Load())
}

func TestToolNames_GeminiStrict(t *testing.T) {
	var hits atomic.Int32
	var got sentNames
	url := toolNameServer(t, "x", &hits, &got)
	p, err := google.New(llm.ResolvedConfig{Provider: llm.ProviderConfig{
		BaseURL: url, Model: "gemini-2.0-flash", APIKey: "test-key",
		Params: map[string]string{"tool_names": "strict"},
	}})
	require.NoError(t, err)
	_, err = p.(llm.ToolCaller).CallWithTools(context.Background(), llm.Request{Messages: toolHistory("a")},
		[]llm.ToolDef{{Name: "ns/tool"}})
	var te *llmerrors.ErrToolName
	require.ErrorAs(t, err, &te)
	assert.Equal(t, llmerrors.ToolNameInvalid, te.Reason)
	assert.Zero(t, hits.Load())
}

func TestToolNames_GeminiHistoryMappedOnComplete(t *testing.T) {
	var hits atomic.Int32
	var got sentNames
	url := toolNameServer(t, "x", &hits, &got)
	c := mustNew(t, url, "gemini-2.0-flash").(llm.Completer)
	_, err := c.Complete(context.Background(), llm.Request{Messages: toolHistory("ns/tool")})
	require.NoError(t, err)
	assert.Equal(t, []string{"ns_tool"}, got.calls)
	assert.Equal(t, []string{"ns_tool"}, got.results)
}
