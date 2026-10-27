package google_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
)

// Runs the gemini_* sections of contracts/llm-v1. The vectors are the
// source of truth: a failure means the adapter drifted from the
// contract, not that a vector should be edited to fit.

const geminiConfPath = "../../../../contracts/llm-v1/vectors.json"

type geminiConfFile struct {
	GeminiStreamEvents []struct {
		Name         string            `json:"name"`
		SSE          string            `json:"sse"`
		Tools        []string          `json:"tools"`
		StreamEvents bool              `json:"stream_events"`
		WantEvents   []json.RawMessage `json:"want_events"`
		WantOutcome  json.RawMessage   `json:"want_outcome"`
		ErrorKind    string            `json:"error_kind"`
	} `json:"gemini_stream_events"`
}

func loadGeminiConf(t *testing.T) geminiConfFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(geminiConfPath))
	require.NoError(t, err)
	var all map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &all))
	require.JSONEq(t, `"v1"`, string(all["version"]))
	// Only this adapter's sections are decoded, strictly; the others
	// belong to the core and anthropic runners.
	var f geminiConfFile
	for k, v := range all {
		if !strings.HasPrefix(k, "gemini_") {
			continue
		}
		section, err := json.Marshal(map[string]json.RawMessage{k: v})
		require.NoError(t, err)
		dec := json.NewDecoder(bytes.NewReader(section))
		dec.DisallowUnknownFields()
		require.NoError(t, dec.Decode(&f), k)
	}
	return f
}

func geminiSSEServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, ":streamGenerateContent")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func confUsageJSON(u llm.Usage) map[string]any {
	return map[string]any{
		"prompt_tokens": u.PromptTokens, "completion_tokens": u.CompletionTokens,
		"cache_read_tokens": u.CacheReadTokens, "cache_creation_tokens": u.CacheCreationTokens,
		"total_tokens": u.TotalTokens,
	}
}

func argsJSON(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	if len(raw) == 0 {
		return nil
	}
	var v any
	require.NoError(t, json.Unmarshal(raw, &v))
	return v
}

func callJSON(t *testing.T, c llm.ToolCall) map[string]any {
	t.Helper()
	return map[string]any{"id": c.ID, "name": c.Name, "arguments": argsJSON(t, c.Arguments)}
}

// eventJSON spells one streamed token as the vectors do.
func eventJSON(t *testing.T, tok llm.Token) map[string]any {
	t.Helper()
	switch {
	case tok.Done:
		assert.Empty(t, tok.Content, "done carries no text")
		return map[string]any{"type": "done"}
	case tok.ToolCall != nil:
		ev := tok.ToolCall
		out := map[string]any{"type": "tool_call", "kind": string(ev.Kind), "index": ev.Index}
		switch ev.Kind {
		case llm.ToolCallStart:
			out["id"], out["name"] = ev.ID, ev.Name
		case llm.ToolCallDelta:
			out["arguments"] = ev.ArgumentsDelta
		case llm.ToolCallEnd:
			require.NotNil(t, ev.Call)
			for k, v := range callJSON(t, *ev.Call) {
				out[k] = v
			}
			assert.Equal(t, ev.Call.ID, ev.ID, "end repeats the id")
			assert.Equal(t, ev.Call.Name, ev.Name, "end repeats the name")
		}
		return out
	case tok.Usage != nil:
		return map[string]any{"type": "usage", "usage": confUsageJSON(*tok.Usage)}
	}
	return map[string]any{"type": "text", "text": tok.Content}
}

func confErrorKind(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.As(err, new(*llmerrors.ErrStreamTruncated)):
		return "truncated"
	case errors.As(err, new(*llmerrors.ErrPermissionDenied)):
		return "permission_denied"
	case errors.As(err, new(*llmerrors.ErrAuth)):
		return "auth"
	case errors.As(err, new(*llmerrors.ErrRateLimit)):
		return "rate_limit"
	case errors.As(err, new(*llmerrors.ErrModel)):
		return "model"
	case errors.As(err, new(*llmerrors.HTTPStatusError)):
		return "http_status"
	}
	return "other"
}

func TestConformanceGeminiStreamEvents(t *testing.T) {
	f := loadGeminiConf(t)
	require.NotEmpty(t, f.GeminiStreamEvents)
	for _, v := range f.GeminiStreamEvents {
		t.Run(v.Name, func(t *testing.T) {
			p := mustNew(t, geminiSSEServer(t, v.SSE).URL, "m")
			req := llm.Request{
				Messages:     []llm.Message{{Role: "user", Content: "Hi"}},
				StreamEvents: v.StreamEvents,
			}
			var it llm.TokenIterator
			var st llm.ToolStream
			var err error
			if len(v.Tools) > 0 {
				var tools []llm.ToolDef
				for _, n := range v.Tools {
					tools = append(tools, llm.ToolDef{Name: n})
				}
				st, err = p.(llm.ToolStreamer).StreamWithTools(context.Background(), req, tools)
				it = st
			} else {
				it, err = p.(llm.Streamer).Stream(context.Background(), req)
			}
			require.NoError(t, err)
			defer it.Close()

			got := []any{}
			var ends []*llm.ToolCallEvent
			var last *llm.Usage
			var streamErr error
			for {
				tok, err := it.Next()
				if err != nil {
					if !errors.Is(err, io.EOF) {
						streamErr = err
					}
					break
				}
				got = append(got, eventJSON(t, tok))
				if tok.ToolCall != nil && tok.ToolCall.Kind == llm.ToolCallEnd {
					ends = append(ends, tok.ToolCall)
				}
				if tok.Usage != nil {
					last = tok.Usage
				}
				if tok.Done {
					_, err := it.Next()
					assert.ErrorIs(t, err, io.EOF, "Next after Done is EOF")
					break
				}
			}
			want, err := json.Marshal(v.WantEvents)
			require.NoError(t, err)
			gotJSON, err := json.Marshal(got)
			require.NoError(t, err)
			assert.JSONEq(t, string(want), string(gotJSON))
			assert.Equal(t, v.ErrorKind, confErrorKind(streamErr), "error: %v", streamErr)

			if st == nil {
				require.Empty(t, v.WantOutcome, "want_outcome needs tools")
				return
			}
			out, ok := st.Outcome()
			if streamErr != nil {
				assert.False(t, ok, "no outcome after an error")
				return
			}
			require.True(t, ok)
			calls := []any{}
			for _, c := range out.ToolCalls {
				calls = append(calls, callJSON(t, c))
			}
			gotOut, err := json.Marshal(map[string]any{
				"content": out.Content, "tool_calls": calls,
				"finish_reason": out.FinishReason, "usage": confUsageJSON(out.Usage),
			})
			require.NoError(t, err)
			assert.JSONEq(t, string(v.WantOutcome), string(gotOut))
			assert.Empty(t, out.Thinking)
			// The events and the outcome tell the same story.
			if v.StreamEvents {
				require.Len(t, out.ToolCalls, len(ends))
				for _, ev := range ends {
					assert.Equal(t, out.ToolCalls[ev.Index], *ev.Call, "end call is Outcome.ToolCalls[index]")
				}
				if last != nil {
					assert.Equal(t, out.Usage, *last, "the last usage snapshot is Outcome.Usage")
				}
			}
		})
	}
}
