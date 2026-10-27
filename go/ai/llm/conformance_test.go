package llm_test

import (
	"bytes"
	"context"
	"encoding/base64"
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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
	"hop.top/kit/go/runtime/bus"
)

// Runs the provider-neutral sections of contracts/llm-v1. The vectors
// are the source of truth: a failure means this implementation drifted
// from the contract, not that a vector should be edited to fit. The
// anthropic_* sections run in go/ai/llm/anthropic.

const llmConfDir = "../../../contracts/llm-v1"

type llmConfFile struct {
	Version     string `json:"version"`
	Description string `json:"description"`
	RedactURI   []struct {
		Name string `json:"name"`
		In   string `json:"in"`
		Want string `json:"want"`
	} `json:"redact_uri"`
	ParseURI []struct {
		Name   string            `json:"name"`
		URI    string            `json:"uri"`
		Scheme string            `json:"scheme"`
		Model  string            `json:"model"`
		Host   string            `json:"host"`
		Params map[string]string `json:"params"`
		Error  string            `json:"error"`
	} `json:"parse_uri"`

	// Consumed by the anthropic runner; decoded here only so the
	// strict decode accepts the file.
	AnthropicRequest      json.RawMessage `json:"anthropic_request"`
	AnthropicRequestError json.RawMessage `json:"anthropic_request_error"`
	AnthropicHeaders      json.RawMessage `json:"anthropic_headers"`
	AnthropicStream       json.RawMessage `json:"anthropic_stream"`
	AnthropicAccumulate   json.RawMessage `json:"anthropic_accumulate"`
	AnthropicError        json.RawMessage `json:"anthropic_error"`
	KeyEndpoint           json.RawMessage `json:"key_endpoint"`
	CredentialHeaders     json.RawMessage `json:"credential_headers"`
	AnthropicLimits       json.RawMessage `json:"anthropic_limits"`
	AnthropicResponse     json.RawMessage `json:"anthropic_response"`
	AnthropicToolNames    json.RawMessage `json:"anthropic_tool_names"`
	AnthropicToolChoice   json.RawMessage `json:"anthropic_tool_choice"`
	AnthropicHermetic     json.RawMessage `json:"anthropic_hermetic"`
	AnthropicStreamEvents json.RawMessage `json:"anthropic_stream_events"`
	ToolNames             []struct {
		Name      string   `json:"name"`
		Rule      string   `json:"rule"`
		Mode      string   `json:"mode"`
		Tools     []string `json:"tools"`
		Want      []string `json:"want"`
		Calls     []string `json:"calls"`
		WantCalls []string `json:"want_calls"`
		Error     *struct {
			Reason string `json:"reason"`
			Name   string `json:"name"`
			Other  string `json:"other"`
		} `json:"error"`
	} `json:"tool_names"`
	BusRequestStarted []struct {
		Name        string          `json:"name"`
		Op          string          `json:"op"`
		Tools       []string        `json:"tools"`
		Request     busConfRequest  `json:"request"`
		WantPayload json.RawMessage `json:"want_payload"`
	} `json:"bus_request_started"`
	BusResponseReceived []struct {
		Name     string `json:"name"`
		Op       string `json:"op"`
		Response struct {
			Content      string           `json:"content"`
			FinishReason string           `json:"finish_reason"`
			Usage        busConfUsage     `json:"usage"`
			Thinking     []busConfThought `json:"thinking"`
		} `json:"response"`
		ToolCalls   []busConfCall   `json:"tool_calls"`
		WantPayload json.RawMessage `json:"want_payload"`
	} `json:"bus_response_received"`
	UsageCost []struct {
		Name  string    `json:"name"`
		Usage confUsage `json:"usage"`
		Price struct {
			Input      float64 `json:"input"`
			Output     float64 `json:"output"`
			CacheRead  float64 `json:"cache_read"`
			CacheWrite float64 `json:"cache_write"`
		} `json:"price"`
		Want float64 `json:"want"`
	} `json:"usage_cost"`
	MediaFetch []confMediaFetch `json:"media_fetch"`
	// Run in go/ai/llm/google.
	GeminiStreamEvents json.RawMessage `json:"gemini_stream_events"`
}

type confMediaFetch struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	MaxBytes int64  `json:"max_bytes"`
	Server   *struct {
		Status   int               `json:"status"`
		Headers  map[string]string `json:"headers"`
		Body     string            `json:"body"`
		BodySize int               `json:"body_size"`
	} `json:"server"`
	ErrorKind string `json:"error_kind"`
	Message   string `json:"message"`
	WantBody  string `json:"want_body"`
	WantSize  int    `json:"want_size"`
	Attempts  *int   `json:"attempts"`
}

