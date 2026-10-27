package openai_test

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
	"sync/atomic"
	"testing"
	"time"

	oai "github.com/openai/openai-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
	"hop.top/kit/go/ai/llm/openai"
)

// Runs the openai_* sections of contracts/llm-v1 against the kit
// adapter. The vectors are the source of truth; the other sections run
// in go/ai/llm and go/ai/llm/anthropic.

const openaiConfPath = "../../../../contracts/llm-v1/vectors.json"

type confRequest struct {
	Model         string        `json:"model"`
	Temperature   *float64      `json:"temperature"`
	MaxTokens     int           `json:"max_tokens"`
	StopSequences []string      `json:"stop_sequences"`
	Messages      []confMessage `json:"messages"`
	ToolChoice    *struct {
		Mode string `json:"mode"`
		Name string `json:"name"`
	} `json:"tool_choice"`
	StreamEvents bool `json:"stream_events"`
}

type confMessage struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	ToolCalls []struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"tool_calls"`
	ToolCallID string `json:"tool_call_id"`
}

type confTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  string `json:"parameters"`
}

type confResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

type confUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type openaiConfFile struct {
	OpenAIRequest []struct {
		Name         string          `json:"name"`
		Call         string          `json:"call"`
		AdapterModel string          `json:"adapter_model"`
		Request      confRequest     `json:"request"`
		Tools        []confTool      `json:"tools"`
		WantBody     json.RawMessage `json:"want_body"`
	} `json:"openai_request"`
	OpenAIStream []struct {
		Name       string   `json:"name"`
		SSE        string   `json:"sse"`
		WantTokens []string `json:"want_tokens"`
		ErrorKind  string   `json:"error_kind"`
	} `json:"openai_stream"`
	OpenAIStreamEvents []struct {
		Name         string            `json:"name"`
		SSE          string            `json:"sse"`
		SSEFrom      string            `json:"sse_from"`
		Tools        []string          `json:"tools"`
		StreamEvents bool              `json:"stream_events"`
		WantEvents   []json.RawMessage `json:"want_events"`
		ErrorKind    string            `json:"error_kind"`
	} `json:"openai_stream_events"`
	OpenAIAccumulate []struct {
		Name    string   `json:"name"`
		SSE     string   `json:"sse"`
		SSEFrom string   `json:"sse_from"`
		Tools   []string `json:"tools"`
		Want    struct {
			Text      string `json:"text"`
			ToolCalls []struct {
				ID        string          `json:"id"`
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"tool_calls"`
			FinishReason string    `json:"finish_reason"`
			Usage        confUsage `json:"usage"`
			Error        bool      `json:"error"`
		} `json:"want"`
	} `json:"openai_accumulate"`
	OpenAIError []struct {
		Name         string         `json:"name"`
		Responses    []confResponse `json:"responses"`
		WantKind     string         `json:"want_kind"`
		WantStatus   int            `json:"want_status"`
		Fallbackable bool           `json:"fallbackable"`
		Attempts     int            `json:"attempts"`
		Stream       bool           `json:"stream"`
	} `json:"openai_error"`
	OpenAILimits []struct {
		Name        string            `json:"name"`
		Params      map[string]string `json:"params"`
		Stream      bool              `json:"stream"`
		ContentType string            `json:"content_type"`
		Body        string            `json:"body"`
		TooLarge    bool              `json:"too_large"`
	} `json:"openai_limits"`
}

// loadOpenAIConf decodes the openai_* sections strictly: each vector's
// fields must all be known. Other sections are left to their runners.
func loadOpenAIConf(t *testing.T) openaiConfFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(openaiConfPath))
	require.NoError(t, err)
	var all map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &all))
	var version string
	require.NoError(t, json.Unmarshal(all["version"], &version))
	require.Equal(t, "v1", version)
	only := map[string]json.RawMessage{}
	for k, v := range all {
		if strings.HasPrefix(k, "openai_") {
			only[k] = v
		}
	}
	b, err := json.Marshal(only)
	require.NoError(t, err)
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var f openaiConfFile
	require.NoError(t, dec.Decode(&f))
	return f
}

