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
		URL      string `json:"url"`
	} `json:"parts"`
	ToolCalls []struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"tool_calls"`
	ToolCallID   string        `json:"tool_call_id"`
	CacheControl *confCache    `json:"cache_control"`
	Thinking     []confThought `json:"thinking"`
}

type confCache struct {
	TTL string `json:"ttl"`
}

type confThought struct {
	Thinking  string `json:"thinking"`
	Signature string `json:"signature"`
	Redacted  string `json:"redacted"`
}

func (c *confCache) toLLM() *llm.CacheControl {
	if c == nil {
		return nil
	}
	return &llm.CacheControl{TTL: c.TTL}
}

func thoughts(ts []confThought) []llm.ThinkingBlock {
	var out []llm.ThinkingBlock
	for _, t := range ts {
		out = append(out, llm.ThinkingBlock{Thinking: t.Thinking, Signature: t.Signature, Redacted: t.Redacted})
	}
	return out
}

type confRequest struct {
	Model          string        `json:"model"`
	Temperature    *float64      `json:"temperature"`
	MaxTokens      int           `json:"max_tokens"`
	StopSequences  []string      `json:"stop_sequences"`
	Messages       []confMessage `json:"messages"`
	ThinkingBudget int           `json:"thinking_budget"`
}

type confTool struct {
	Name         string     `json:"name"`
	Description  string     `json:"description"`
	Parameters   string     `json:"parameters"`
	CacheControl *confCache `json:"cache_control"`
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
		InputTokens         int64 `json:"input_tokens"`
		OutputTokens        int64 `json:"output_tokens"`
		CacheReadTokens     int   `json:"cache_read_tokens"`
		CacheCreationTokens int   `json:"cache_creation_tokens"`
	} `json:"usage"`
	Error          bool           `json:"error"`
	ThinkingBlocks *[]confThought `json:"thinking_blocks"`
}

type anthropicConfFile struct {
	Version          string          `json:"version"`
	Description      string          `json:"description"`
	RedactURI        json.RawMessage `json:"redact_uri"`
	ParseURI         json.RawMessage `json:"parse_uri"`
	MediaFetch       json.RawMessage `json:"media_fetch"` // run in go/ai/llm
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
		Name           string   `json:"name"`
		SSE            string   `json:"sse"`
		WantTokens     []string `json:"want_tokens"`
		WantError      bool     `json:"want_error"`
		ErrorKind      string   `json:"error_kind"`
		ThinkingBudget int      `json:"thinking_budget"`
		WantThinking   []string `json:"want_thinking"`
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
	AnthropicResponse []struct {
		Name  string `json:"name"`
		Tools bool   `json:"tools"`
		Body  string `json:"body"`
		Want  struct {
			Content        string        `json:"content"`
			ThinkingBlocks []confThought `json:"thinking_blocks"`
			ToolCalls      []struct {
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			} `json:"tool_calls"`
			StopReason string    `json:"stop_reason"`
			Usage      confUsage `json:"usage"`
		} `json:"want"`
	} `json:"anthropic_response"`
	UsageCost           json.RawMessage `json:"usage_cost"`
	BusRequestStarted   json.RawMessage `json:"bus_request_started"`
	BusResponseReceived json.RawMessage `json:"bus_response_received"`
	ToolNames           json.RawMessage `json:"tool_names"`
	AnthropicToolNames  []struct {
		Name        string            `json:"name"`
		Call        string            `json:"call"`
		Params      map[string]string `json:"params"`
		Messages    []confMessage     `json:"messages"`
		Tools       []string          `json:"tools"`
		Body        string            `json:"body"`
		WantTools   []string          `json:"want_tools"`
		WantHistory []string          `json:"want_history"`
		WantCalls   []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"want_calls"`
		Error *struct {
			Reason string `json:"reason"`
			Name   string `json:"name"`
			Other  string `json:"other"`
		} `json:"error"`
		Refused bool `json:"refused"`
	} `json:"anthropic_tool_names"`
	AnthropicToolChoice []struct {
		Name       string   `json:"name"`
		Call       string   `json:"call"`
		Tools      []string `json:"tools"`
		ToolChoice *struct {
			Mode string `json:"mode"`
			Name string `json:"name"`
		} `json:"tool_choice"`
		WantToolChoice json.RawMessage `json:"want_tool_choice"`
		Error          string          `json:"error"`
	} `json:"anthropic_tool_choice"`
	AnthropicHermetic []struct {
		Name     string            `json:"name"`
		BaseURL  string            `json:"base_url"`
		YAML     string            `json:"yaml"`
		Env      map[string]string `json:"env"`
		WantHost string            `json:"want_host"`
		Sent     []string          `json:"sent"`
		Withheld []string          `json:"withheld"`
	} `json:"anthropic_hermetic"`
	AnthropicStreamEvents []struct {
		Name         string            `json:"name"`
		SSE          string            `json:"sse"`
		SSEFrom      string            `json:"sse_from"`
		Tools        []string          `json:"tools"`
		StreamEvents bool              `json:"stream_events"`
		WantEvents   []json.RawMessage `json:"want_events"`
		ErrorKind    string            `json:"error_kind"`
	} `json:"anthropic_stream_events"`
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
	if r.ThinkingBudget > 0 {
		out.Thinking = &llm.ThinkingConfig{BudgetTokens: r.ThinkingBudget}
	}
	for _, m := range r.Messages {
		msg := llm.Message{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID,
			CacheControl: m.CacheControl.toLLM(), Thinking: thoughts(m.Thinking)}
		for _, p := range m.Parts {
			part := llm.ContentPart{Type: llm.PartType(p.Type), Text: p.Text, MimeType: p.MimeType}
			if p.Data != "" {
				data, err := base64.StdEncoding.DecodeString(p.Data)
				require.NoError(t, err)
				part.Source = llm.InlineSource(data, p.MimeType)
			}
			if p.URL != "" {
				part.Source = unfetchedURL{t: t, url: p.URL}
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
		def := llm.ToolDef{Name: d.Name, Description: d.Description, CacheControl: d.CacheControl.toLLM()}
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
			req := llm.Request{Messages: []llm.Message{{Role: "user", Content: "Hi"}}}
			if v.ThinkingBudget > 0 {
				req.MaxTokens = 4 * v.ThinkingBudget
				req.Thinking = &llm.ThinkingConfig{BudgetTokens: v.ThinkingBudget}
			}
			it, err := p.(llm.Streamer).Stream(context.Background(), req)
			require.NoError(t, err)
			defer it.Close()
			tokens, thinking := []string{}, []string{}
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
				if tok.Thinking != "" {
					thinking = append(thinking, tok.Thinking)
					continue
				}
				tokens = append(tokens, tok.Content)
			}
			assert.Equal(t, v.WantTokens, tokens)
			assert.Equal(t, v.WantThinking, thinking)
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
			assert.Equal(t, v.Want.Usage.CacheReadTokens, out.Usage.CacheReadTokens)
			assert.Equal(t, v.Want.Usage.CacheCreationTokens, out.Usage.CacheCreationTokens)
			if v.Want.ThinkingBlocks != nil {
				assert.Equal(t, thoughts(*v.Want.ThinkingBlocks), out.Thinking)
			}
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
	case errors.As(err, new(*llmerrors.ErrPermissionDenied)):
		if !errors.As(err, &auth) {
			return "permission_denied without auth", 0
		}
		return "permission_denied", 0
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

func TestConformanceAnthropicResponse(t *testing.T) {
	f := loadAnthropicConf(t)
	require.NotEmpty(t, f.AnthropicResponse)
	for _, v := range f.AnthropicResponse {
		t.Run(v.Name, func(t *testing.T) {
			clearAnthropicEnv(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, v.Body)
			}))
			t.Cleanup(srv.Close)
			p := newConfAdapter(t, srv.URL, "m", "k")
			req := llm.Request{Messages: []llm.Message{{Role: "user", Content: "Hi"}}}
			var content, stop string
			var usage llm.Usage
			var thinking []llm.ThinkingBlock
			var calls []llm.ToolCall
			if v.Tools {
				r, err := p.(llm.ToolCaller).CallWithTools(context.Background(), req, []llm.ToolDef{{Name: "f"}})
				require.NoError(t, err)
				content, stop, usage, thinking, calls = r.Content, r.FinishReason, r.Usage, r.Thinking, r.ToolCalls
			} else {
				r, err := p.(llm.Completer).Complete(context.Background(), req)
				require.NoError(t, err)
				content, stop, usage, thinking = r.Content, r.FinishReason, r.Usage, r.Thinking
			}
			assert.Equal(t, v.Want.Content, content)
			assert.Equal(t, v.Want.StopReason, stop)
			assert.Equal(t, llm.Usage(v.Want.Usage), usage)
			assert.Equal(t, thoughts(v.Want.ThinkingBlocks), thinking)
			require.Len(t, calls, len(v.Want.ToolCalls))
			for i, want := range v.Want.ToolCalls {
				assert.Equal(t, want.ID, calls[i].ID)
				assert.Equal(t, want.Name, calls[i].Name)
				assert.JSONEq(t, string(want.Input), string(calls[i].Arguments))
			}
		})
	}
}

type confUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	CacheReadTokens     int `json:"cache_read_tokens"`
	CacheCreationTokens int `json:"cache_creation_tokens"`
	TotalTokens         int `json:"total_tokens"`
}

// sentToolNames reads the tool names and the tool_use names of the
// history from a Messages API request body.
func sentToolNames(t *testing.T, body []byte) (tools, history []string) {
	t.Helper()
	var b struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
		Messages []struct {
			Content []struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(body, &b))
	for _, tl := range b.Tools {
		tools = append(tools, tl.Name)
	}
	for _, m := range b.Messages {
		for _, c := range m.Content {
			if c.Type == "tool_use" {
				history = append(history, c.Name)
			}
		}
	}
	return tools, history
}

func TestConformanceAnthropicToolNames(t *testing.T) {
	f := loadAnthropicConf(t)
	require.NotEmpty(t, f.AnthropicToolNames)
	for _, v := range f.AnthropicToolNames {
		t.Run(v.Name, func(t *testing.T) {
			clearAnthropicEnv(t)
			var body []byte
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				body, _ = io.ReadAll(r.Body)
				if v.Call == "stream" || v.Call == "stream_tools" {
					w.Header().Set("Content-Type", "text/event-stream")
				} else {
					w.Header().Set("Content-Type", "application/json")
				}
				_, _ = io.WriteString(w, v.Body)
			}))
			t.Cleanup(srv.Close)
			p, err := anthropic.New(llm.ResolvedConfig{Provider: llm.ProviderConfig{
				APIKey: "k", BaseURL: srv.URL, Model: "m", Params: v.Params,
			}})
			if v.Refused {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			t.Cleanup(func() { _ = p.Close() })

			req := llm.Request{Messages: []llm.Message{{Role: "user", Content: "Hi"}}}
			if v.Messages != nil {
				req = confRequest{Messages: v.Messages}.toLLM(t)
			}
			var defs []llm.ToolDef
			for _, n := range v.Tools {
				defs = append(defs, llm.ToolDef{Name: n})
			}
			ctx := context.Background()
			var calls []llm.ToolCall
			switch v.Call {
			case "tools":
				var r llm.ToolResponse
				r, err = p.(llm.ToolCaller).CallWithTools(ctx, req, defs)
				calls = r.ToolCalls
			case "stream_tools":
				var st llm.ToolStream
				st, err = p.(llm.ToolStreamer).StreamWithTools(ctx, req, defs)
				if err == nil {
					for {
						tok, nerr := st.Next()
						require.NoError(t, nerr)
						if tok.Done {
							break
						}
					}
					out, ok := st.Outcome()
					require.True(t, ok)
					calls = out.ToolCalls
					_ = st.Close()
				}
			case "complete":
				_, err = p.(llm.Completer).Complete(ctx, req)
			case "stream":
				var it llm.TokenIterator
				it, err = p.(llm.Streamer).Stream(ctx, req)
				if err == nil {
					for {
						tok, nerr := it.Next()
						require.NoError(t, nerr)
						if tok.Done {
							break
						}
					}
					_ = it.Close()
				}
			default:
				t.Fatalf("unknown call %q", v.Call)
			}
			if v.Error != nil {
				var te *llmerrors.ErrToolName
				require.ErrorAs(t, err, &te)
				assert.Equal(t, v.Error.Reason, te.Reason)
				assert.Equal(t, v.Error.Name, te.Name)
				assert.Equal(t, v.Error.Other, te.Other)
				assert.False(t, llmerrors.IsFallbackable(err))
				assert.Zero(t, hits.Load(), "a rejected request reaches no server")
				return
			}
			require.NoError(t, err)
			tools, history := sentToolNames(t, body)
			assert.Equal(t, v.WantTools, tools)
			assert.Equal(t, v.WantHistory, history)
			require.Len(t, calls, len(v.WantCalls))
			for i, want := range v.WantCalls {
				assert.Equal(t, want.ID, calls[i].ID)
				assert.Equal(t, want.Name, calls[i].Name)
			}
		})
	}
}

