package routellm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
)

func sseRouter(t *testing.T, body string, sent *map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sent != nil {
			require.NoError(t, json.NewDecoder(r.Body).Decode(sent))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newRouter(t *testing.T, base string, params map[string]string) llm.Provider {
	t.Helper()
	p, err := New(llm.ResolvedConfig{
		URI:      llm.URI{Scheme: "routellm", Model: "mf:0.7"},
		Provider: llm.ProviderConfig{Model: "mf:0.7", BaseURL: base, Params: params},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	return p
}

// StreamWithTools reaches the inner openai adapter: calls come back
// under their original names, events stream when asked for.
func TestStreamWithTools_ThroughInnerAdapter(t *testing.T) {
	sse := `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"pane_open","arguments":"{}"}}]},"finish_reason":null}]}` + "\n\n" +
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n" +
		`data: {"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}` + "\n\n" +
		"data: [DONE]\n\n"
	var sent map[string]any
	p := newRouter(t, sseRouter(t, sse, &sent).URL, nil)
	ts, ok := p.(llm.ToolStreamer)
	require.True(t, ok, "routellm implements ToolStreamer")
	st, err := ts.StreamWithTools(context.Background(), llm.Request{
		Messages:     []llm.Message{{Role: "user", Content: "Hi"}},
		StreamEvents: true,
	}, []llm.ToolDef{{Name: "pane.open"}})
	require.NoError(t, err)
	defer st.Close()
	var kinds []string
	for {
		tok, err := st.Next()
		require.NoError(t, err)
		if tok.Done {
			break
		}
		switch {
		case tok.ToolCall != nil:
			kinds = append(kinds, string(tok.ToolCall.Kind)+":"+tok.ToolCall.Name)
		case tok.Usage != nil:
			kinds = append(kinds, "usage")
		}
	}
	assert.Equal(t, []string{"start:pane.open", "delta:", "end:pane.open", "usage"}, kinds)
	out, ok := st.Outcome()
	require.True(t, ok)
	require.Len(t, out.ToolCalls, 1)
	assert.Equal(t, "pane.open", out.ToolCalls[0].Name)
	assert.Equal(t, 7, out.Usage.TotalTokens)
	assert.Equal(t, "router-mf-0.7", sent["model"])
	assert.Equal(t, map[string]any{"include_usage": true}, sent["stream_options"])
}

// The size-cap params pass to the inner adapter.
func TestLimitsParamsReachInnerAdapter(t *testing.T) {
	sse := `data: {"choices":[{"index":0,"delta":{"content":"` + strings.Repeat("y", 2048) + `"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"
	p := newRouter(t, sseRouter(t, sse, nil).URL, map[string]string{"max_event_bytes": "1024"})
	it, err := p.(llm.Streamer).Stream(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "Hi"}},
	})
	require.NoError(t, err)
	defer it.Close()
	_, err = it.Next()
	var tooLarge *llmerrors.ErrResponseTooLarge
	require.ErrorAs(t, err, &tooLarge)
	assert.Equal(t, int64(1024), tooLarge.Limit)
	assert.Equal(t, "routellm", tooLarge.Provider)

	_, err = New(llm.ResolvedConfig{
		URI:      llm.URI{Scheme: "routellm", Model: "mf:0.7"},
		Provider: llm.ProviderConfig{Model: "mf:0.7", Params: map[string]string{"max_body_bytes": "0"}},
	})
	assert.EqualError(t, err, "routellm: create inner adapter: routellm: max_body_bytes must be a positive byte count")
}