func (r confRequest) toLLM() llm.Request {
	out := llm.Request{
		Model:         r.Model,
		Temperature:   r.Temperature,
		MaxTokens:     r.MaxTokens,
		StopSequences: r.StopSequences,
		StreamEvents:  r.StreamEvents,
	}
	if c := r.ToolChoice; c != nil {
		out.ToolChoice = &llm.ToolChoice{Mode: llm.ToolChoiceMode(c.Mode), Name: c.Name}
	}
	for _, m := range r.Messages {
		msg := llm.Message{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			call := llm.ToolCall{ID: tc.ID, Name: tc.Name}
			if tc.Arguments != "" {
				call.Arguments = json.RawMessage(tc.Arguments)
			}
			msg.ToolCalls = append(msg.ToolCalls, call)
		}
		out.Messages = append(out.Messages, msg)
	}
	return out
}

func toolDefs(tools []confTool) []llm.ToolDef {
	var out []llm.ToolDef
	for _, d := range tools {
		def := llm.ToolDef{Name: d.Name, Description: d.Description}
		if d.Parameters != "" {
			def.Parameters = json.RawMessage(d.Parameters)
		}
		out = append(out, def)
	}
	return out
}

func namedTools(names []string) []llm.ToolDef {
	var out []llm.ToolDef
	for _, n := range names {
		out = append(out, llm.ToolDef{Name: n})
	}
	return out
}

// confKey is the key the vectors run with; no error text may hold it.
const confKey = "conf-key-must-not-leak"

func newConfAdapter(t *testing.T, base, model string, params map[string]string) llm.Provider {
	t.Helper()
	p, err := openai.New(llm.ResolvedConfig{
		URI:      llm.URI{Scheme: "openai", Model: model},
		Provider: llm.ProviderConfig{APIKey: confKey, BaseURL: base, Model: model, Params: params},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	return p
}

const okCompletion = `{"id":"c","object":"chat.completion","created":1,"model":"m",` +
	`"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],` +
	`"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`

const okSSE = "data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\"," +
	"\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"

func sseServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// errorKind names the llm error class err maps to, as the vectors do.
func errorKind(err error) (kind string, status int) {
	var auth *llmerrors.ErrAuth
	var hs *llmerrors.HTTPStatusError
	var api *oai.Error
	switch {
	case err == nil:
		return "", 0
	case errors.As(err, new(*llmerrors.ErrStreamTruncated)):
		return "truncated", 0
	case errors.As(err, new(*llmerrors.ErrResponseTooLarge)):
		return "too_large", 0
	case errors.As(err, new(*llmerrors.ErrPermissionDenied)):
		if !errors.As(err, &auth) {
			return "permission_denied without auth", 0
		}
		return "permission_denied", 0
	case errors.As(err, &auth):
		return "auth", 0
	case errors.As(err, new(*llmerrors.ErrRateLimit)):
		return "rate_limit", 0
	case errors.As(err, new(*llmerrors.ErrModel)):
		return "model", 0
	case errors.As(err, &hs):
		return "http_status", hs.StatusCode
	case errors.As(err, &api):
		return "api", api.StatusCode
	}
	return "other", 0
}

func TestConformanceOpenAIRequest(t *testing.T) {
	f := loadOpenAIConf(t)
	require.NotEmpty(t, f.OpenAIRequest)
	for _, v := range f.OpenAIRequest {
		t.Run(v.Name, func(t *testing.T) {
			var body []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ = io.ReadAll(r.Body)
				if strings.HasSuffix(v.Call, "stream") || v.Call == "stream_tools" {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, okSSE)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, okCompletion)
			}))
			t.Cleanup(srv.Close)
			p := newConfAdapter(t, srv.URL, v.AdapterModel, nil)
			req := v.Request.toLLM()
			ctx := context.Background()
			var err error
			switch v.Call {
			case "complete":
				_, err = p.(llm.Completer).Complete(ctx, req)
			case "tools":
				_, err = p.(llm.ToolCaller).CallWithTools(ctx, req, toolDefs(v.Tools))
			case "stream":
				var it llm.TokenIterator
				if it, err = p.(llm.Streamer).Stream(ctx, req); err == nil {
					err = drain(it)
				}
			case "stream_tools":
				var st llm.ToolStream
				if st, err = p.(llm.ToolStreamer).StreamWithTools(ctx, req, toolDefs(v.Tools)); err == nil {
					err = drain(st)
				}
			default:
				t.Fatalf("unknown call %q", v.Call)
			}
			require.NoError(t, err)
			assert.JSONEq(t, string(v.WantBody), string(body))
		})
	}
}

// drain reads it to its end, closing it.
func drain(it llm.TokenIterator) error {
	defer it.Close()
	for {
		tok, err := it.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if tok.Done {
			return nil
		}
	}
}