// minimalSSE is a complete, empty streamed turn.
const minimalSSE = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"m\",\"stop_reason\":null,\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n" +
	"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

func TestConformanceAnthropicToolChoice(t *testing.T) {
	f := loadAnthropicConf(t)
	require.NotEmpty(t, f.AnthropicToolChoice)
	for _, v := range f.AnthropicToolChoice {
		t.Run(v.Name, func(t *testing.T) {
			clearAnthropicEnv(t)
			var body []byte
			var sent atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sent.Add(1)
				body, _ = io.ReadAll(r.Body)
				if v.Call == "stream" || v.Call == "stream_tools" {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, minimalSSE)
					return
				}
				okMessage(w)
			}))
			t.Cleanup(srv.Close)
			p := newConfAdapter(t, srv.URL, "m", "k")
			req := llm.Request{Messages: []llm.Message{{Role: "user", Content: "Hi"}}}
			if c := v.ToolChoice; c != nil {
				req.ToolChoice = &llm.ToolChoice{Mode: llm.ToolChoiceMode(c.Mode), Name: c.Name}
			}
			var tools []llm.ToolDef
			for _, n := range v.Tools {
				tools = append(tools, llm.ToolDef{Name: n})
			}
			var err error
			switch v.Call {
			case "complete":
				_, err = p.(llm.Completer).Complete(context.Background(), req)
			case "stream":
				var it llm.TokenIterator
				if it, err = p.(llm.Streamer).Stream(context.Background(), req); err == nil {
					_ = it.Close()
				}
			case "tools":
				_, err = p.(llm.ToolCaller).CallWithTools(context.Background(), req, tools)
			case "stream_tools":
				var st llm.ToolStream
				if st, err = p.(llm.ToolStreamer).StreamWithTools(context.Background(), req, tools); err == nil {
					_ = st.Close()
				}
			default:
				t.Fatalf("unknown call %q", v.Call)
			}
			if v.Error != "" {
				require.EqualError(t, err, v.Error)
				assert.Zero(t, sent.Load(), "a rejected request is never sent")
				return
			}
			require.NoError(t, err)
			var got struct {
				ToolChoice json.RawMessage `json:"tool_choice"`
			}
			require.NoError(t, json.Unmarshal(body, &got))
			if string(v.WantToolChoice) == "null" || len(v.WantToolChoice) == 0 {
				assert.Empty(t, got.ToolChoice, "no tool_choice sent")
				return
			}
			assert.JSONEq(t, string(v.WantToolChoice), string(got.ToolChoice))
		})
	}
}