func loadLLMConf(t *testing.T) llmConfFile {
	t.Helper()
	entries, err := os.ReadDir(llmConfDir)
	require.NoError(t, err)
	for _, e := range entries {
		if e.Name() != "vectors.json" && e.Name() != "README.md" && e.Name() != "tool-names.md" {
			t.Fatalf("contracts/llm-v1/%s: no runner consumes it", e.Name())
		}
	}
	raw, err := os.ReadFile(filepath.Join(llmConfDir, "vectors.json"))
	require.NoError(t, err)
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var f llmConfFile
	require.NoError(t, dec.Decode(&f))
	require.Equal(t, "v1", f.Version)
	require.NotEmpty(t, f.Description)
	return f
}

func TestConformanceLLMRedactURI(t *testing.T) {
	f := loadLLMConf(t)
	require.NotEmpty(t, f.RedactURI)
	for _, v := range f.RedactURI {
		t.Run(v.Name, func(t *testing.T) {
			assert.Equal(t, v.Want, llm.RedactURI(v.In))
		})
	}
}

func TestConformanceLLMParseURI(t *testing.T) {
	f := loadLLMConf(t)
	require.NotEmpty(t, f.ParseURI)
	for _, v := range f.ParseURI {
		t.Run(v.Name, func(t *testing.T) {
			got, err := llm.ParseURI(v.URI)
			if v.Error != "" {
				require.Error(t, err)
				assert.Equal(t, v.Error, err.Error())
				return
			}
			require.NoError(t, err)
			assert.Equal(t, v.Scheme, got.Scheme)
			assert.Equal(t, v.Model, got.Model)
			assert.Equal(t, v.Host, got.Host)
			assert.Equal(t, v.Params, got.Params)
		})
	}
}