func TestConformanceOpenAIStream(t *testing.T) {
	f := loadOpenAIConf(t)
	require.NotEmpty(t, f.OpenAIStream)
	for _, v := range f.OpenAIStream {
		t.Run(v.Name, func(t *testing.T) {
			p := newConfAdapter(t, sseServer(t, v.SSE).URL, "m", nil)
			it, err := p.(llm.Streamer).Stream(context.Background(), llm.Request{
				Messages: []llm.Message{{Role: "user", Content: "Hi"}},
			})
			require.NoError(t, err)
			defer it.Close()
			tokens := []string{}
			var streamErr error
			for {
				tok, err := it.Next()
				if err != nil {
					if !errors.Is(err, io.EOF) {
						streamErr = err
					}
					break
				}
				if tok.Done {
					_, err := it.Next()
					assert.ErrorIs(t, err, io.EOF, "Next after Done is EOF")
					break
				}
				tokens = append(tokens, tok.Content)
			}
			assert.Equal(t, v.WantTokens, tokens)
			kind, _ := errorKind(streamErr)
			assert.Equal(t, v.ErrorKind, kind, "error: %v", streamErr)
			if streamErr != nil {
				assert.NotContains(t, streamErr.Error(), confKey)
			}
		})
	}
}

// streams maps "openai_stream/<name>" to its SSE body, for sse_from.
func (f openaiConfFile) streams() map[string]string {
	out := map[string]string{}
	for _, s := range f.OpenAIStream {
		out["openai_stream/"+s.Name] = s.SSE
	}
	return out
}

func sseOf(t *testing.T, f openaiConfFile, sse, from string) string {
	t.Helper()
	if from == "" {
		return sse
	}
	body, ok := f.streams()[from]
	require.True(t, ok, "sse_from %q", from)
	return body
}

// eventJSON spells one streamed token as the vectors do.
func eventJSON(t *testing.T, tok llm.Token) map[string]any {
	t.Helper()
	switch {
	case tok.Done:
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
			var args any
			require.NoError(t, json.Unmarshal(ev.Call.Arguments, &args))
			out["id"], out["name"], out["arguments"] = ev.Call.ID, ev.Call.Name, args
			assert.Equal(t, ev.Call.ID, ev.ID, "end repeats the id")
			assert.Equal(t, ev.Call.Name, ev.Name, "end repeats the name")
		}
		return out
	case tok.Usage != nil:
		u := tok.Usage
		return map[string]any{"type": "usage", "usage": map[string]any{
			"prompt_tokens": u.PromptTokens, "completion_tokens": u.CompletionTokens,
			"cache_read_tokens": u.CacheReadTokens, "cache_creation_tokens": u.CacheCreationTokens,
			"total_tokens": u.TotalTokens,
		}}
	default:
		return map[string]any{"type": "text", "text": tok.Content}
	}
}

