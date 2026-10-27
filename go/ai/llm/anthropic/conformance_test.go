package anthropic_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/ai/llm/anthropic"
	llmerrors "hop.top/kit/go/ai/llm/errors"
)

// Runs the anthropic_* sections of contracts/llm-v1 against the kit
// adapter (request shaping, headers, stream tokens, error mapping) and
// the SDK's Message.Accumulate (stream accumulation). The vectors are
// the source of truth; the provider-neutral sections run in go/ai/llm.

const anthropicConfPath = "../../../../contracts/llm-v1/vectors.json"

type confMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Parts   []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		MimeType string `json:"mime_type"`
		Data     string `json:"data"`
	} `json:"parts"`
	ToolCalls []struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"tool_calls"`
	ToolCallID string `json:"tool_call_id"`
}

type confRequest struct {
	Model         string        `json:"model"`
	Temperature   *float64      `json:"temperature"`
	MaxTokens     int           `json:"max_tokens"`
	StopSequences []string      `json:"stop_sequences"`
	Messages      []confMessage `json:"messages"`
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

type confAccumulated struct {
	Text      string `json:"text"`
	Thinking  string `json:"thinking"`
	ToolCalls []struct {
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"tool_calls"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
	Error bool `json:"error"`
}

type anthropicConfFile struct {
	Version          string          `json:"version"`
	Description      string          `json:"description"`
	RedactURI        json.RawMessage `json:"redact_uri"`
	ParseURI         json.RawMessage `json:"parse_uri"`
	AnthropicRequest []struct {
		Name         string          `json:"name"`
		AdapterModel string          `json:"adapter_model"`
		Request      confRequest     `json:"request"`
		Tools        []confTool      `json:"tools"`
		WantBody     json.RawMessage `json:"want_body"`
	} `json:"anthropic_request"`
	AnthropicRequestError []struct {
		Name    string      `json:"name"`
		Request confRequest `json:"request"`
		Error   string      `json:"error"`
	} `json:"anthropic_request_error"`
	AnthropicHeaders []struct {
		Name   string            `json:"name"`
		APIKey string            `json:"api_key"`
		Env    map[string]string `json:"env"`
		Want   map[string]string `json:"want"`
		Absent []string          `json:"absent"`
	} `json:"anthropic_headers"`
	AnthropicStream []struct {
		Name       string   `json:"name"`
		SSE        string   `json:"sse"`
		WantTokens []string `json:"want_tokens"`
		WantError  bool     `json:"want_error"`
		ErrorKind  string   `json:"error_kind"`
	} `json:"anthropic_stream"`
	AnthropicAccumulate []struct {
		Name    string          `json:"name"`
		SSE     string          `json:"sse"`
		SSEFrom string          `json:"sse_from"`
		Want    confAccumulated `json:"want"`
	} `json:"anthropic_accumulate"`
	AnthropicError []struct {
		Name         string         `json:"name"`
		Responses    []confResponse `json:"responses"`
		WantKind     string         `json:"want_kind"`
		WantStatus   int            `json:"want_status"`
		Fallbackable bool           `json:"fallbackable"`
		Attempts     int            `json:"attempts"`
		Stream       bool           `json:"stream"`
	} `json:"anthropic_error"`
	AnthropicLimits []struct {
		Name        string            `json:"name"`
		Params      map[string]string `json:"params"`
		Stream      bool              `json:"stream"`
		ContentType string            `json:"content_type"`
		Body        string            `json:"body"`
		TooLarge    bool              `json:"too_large"`
	} `json:"anthropic_limits"`
	KeyEndpoint []struct {
		Name    string            `json:"name"`
		Op      string            `json:"op"`
		URI     string            `json:"uri"`
		YAML    string            `json:"yaml"`
		Env     map[string]string `json:"env"`
		WantURI string            `json:"want_uri"`
		Refused bool              `json:"refused"`
	} `json:"key_endpoint"`
	CredentialHeaders []struct {
		Name     string            `json:"name"`
		BaseURL  string            `json:"base_url"`
		YAML     string            `json:"yaml"`
		Env      map[string]string `json:"env"`
		Sent     []string          `json:"sent"`
		Withheld []string          `json:"withheld"`
		Refused  bool              `json:"refused"`
	} `json:"credential_headers"`
}

func loadAnthropicConf(t *testing.T) anthropicConfFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(anthropicConfPath))
	require.NoError(t, err)
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var f anthropicConfFile
	require.NoError(t, dec.Decode(&f))
	require.Equal(t, "v1", f.Version)
	return f
}

func (r confRequest) toLLM(t *testing.T) llm.Request {
	t.Helper()
	out := llm.Request{
		Model:         r.Model,
		Temperature:   r.Temperature,
		MaxTokens:     r.MaxTokens,
		StopSequences: r.StopSequences,
	}
	for _, m := range r.Messages {
		msg := llm.Message{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID}
		for _, p := range m.Parts {
			part := llm.ContentPart{Type: llm.PartType(p.Type), Text: p.Text, MimeType: p.MimeType}
			if p.Data != "" {
				data, err := base64.StdEncoding.DecodeString(p.Data)
				require.NoError(t, err)
				part.Source = llm.InlineSource(data, p.MimeType)
			}
			msg.Parts = append(msg.Parts, part)
		}
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

// clearAnthropicEnv unsets the ANTHROPIC_* inputs the adapter and SDK read.
func clearAnthropicEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL",
		"ANTHROPIC_PROFILE", "ANTHROPIC_WORKSPACE_ID", "ANTHROPIC_ORGANIZATION_ID",
		"ANTHROPIC_CUSTOM_HEADERS",
	} {
		unsetenv(t, k)
	}
	t.Setenv("ANTHROPIC_CONFIG_DIR", t.TempDir())
}

func newConfAdapter(t *testing.T, base, model, key string) llm.Provider {
	t.Helper()
	p, err := anthropic.New(llm.ResolvedConfig{Provider: llm.ProviderConfig{
		APIKey: key, BaseURL: base, Model: model,
	}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func okMessage(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(anthropicResponse("ok", "end_turn", 1, 1))
}

func TestConformanceAnthropicRequest(t *testing.T) {
	f := loadAnthropicConf(t)
	require.NotEmpty(t, f.AnthropicRequest)
	for _, v := range f.AnthropicRequest {
		t.Run(v.Name, func(t *testing.T) {
			clearAnthropicEnv(t)
			var body []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ = io.ReadAll(r.Body)
				okMessage(w)
			}))
			t.Cleanup(srv.Close)
			p := newConfAdapter(t, srv.URL, v.AdapterModel, "k")
			req := v.Request.toLLM(t)
			var err error
			if v.Tools != nil {
				_, err = p.(llm.ToolCaller).CallWithTools(context.Background(), req, toolDefs(v.Tools))
			} else {
				_, err = p.(llm.Completer).Complete(context.Background(), req)
			}
			require.NoError(t, err)
			assert.JSONEq(t, string(v.WantBody), string(body))
		})
	}
}

func TestConformanceAnthropicRequestError(t *testing.T) {
	f := loadAnthropicConf(t)
	require.NotEmpty(t, f.AnthropicRequestError)
	for _, v := range f.AnthropicRequestError {
		t.Run(v.Name, func(t *testing.T) {
			clearAnthropicEnv(t)
			var sent atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				sent.Add(1)
				okMessage(w)
			}))
			t.Cleanup(srv.Close)
			p := newConfAdapter(t, srv.URL, "m", "k")
			_, err := p.(llm.Completer).Complete(context.Background(), v.Request.toLLM(t))
			require.Error(t, err)
			assert.Equal(t, v.Error, err.Error())
			assert.Zero(t, sent.Load(), "a rejected request is never sent")
		})
	}
}

func TestConformanceAnthropicHeaders(t *testing.T) {
	f := loadAnthropicConf(t)
	require.NotEmpty(t, f.AnthropicHeaders)
	for _, v := range f.AnthropicHeaders {
		t.Run(v.Name, func(t *testing.T) {
			clearAnthropicEnv(t)
			for k, val := range v.Env {
				t.Setenv(k, val)
			}
			var got http.Header
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Clone()
				okMessage(w)
			}))
			t.Cleanup(srv.Close)
			p := newConfAdapter(t, srv.URL, "m", v.APIKey)
			_, err := p.(llm.Completer).Complete(context.Background(), llm.Request{
				Messages: []llm.Message{{Role: "user", Content: "Hi"}},
			})
			require.NoError(t, err)
			for k, want := range v.Want {
				assert.Equal(t, want, got.Get(k), k)
			}
			for _, k := range v.Absent {
				assert.Empty(t, got.Values(k), k)
			}
		})
	}
}

func sseServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestConformanceAnthropicStream(t *testing.T) {
	f := loadAnthropicConf(t)
	require.NotEmpty(t, f.AnthropicStream)
	for _, v := range f.AnthropicStream {
		t.Run(v.Name, func(t *testing.T) {
			clearAnthropicEnv(t)
			p := newConfAdapter(t, sseServer(t, v.SSE).URL, "m", "k")
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
			assert.Equal(t, v.WantError, streamErr != nil, "error: %v", streamErr)
			kind, _ := errorKind(streamErr)
			assert.Equal(t, v.ErrorKind, kind, "error: %v", streamErr)
		})
	}
}

func TestConformanceAnthropicAccumulate(t *testing.T) {
	f := loadAnthropicConf(t)
	require.NotEmpty(t, f.AnthropicAccumulate)
	streams := map[string]string{}
	for _, s := range f.AnthropicStream {
		streams["anthropic_stream/"+s.Name] = s.SSE
	}
	for _, v := range f.AnthropicAccumulate {
		t.Run(v.Name, func(t *testing.T) {
			clearAnthropicEnv(t)
			sse := v.SSE
			if v.SSEFrom != "" {
				var ok bool
				sse, ok = streams[v.SSEFrom]
				require.True(t, ok, "sse_from %q", v.SSEFrom)
			}
			p := newConfAdapter(t, sseServer(t, sse).URL, "m", "k")
			st, err := p.(llm.ToolStreamer).StreamWithTools(context.Background(), llm.Request{
				Messages: []llm.Message{{Role: "user", Content: "Hi"}},
			}, []llm.ToolDef{{Name: "get_weather"}})
			require.NoError(t, err)
			defer st.Close()
			var streamErr error
			for {
				tok, err := st.Next()
				if err != nil {
					if !errors.Is(err, io.EOF) {
						streamErr = err
					}
					break
				}
				if tok.Done {
					break
				}
			}
			require.Equal(t, v.Want.Error, streamErr != nil, "error: %v", streamErr)
			if v.Want.Error {
				_, ok := st.Outcome()
				assert.False(t, ok, "no outcome after an error")
				return
			}
			out, ok := st.Outcome()
			require.True(t, ok)
			thinking := ""
			for _, b := range out.Thinking {
				thinking += b.Thinking
			}
			assert.Equal(t, v.Want.Text, out.Content)
			assert.Equal(t, v.Want.Thinking, thinking)
			assert.Equal(t, v.Want.StopReason, out.FinishReason)
			assert.Equal(t, int(v.Want.Usage.InputTokens), out.Usage.PromptTokens)
			assert.Equal(t, int(v.Want.Usage.OutputTokens), out.Usage.CompletionTokens)
			require.Len(t, out.ToolCalls, len(v.Want.ToolCalls))
			for i, want := range v.Want.ToolCalls {
				assert.Equal(t, want.ID, out.ToolCalls[i].ID)
				assert.Equal(t, want.Name, out.ToolCalls[i].Name)
				assert.JSONEq(t, string(want.Input), string(out.ToolCalls[i].Arguments))
			}
		})
	}
}

// errorKind names the llm error class err maps to, as the vectors do.
func errorKind(err error) (kind string, status int) {
	var auth *llmerrors.ErrAuth
	var rl *llmerrors.ErrRateLimit
	var model *llmerrors.ErrModel
	var hs *llmerrors.HTTPStatusError
	var api *sdk.Error
	switch {
	case err == nil:
		return "", 0
	case errors.As(err, new(*llmerrors.ErrStreamTruncated)):
		return "truncated", 0
	case errors.As(err, new(*llmerrors.ErrResponseTooLarge)):
		return "too_large", 0
	case errors.As(err, &auth):
		return "auth", 0
	case errors.As(err, &rl):
		return "rate_limit", 0
	case errors.As(err, &model):
		return "model", 0
	case errors.As(err, &hs):
		return "http_status", hs.StatusCode
	case errors.As(err, &api):
		return "api", api.StatusCode
	}
	return "other", 0
}

func TestConformanceAnthropicError(t *testing.T) {
	f := loadAnthropicConf(t)
	require.NotEmpty(t, f.AnthropicError)
	for _, v := range f.AnthropicError {
		t.Run(v.Name, func(t *testing.T) {
			clearAnthropicEnv(t)
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
			p := newConfAdapter(t, srv.URL, "m", confKey)
			// A wait the client should not honor fails here, not hangs.
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			req := llm.Request{Messages: []llm.Message{{Role: "user", Content: "Hi"}}}
			var err error
			if v.Stream {
				err = drainStream(ctx, p, req)
			} else {
				_, err = p.(llm.Completer).Complete(ctx, req)
			}
			if err != nil {
				assert.NotContains(t, err.Error(), confKey)
				assert.NotContains(t, fmt.Sprintf("%+v", err), confKey)
			}
			kind, status := errorKind(err)
			assert.Equal(t, v.WantKind, kind, "error: %v", err)
			assert.Equal(t, v.WantStatus, status)
			assert.Equal(t, v.Fallbackable, llmerrors.IsFallbackable(err))
			assert.Equal(t, v.Attempts, int(attempts.Load()))
		})
	}
}

func TestConformanceKeyEndpoint(t *testing.T) {
	f := loadAnthropicConf(t)
	require.NotEmpty(t, f.KeyEndpoint)
	for _, v := range f.KeyEndpoint {
		t.Run(v.Name, func(t *testing.T) {
			clearAnthropicEnv(t)
			unsetenv(t, "LLM_BASE_URL")
			unsetenv(t, llm.FallbackEnvKey)
			unsetenv(t, "LLM_PROVIDER")
			dir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", dir)
			if v.YAML != "" {
				require.NoError(t, os.MkdirAll(filepath.Join(dir, "hop"), 0o750))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "hop", "llm.yaml"), []byte(v.YAML), 0o600))
			}
			for k, val := range v.Env {
				t.Setenv(k, val)
			}
			var err error
			switch v.Op {
			case "apply":
				var got string
				got, err = llm.ApplyAPIKey(context.Background(), nil, v.URI)
				if !v.Refused {
					assert.Equal(t, v.WantURI, got)
				}
			case "load":
				_, err = llm.LoadConfig(v.URI)
			case "resolve":
				_, err = llm.Resolve(v.URI)
			default:
				t.Fatalf("unknown op %q", v.Op)
			}
			if v.Refused {
				assert.ErrorIs(t, err, llm.ErrKeyEndpoint)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestConformanceCredentialHeaders(t *testing.T) {
	f := loadAnthropicConf(t)
	require.NotEmpty(t, f.CredentialHeaders)
	for _, v := range f.CredentialHeaders {
		t.Run(v.Name, func(t *testing.T) {
			_, rt, _ := isolateAnthropicEnv(t)
			unsetenv(t, "LLM_BASE_URL")
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			if v.YAML != "" {
				writeLLMYAML(t, v.YAML)
			}
			for k, val := range v.Env {
				t.Setenv(k, val)
			}
			p, err := anthropic.New(llm.ResolvedConfig{Provider: llm.ProviderConfig{
				APIKey: "kit-key", BaseURL: v.BaseURL, Model: "m",
			}})
			if v.Refused {
				require.ErrorIs(t, err, llm.ErrKeyEndpoint)
				return
			}
			require.NoError(t, err)
			_, err = p.(llm.Completer).Complete(context.Background(), llm.Request{
				Messages: []llm.Message{{Role: "user", Content: "Hi"}},
			})
			require.NoError(t, err)
			req := rt.only(t)
			for _, h := range v.Sent {
				assert.NotEmpty(t, req.Header.Get(h), h)
			}
			for _, h := range v.Withheld {
				assert.Empty(t, req.Header.Values(h), h)
			}
		})
	}
}

// confKey is the key error vectors run with; no error text may hold it.
const confKey = "conf-key-must-not-leak"

// drainStream opens a stream and reads it to its end.
func drainStream(ctx context.Context, p llm.Provider, req llm.Request) error {
	it, err := p.(llm.Streamer).Stream(ctx, req)
	if err != nil {
		return err
	}
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

func TestConformanceAnthropicLimits(t *testing.T) {
	f := loadAnthropicConf(t)
	require.NotEmpty(t, f.AnthropicLimits)
	for _, v := range f.AnthropicLimits {
		t.Run(v.Name, func(t *testing.T) {
			clearAnthropicEnv(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", v.ContentType)
				_, _ = io.WriteString(w, v.Body)
			}))
			t.Cleanup(srv.Close)
			p, err := anthropic.New(llm.ResolvedConfig{Provider: llm.ProviderConfig{
				APIKey: "k", BaseURL: srv.URL, Model: "m", Params: v.Params,
			}})
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			req := llm.Request{Messages: []llm.Message{{Role: "user", Content: "Hi"}}}
			if v.Stream {
				err = drainStream(ctx, p, req)
			} else {
				_, err = p.(llm.Completer).Complete(ctx, req)
			}
			if v.TooLarge {
				kind, _ := errorKind(err)
				assert.Equal(t, "too_large", kind, "error: %v", err)
				return
			}
			assert.NoError(t, err)
		})
	}
}
