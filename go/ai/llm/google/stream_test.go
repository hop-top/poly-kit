package google_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// streamServer answers one streamGenerateContent call with chunks and
// hands the request body to got.
func streamServer(t *testing.T, got *map[string]any, chunks ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, ":streamGenerateContent")
		if got != nil {
			require.NoError(t, json.NewDecoder(r.Body).Decode(got))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\r\n\r\n", c)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func drain(t *testing.T, it llm.TokenIterator) ([]llm.Token, error) {
	t.Helper()
	var toks []llm.Token
	for {
		tok, err := it.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return toks, nil
			}
			return toks, err
		}
		toks = append(toks, tok)
	}
}

func TestStreamWithTools_SendsWireNamesAndToolChoice(t *testing.T) {
	var body map[string]any
	srv := streamServer(t, &body,
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"pane_open","args":{}}}]},"finishReason":"STOP"}]}`)
	p := mustNew(t, srv.URL, "gemini-2.5-flash")

	st, err := p.(llm.ToolStreamer).StreamWithTools(context.Background(), llm.Request{
		Messages:   []llm.Message{{Role: "user", Content: "open"}},
		ToolChoice: &llm.ToolChoice{Mode: llm.ToolChoiceTool, Name: "pane/open"},
	}, []llm.ToolDef{{Name: "pane/open", Description: "d"}})
	require.NoError(t, err)
	defer st.Close()
	_, err = drain(t, st)
	require.NoError(t, err)

	decls := body["tools"].([]any)[0].(map[string]any)["functionDeclarations"].([]any)
	assert.Equal(t, "pane_open", decls[0].(map[string]any)["name"])
	fc := body["toolConfig"].(map[string]any)["functionCallingConfig"].(map[string]any)
	assert.Equal(t, []any{"pane_open"}, fc["allowedFunctionNames"])

	out, ok := st.Outcome()
	require.True(t, ok)
	require.Len(t, out.ToolCalls, 1)
	assert.Equal(t, "pane/open", out.ToolCalls[0].Name)
	assert.True(t, strings.HasPrefix(out.ToolCalls[0].ID, "call_"), "an ID is synthesized: %q", out.ToolCalls[0].ID)
}

func TestStreamWithTools_ThoughtSignatureRoundTrips(t *testing.T) {
	srv := streamServer(t, nil,
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"fc_1","name":"get_weather","args":{"city":"LA"}},"thoughtSignature":"c2ln"}]},"finishReason":"STOP"}]}`)
	p := mustNew(t, srv.URL, "gemini-2.5-flash")
	st, err := p.(llm.ToolStreamer).StreamWithTools(context.Background(), llm.Request{
		Messages:     []llm.Message{{Role: "user", Content: "weather"}},
		StreamEvents: true,
	}, []llm.ToolDef{{Name: "get_weather"}})
	require.NoError(t, err)
	defer st.Close()
	toks, err := drain(t, st)
	require.NoError(t, err)

	out, ok := st.Outcome()
	require.True(t, ok)
	require.Len(t, out.ToolCalls, 1)
	assert.JSONEq(t, `{"thought_signature":"c2ln"}`, string(out.ToolCalls[0].ProviderData["google"]))
	var end *llm.ToolCallEvent
	for _, tok := range toks {
		if tok.ToolCall != nil && tok.ToolCall.Kind == llm.ToolCallEnd {
			end = tok.ToolCall
		}
	}
	require.NotNil(t, end)
	assert.Equal(t, out.ToolCalls[0], *end.Call)

	// Replayed, the signature goes back on its functionCall part.
	got, err := captureContents(t, []llm.Message{
		{Role: "user", Content: "weather"},
		{Role: "assistant", ToolCalls: out.ToolCalls},
		{Role: "tool", ToolCallID: "fc_1", Content: "sunny"},
	})
	require.NoError(t, err)
	_, ps := parts(t, got[1])
	assert.Equal(t, "c2ln", ps[0]["thoughtSignature"])
}

func TestStreamWithTools_NoOutcomeAfterClose(t *testing.T) {
	srv := streamServer(t, nil,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"a"}]}}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"b"}]},"finishReason":"STOP"}]}`)
	p := mustNew(t, srv.URL, "gemini-2.5-flash")
	st, err := p.(llm.ToolStreamer).StreamWithTools(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	}, []llm.ToolDef{{Name: "get_weather"}})
	require.NoError(t, err)
	tok, err := st.Next()
	require.NoError(t, err)
	assert.Equal(t, "a", tok.Content)
	require.NoError(t, st.Close())
	_, err = st.Next()
	assert.ErrorIs(t, err, io.EOF)
	_, ok := st.Outcome()
	assert.False(t, ok)
}

func TestStreamWithTools_CanceledContext(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"a\"}]}}]}\r\n\r\n")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithCancel(context.Background())
	p := mustNew(t, srv.URL, "gemini-2.5-flash")
	st, err := p.(llm.ToolStreamer).StreamWithTools(ctx, llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	}, []llm.ToolDef{{Name: "get_weather"}})
	require.NoError(t, err)
	defer st.Close()
	tok, err := st.Next()
	require.NoError(t, err)
	assert.Equal(t, "a", tok.Content)
	cancel()
	_, err = st.Next()
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	_, ok := st.Outcome()
	assert.False(t, ok)
}

func TestStreamWithTools_BadChunk(t *testing.T) {
	srv := streamServer(t, nil, `{not json`)
	p := mustNew(t, srv.URL, "gemini-2.5-flash")
	st, err := p.(llm.ToolStreamer).StreamWithTools(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	}, nil)
	require.NoError(t, err)
	defer st.Close()
	_, err = drain(t, st)
	require.ErrorContains(t, err, "gemini: decode stream chunk")
	_, ok := st.Outcome()
	assert.False(t, ok)
}

func TestStreamWithTools_StatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"code":403,"message":"denied","status":"PERMISSION_DENIED"}}`)
	}))
	t.Cleanup(srv.Close)
	p := mustNew(t, srv.URL, "gemini-2.5-flash")
	_, err := p.(llm.ToolStreamer).StreamWithTools(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	}, nil)
	assert.True(t, errors.As(err, new(*llmerrors.ErrPermissionDenied)), "got %v", err)
}

// Not opted in, Stream keeps its old termination: a body without a
// finish reason still ends with Done.
func TestStream_NotOptedInUnchanged(t *testing.T) {
	srv := streamServer(t, nil,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"a"}]}}],"usageMetadata":{"promptTokenCount":1,"totalTokenCount":1}}`)
	p := mustNew(t, srv.URL, "gemini-2.5-flash")
	it, err := p.(llm.Streamer).Stream(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)
	defer it.Close()
	toks, err := drain(t, it)
	require.NoError(t, err)
	assert.Equal(t, []llm.Token{{Content: "a"}, {Done: true}}, toks)
}