func TestConformanceOpenAIStreamEvents(t *testing.T) {
	f := loadOpenAIConf(t)
	require.NotEmpty(t, f.OpenAIStreamEvents)
	for _, v := range f.OpenAIStreamEvents {
		t.Run(v.Name, func(t *testing.T) {
			p := newConfAdapter(t, sseServer(t, sseOf(t, f, v.SSE, v.SSEFrom)).URL, "m", nil)
			req := llm.Request{
				Messages:     []llm.Message{{Role: "user", Content: "Hi"}},
				StreamEvents: v.StreamEvents,
			}
			var it llm.TokenIterator
			var st llm.ToolStream
			var err error
			if len(v.Tools) > 0 {
				st, err = p.(llm.ToolStreamer).StreamWithTools(context.Background(), req, namedTools(v.Tools))
				it = st
			} else {
				it, err = p.(llm.Streamer).Stream(context.Background(), req)
			}
			require.NoError(t, err)
			defer it.Close()
			var got []any
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
			kind, _ := errorKind(streamErr)
			assert.Equal(t, v.ErrorKind, kind, "error: %v", streamErr)
			if st == nil {
				return
			}
			out, ok := st.Outcome()
			if streamErr != nil {
				assert.False(t, ok, "no outcome after an error")
				return
			}
			// The events and the outcome tell the same story.
			require.True(t, ok)
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

func TestConformanceOpenAIAccumulate(t *testing.T) {
	f := loadOpenAIConf(t)
	require.NotEmpty(t, f.OpenAIAccumulate)
	for _, v := range f.OpenAIAccumulate {
		t.Run(v.Name, func(t *testing.T) {
			p := newConfAdapter(t, sseServer(t, sseOf(t, f, v.SSE, v.SSEFrom)).URL, "m", nil)
			st, err := p.(llm.ToolStreamer).StreamWithTools(context.Background(), llm.Request{
				Messages: []llm.Message{{Role: "user", Content: "Hi"}},
			}, namedTools(v.Tools))
			require.NoError(t, err)
			streamErr := drain(st)
			require.Equal(t, v.Want.Error, streamErr != nil, "error: %v", streamErr)
			out, ok := st.Outcome()
			if v.Want.Error {
				assert.False(t, ok, "no outcome after an error")
				return
			}
			require.True(t, ok)
			assert.Equal(t, v.Want.Text, out.Content)
			assert.Equal(t, v.Want.FinishReason, out.FinishReason)
			assert.Equal(t, llm.Usage{
				PromptTokens: v.Want.Usage.PromptTokens, CompletionTokens: v.Want.Usage.CompletionTokens,
				TotalTokens: v.Want.Usage.TotalTokens,
			}, out.Usage)
			require.Len(t, out.ToolCalls, len(v.Want.ToolCalls))
			for i, want := range v.Want.ToolCalls {
				assert.Equal(t, want.ID, out.ToolCalls[i].ID)
				assert.Equal(t, want.Name, out.ToolCalls[i].Name)
				assert.JSONEq(t, string(want.Arguments), string(out.ToolCalls[i].Arguments))
			}
		})
	}
}

func TestConformanceOpenAIError(t *testing.T) {
	f := loadOpenAIConf(t)
	require.NotEmpty(t, f.OpenAIError)
	for _, v := range f.OpenAIError {
		t.Run(v.Name, func(t *testing.T) {
			var attempts atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				n := int(attempts.Add(1)) - 1
				r := v.Responses[min(n, len(v.Responses)-1)]
				for k, val := range r.Headers {
					w.Header().Set(k, val)
				}
				if w.Header().Get("Content-Type") == "" {
					w.Header().Set("Content-Type", "application/json")
				}
				w.WriteHeader(r.Status)
				_, _ = io.WriteString(w, r.Body)
			}))
			t.Cleanup(srv.Close)
			p := newConfAdapter(t, srv.URL, "m", nil)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			req := llm.Request{Messages: []llm.Message{{Role: "user", Content: "Hi"}}}
			var err error
			if v.Stream {
				var it llm.TokenIterator
				if it, err = p.(llm.Streamer).Stream(ctx, req); err == nil {
					err = drain(it)
				}
			} else {
				_, err = p.(llm.Completer).Complete(ctx, req)
			}
			if err != nil {
				assert.NotContains(t, err.Error(), confKey)
			}
			kind, status := errorKind(err)
			assert.Equal(t, v.WantKind, kind, "error: %v", err)
			assert.Equal(t, v.WantStatus, status)
			assert.Equal(t, v.Fallbackable, llmerrors.IsFallbackable(err))
			assert.Equal(t, v.Attempts, int(attempts.Load()))
		})
	}
}

func TestConformanceOpenAILimits(t *testing.T) {
	f := loadOpenAIConf(t)
	require.NotEmpty(t, f.OpenAILimits)
	for _, v := range f.OpenAILimits {
		t.Run(v.Name, func(t *testing.T) {
			var attempts atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				attempts.Add(1)
				w.Header().Set("Content-Type", v.ContentType)
				_, _ = io.WriteString(w, v.Body)
			}))
			t.Cleanup(srv.Close)
			p := newConfAdapter(t, srv.URL, "m", v.Params)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			req := llm.Request{Messages: []llm.Message{{Role: "user", Content: "Hi"}}}
			var err error
			if v.Stream {
				var it llm.TokenIterator
				if it, err = p.(llm.Streamer).Stream(ctx, req); err == nil {
					err = drain(it)
				}
			} else {
				_, err = p.(llm.Completer).Complete(ctx, req)
			}
			assert.Equal(t, int32(1), attempts.Load(), "an overrun is not retried")
			if v.TooLarge {
				kind, _ := errorKind(err)
				assert.Equal(t, "too_large", kind, "error: %v", err)
				assert.False(t, llmerrors.IsFallbackable(err))
				return
			}
			assert.NoError(t, err)
		})
	}
}