func TestConformanceLLMUsageCost(t *testing.T) {
	f := loadLLMConf(t)
	require.NotEmpty(t, f.UsageCost)
	for _, v := range f.UsageCost {
		t.Run(v.Name, func(t *testing.T) {
			p := llm.Price{Input: v.Price.Input, Output: v.Price.Output, CacheRead: v.Price.CacheRead, CacheWrite: v.Price.CacheWrite}
			assert.InDelta(t, v.Want, v.Usage.toLLM().Cost(p), 1e-9)
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

func (u confUsage) toLLM() llm.Usage { return llm.Usage(u) }

var confToolNameRules = map[string]llm.ToolNameRule{
	"anthropic": llm.AnthropicToolNames,
	"openai":    llm.OpenAIToolNames,
	"gemini":    llm.GeminiToolNames,
}

func TestConformanceLLMToolNames(t *testing.T) {
	f := loadLLMConf(t)
	require.NotEmpty(t, f.ToolNames)
	for _, v := range f.ToolNames {
		t.Run(v.Name, func(t *testing.T) {
			rule, ok := confToolNameRules[v.Rule]
			require.True(t, ok, "unknown rule %q", v.Rule)
			mode := llm.ToolNamesSanitize
			if v.Mode != "" {
				mode = llm.ToolNameMode(v.Mode)
			}
			var defs []llm.ToolDef
			for _, n := range v.Tools {
				defs = append(defs, llm.ToolDef{Name: n})
			}
			names, err := llm.NewToolNames(rule, mode, defs)
			if v.Error != nil {
				var te *llmerrors.ErrToolName
				require.ErrorAs(t, err, &te)
				assert.Equal(t, v.Error.Reason, te.Reason)
				assert.Equal(t, v.Error.Name, te.Name)
				assert.Equal(t, v.Error.Other, te.Other)
				assert.Equal(t, rule.Provider, te.Provider)
				return
			}
			require.NoError(t, err)
			var got []string
			for _, n := range v.Tools {
				got = append(got, names.Wire(n))
			}
			assert.Equal(t, v.Want, got)
			for _, w := range got {
				assert.True(t, rule.Valid(w), "wire name %q invalid for %s", w, v.Rule)
			}
			if mode == llm.ToolNamesSanitize {
				for i, n := range v.Tools {
					s, err := rule.Sanitize(n)
					require.NoError(t, err)
					assert.Equal(t, v.Want[i], s, "Sanitize(%q)", n)
				}
			}
			var back []string
			for _, c := range v.Calls {
				back = append(back, names.Original(c))
			}
			assert.Equal(t, v.WantCalls, back)
		})
	}
}

type busConfThought struct {
	Thinking  string `json:"thinking"`
	Signature string `json:"signature"`
}

type busConfCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type busConfUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	CacheReadTokens     int `json:"cache_read_tokens"`
	CacheCreationTokens int `json:"cache_creation_tokens"`
	TotalTokens         int `json:"total_tokens"`
}

func (u busConfUsage) toLLM() llm.Usage {
	return llm.Usage{
		PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens,
		CacheReadTokens: u.CacheReadTokens, CacheCreationTokens: u.CacheCreationTokens,
		TotalTokens: u.TotalTokens,
	}
}

type busConfRequest struct {
	Model          string         `json:"model"`
	MaxTokens      int            `json:"max_tokens"`
	Temperature    *float64       `json:"temperature"`
	StopSequences  []string       `json:"stop_sequences"`
	ThinkingBudget int            `json:"thinking_budget"`
	Extensions     map[string]any `json:"extensions"`
	Messages       []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
		Parts   []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			MimeType string `json:"mime_type"`
			Data     string `json:"data"`
		} `json:"parts"`
		ToolCalls  []busConfCall    `json:"tool_calls"`
		ToolCallID string           `json:"tool_call_id"`
		Thinking   []busConfThought `json:"thinking"`
	} `json:"messages"`
}

func (r busConfRequest) toLLM(t *testing.T) llm.Request {
	t.Helper()
	out := llm.Request{
		Model: r.Model, MaxTokens: r.MaxTokens, Temperature: r.Temperature,
		StopSequences: r.StopSequences, Extensions: r.Extensions,
	}
	if r.ThinkingBudget > 0 {
		out.Thinking = &llm.ThinkingConfig{BudgetTokens: r.ThinkingBudget}
	}
	for _, m := range r.Messages {
		msg := llm.Message{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID,
			ToolCalls: busCalls(m.ToolCalls), Thinking: busThoughts(m.Thinking)}
		for _, p := range m.Parts {
			part := llm.ContentPart{Type: llm.PartType(p.Type), Text: p.Text, MimeType: p.MimeType}
			if p.Data != "" {
				data, err := base64.StdEncoding.DecodeString(p.Data)
				require.NoError(t, err)
				part.Source = llm.InlineSource(data, p.MimeType)
			}
			msg.Parts = append(msg.Parts, part)
		}
		out.Messages = append(out.Messages, msg)
	}
	return out
}

func busCalls(cs []busConfCall) []llm.ToolCall {
	var out []llm.ToolCall
	for _, c := range cs {
		out = append(out, llm.ToolCall{ID: c.ID, Name: c.Name, Arguments: json.RawMessage(c.Arguments)})
	}
	return out
}

func busThoughts(ts []busConfThought) []llm.ThinkingBlock {
	var out []llm.ThinkingBlock
	for _, tb := range ts {
		out = append(out, llm.ThinkingBlock{Thinking: tb.Thinking, Signature: tb.Signature})
	}
	return out
}

// busConfProvider serves every call kind the bus vectors make.
type busConfProvider struct {
	resp  llm.Response
	tools llm.ToolResponse
}

func (*busConfProvider) Close() error { return nil }
func (p *busConfProvider) Complete(context.Context, llm.Request) (llm.Response, error) {
	return p.resp, nil
}
func (p *busConfProvider) Stream(context.Context, llm.Request) (llm.TokenIterator, error) {
	return &busConfStream{}, nil
}
func (p *busConfProvider) CallWithTools(context.Context, llm.Request, []llm.ToolDef) (llm.ToolResponse, error) {
	return p.tools, nil
}
func (p *busConfProvider) StreamWithTools(context.Context, llm.Request, []llm.ToolDef) (llm.ToolStream, error) {
	return &busConfStream{}, nil
}

type busConfStream struct{}

func (*busConfStream) Next() (llm.Token, error)           { return llm.Token{Done: true}, nil }
func (*busConfStream) Close() error                       { return nil }
func (*busConfStream) Outcome() (llm.StreamOutcome, bool) { return llm.StreamOutcome{}, true }

// busPayloads runs call on a client wired to a bus and returns the JSON
// of each payload published on topic.
func busPayloads(t *testing.T, p llm.Provider, topic bus.Topic, call func(*llm.Client) error) []json.RawMessage {
	t.Helper()
	b := bus.New()
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	var got []json.RawMessage
	b.Subscribe(string(topic), func(_ context.Context, e bus.Event) error {
		raw, err := json.Marshal(e.Payload)
		require.NoError(t, err)
		got = append(got, raw)
		return nil
	})
	require.NoError(t, call(llm.NewClient(p, llm.WithBus(b))))
	return got
}

func TestConformanceLLMBusRequestStarted(t *testing.T) {
	f := loadLLMConf(t)
	require.NotEmpty(t, f.BusRequestStarted)
	for _, v := range f.BusRequestStarted {
		t.Run(v.Name, func(t *testing.T) {
			req := v.Request.toLLM(t)
			var tools []llm.ToolDef
			for _, n := range v.Tools {
				tools = append(tools, llm.ToolDef{Name: n})
			}
			ctx := context.Background()
			got := busPayloads(t, &busConfProvider{}, llm.TopicRequestStart, func(c *llm.Client) error {
				var err error
				switch v.Op {
				case "complete":
					_, err = c.Complete(ctx, req)
				case "stream":
					_, err = c.Stream(ctx, req)
				case "tool_call":
					_, err = c.CallWithTools(ctx, req, tools)
				case "stream_tools":
					_, err = c.StreamWithTools(ctx, req, tools)
				default:
					t.Fatalf("unknown op %q", v.Op)
				}
				return err
			})
			require.Len(t, got, 1)
			assert.JSONEq(t, string(v.WantPayload), string(got[0]))
		})
	}
}

func TestConformanceLLMBusResponseReceived(t *testing.T) {
	f := loadLLMConf(t)
	require.NotEmpty(t, f.BusResponseReceived)
	for _, v := range f.BusResponseReceived {
		t.Run(v.Name, func(t *testing.T) {
			r := v.Response
			p := &busConfProvider{
				resp: llm.Response{Content: r.Content, FinishReason: r.FinishReason,
					Usage: r.Usage.toLLM(), Thinking: busThoughts(r.Thinking)},
				tools: llm.ToolResponse{Content: r.Content, FinishReason: r.FinishReason,
					Usage: r.Usage.toLLM(), Thinking: busThoughts(r.Thinking), ToolCalls: busCalls(v.ToolCalls)},
			}
			ctx := context.Background()
			req := llm.Request{Messages: []llm.Message{{Role: "user", Content: "Hi"}}}
			got := busPayloads(t, p, llm.TopicRequestEnd, func(c *llm.Client) error {
				var err error
				switch v.Op {
				case "complete":
					_, err = c.Complete(ctx, req)
				case "tool_call":
					_, err = c.CallWithTools(ctx, req, []llm.ToolDef{{Name: "get_weather"}})
				default:
					t.Fatalf("unknown op %q", v.Op)
				}
				return err
			})
			require.Len(t, got, 1)
			var payload map[string]any
			require.NoError(t, json.Unmarshal(got[0], &payload))
			ms, ok := payload["duration_ms"].(float64)
			require.True(t, ok, "duration_ms is a number: %s", got[0])
			assert.GreaterOrEqual(t, ms, 0.0)
			assert.Equal(t, ms, float64(int64(ms)), "duration_ms is an integer")
			delete(payload, "duration_ms")
			rest, err := json.Marshal(payload)
			require.NoError(t, err)
			assert.JSONEq(t, string(v.WantPayload), string(rest))
		})
	}
}

// mediaFetchKind names the error a URLSource read failed with, as the
// vectors spell it.
func mediaFetchKind(err error) string {
	var hs *llmerrors.HTTPStatusError
	var tl *llmerrors.ErrMediaTooLarge
	switch {
	case errors.Is(err, llm.ErrMediaURLRefused):
		return "refused"
	case errors.As(err, &tl):
		return "too_large"
	case errors.As(err, &hs) && hs.StatusCode >= 300 && hs.StatusCode < 400:
		return "redirect"
	case strings.Contains(err.Error(), ": http "):
		return "http_status"
	}
	return "other: " + err.Error()
}

func TestConformanceLLMMediaFetch(t *testing.T) {
	f := loadLLMConf(t)
	require.NotEmpty(t, f.MediaFetch)
	for _, v := range f.MediaFetch {
		t.Run(v.Name, func(t *testing.T) {
			raw := v.URL
			var attempts atomic.Int32
			if v.Server != nil {
				require.True(t, strings.HasPrefix(raw, "/"), "a served url is a path")
				s := v.Server
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					attempts.Add(1)
					for k, val := range s.Headers {
						w.Header().Set(k, val)
					}
					w.WriteHeader(s.Status)
					body := s.Body
					if s.BodySize > 0 {
						body = strings.Repeat("x", s.BodySize)
					}
					_, _ = io.WriteString(w, body)
				}))
				t.Cleanup(srv.Close)
				raw = srv.URL + raw
				llm.AllowLoopbackMedia(t)
			}
			var opts []llm.URLSourceOption
			if v.MaxBytes > 0 {
				opts = append(opts, llm.MediaMaxBytes(v.MaxBytes))
			}
			var got []byte
			rc, err := llm.URLSource(raw, opts...).Reader(context.Background())
			if err == nil {
				got, err = io.ReadAll(rc)
				_ = rc.Close()
			}
			if v.Attempts != nil {
				assert.Equal(t, int32(*v.Attempts), attempts.Load(), "requests the server saw")
			}
			if v.ErrorKind != "" {
				require.Error(t, err)
				assert.Equal(t, v.ErrorKind, mediaFetchKind(err))
				if v.Message != "" {
					assert.Equal(t, v.Message, err.Error())
				}
				return
			}
			require.NoError(t, err)
			if v.WantBody != "" {
				assert.Equal(t, v.WantBody, string(got))
			}
			if v.WantSize > 0 {
				assert.Len(t, got, v.WantSize)
			}
		})
	}
}