func TestConformanceAnthropicHermetic(t *testing.T) {
	f := loadAnthropicConf(t)
	require.NotEmpty(t, f.AnthropicHermetic)
	for _, v := range f.AnthropicHermetic {
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
			p, err := anthropic.New(llm.ResolvedConfig{
				Provider: llm.ProviderConfig{APIKey: "kit-key", BaseURL: v.BaseURL, Model: "m"},
				Hermetic: true,
			})
			require.NoError(t, err)
			_, err = p.(llm.Completer).Complete(context.Background(), llm.Request{
				Messages: []llm.Message{{Role: "user", Content: "Hi"}},
			})
			require.NoError(t, err)
			req := rt.only(t)
			assert.Equal(t, v.WantHost, req.URL.Host)
			for _, h := range v.Sent {
				assert.NotEmpty(t, req.Header.Get(h), h)
			}
			for _, h := range v.Withheld {
				assert.Empty(t, req.Header.Values(h), h)
			}
		})
	}
}

// confUsageJSON is a Usage as the stream event vectors spell it.
func confUsageJSON(u llm.Usage) map[string]any {
	return map[string]any{
		"prompt_tokens": u.PromptTokens, "completion_tokens": u.CompletionTokens,
		"cache_read_tokens": u.CacheReadTokens, "cache_creation_tokens": u.CacheCreationTokens,
		"total_tokens": u.TotalTokens,
	}
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
		return map[string]any{"type": "usage", "usage": confUsageJSON(*tok.Usage)}
	case tok.Thinking != "":
		return map[string]any{"type": "thinking", "thinking": tok.Thinking}
	default:
		return map[string]any{"type": "text", "text": tok.Content}
	}
}

func TestConformanceAnthropicStreamEvents(t *testing.T) {
	f := loadAnthropicConf(t)
	require.NotEmpty(t, f.AnthropicStreamEvents)
	streams := map[string]string{}
	for _, s := range f.AnthropicStream {
		streams["anthropic_stream/"+s.Name] = s.SSE
	}
	for _, v := range f.AnthropicStreamEvents {
		t.Run(v.Name, func(t *testing.T) {
			clearAnthropicEnv(t)
			sse := v.SSE
			if v.SSEFrom != "" {
				var ok bool
				sse, ok = streams[v.SSEFrom]
				require.True(t, ok, "sse_from %q", v.SSEFrom)
			}
			p := newConfAdapter(t, sseServer(t, sse).URL, "m", "k")
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
			if st == nil || streamErr != nil {
				return
			}
			// The events and the outcome tell the same story.
			out, ok := st.Outcome()
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

// unfetchedURL is a URL media source the adapter must pass to the
// provider as a URL: opening it fails the test.
type unfetchedURL struct {
	t   *testing.T
	url string
}

func (u unfetchedURL) URL() string      { return u.url }
func (u unfetchedURL) MimeType() string { return "" }
func (u unfetchedURL) Reader(context.Context) (io.ReadCloser, error) {
	u.t.Errorf("url source %s was fetched; it must be sent as a url source", u.url)
	return nil, errors.New("url source fetched")
}
