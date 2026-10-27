package llm_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/core/redact"
	"hop.top/kit/go/runtime/bus"
)

// fullProvider implements every call kind the bus payloads cover.
type fullProvider struct {
	resp  llm.Response
	tools llm.ToolResponse
}

func (p *fullProvider) Close() error { return nil }
func (p *fullProvider) Complete(context.Context, llm.Request) (llm.Response, error) {
	return p.resp, nil
}
func (p *fullProvider) Stream(context.Context, llm.Request) (llm.TokenIterator, error) {
	return &busMockIterator{tokens: []llm.Token{{Done: true}}}, nil
}
func (p *fullProvider) CallWithTools(context.Context, llm.Request, []llm.ToolDef) (llm.ToolResponse, error) {
	return p.tools, nil
}

// capture subscribes to topic and returns what it receives.
func capture(t *testing.T, b bus.Bus, topic string) func() []any {
	t.Helper()
	var mu sync.Mutex
	var got []any
	b.Subscribe(topic, func(_ context.Context, e bus.Event) error {
		mu.Lock()
		got = append(got, e.Payload)
		mu.Unlock()
		return nil
	})
	return func() []any {
		mu.Lock()
		defer mu.Unlock()
		return append([]any(nil), got...)
	}
}

const secretPrompt = "my password is hunter2 and token AKIAIOSFODNN7EXAMPLE"

func secretRequest() llm.Request {
	temp := 0.0
	return llm.Request{
		Model:       "m",
		Temperature: &temp,
		MaxTokens:   64,
		Messages: []llm.Message{
			{Role: "system", Content: "be brief"},
			{Role: "user", Content: secretPrompt},
		},
		Extensions: map[string]any{"b": 1, "a": "x"},
	}
}

func TestRequestStarted_MetadataOnlyByDefault(t *testing.T) {
	b := bus.New()
	defer func() { _ = b.Close(context.Background()) }()
	got := capture(t, b, string(llm.TopicRequestStart))
	ended := capture(t, b, string(llm.TopicRequestEnd))

	c := llm.NewClient(&fullProvider{resp: llm.Response{
		Content: "the answer is hunter2", FinishReason: "end_turn",
		Usage: llm.Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7},
	}}, llm.WithBus(b))
	_, err := c.Complete(context.Background(), secretRequest())
	require.NoError(t, err)

	require.Len(t, got(), 1)
	p, ok := got()[0].(llm.RequestStartPayload)
	require.True(t, ok, "payload type %T", got()[0])
	assert.Nil(t, p.Request, "no content by default")
	assert.Equal(t, "complete", p.Op)
	assert.Equal(t, 2, p.Messages)
	assert.Equal(t, map[string]int{"system": 1, "user": 1}, p.Roles)
	assert.Equal(t, len("be brief")+len(secretPrompt), p.ContentBytes)
	assert.Equal(t, []string{"a", "b"}, p.ExtensionKeys)
	raw, err := json.Marshal(p)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "hunter2")

	require.Len(t, ended(), 1)
	e, ok := ended()[0].(llm.RequestEndPayload)
	require.True(t, ok)
	assert.Nil(t, e.Response, "no content by default")
	assert.Equal(t, "end_turn", e.FinishReason)
	assert.Equal(t, 7, e.Usage.TotalTokens)
	assert.Equal(t, len("the answer is hunter2"), e.ContentBytes)
	raw, err = json.Marshal(e)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "hunter2")
}

func TestHooks_KeepFullContentInProcess(t *testing.T) {
	var req llm.Request
	var resp llm.Response
	c := llm.NewClient(&fullProvider{resp: llm.Response{Content: "secret answer"}},
		llm.OnRequest(func(r llm.Request) { req = r }),
		llm.OnResponse(func(r llm.Response, _ time.Duration) { resp = r }),
	)
	_, err := c.Complete(context.Background(), secretRequest())
	require.NoError(t, err)
	assert.Equal(t, secretPrompt, req.Messages[1].Content)
	assert.Equal(t, "secret answer", resp.Content)
}

func TestWithRequestContent_OptsIn(t *testing.T) {
	b := bus.New()
	defer func() { _ = b.Close(context.Background()) }()
	got := capture(t, b, string(llm.TopicRequestStart))
	ended := capture(t, b, string(llm.TopicRequestEnd))

	in := secretRequest()
	c := llm.NewClient(&fullProvider{resp: llm.Response{Content: "answer hunter2"}}, llm.WithBus(b),
		llm.WithRequestContent(func(r llm.Request) llm.Request {
			r.Messages[1].Content = "[cut]"
			return r
		}),
		llm.WithResponseContent(nil),
	)
	_, err := c.Complete(context.Background(), in)
	require.NoError(t, err)
	p := got()[0].(llm.RequestStartPayload)
	require.NotNil(t, p.Request)
	assert.Equal(t, "[cut]", p.Request.Messages[1].Content)
	assert.Equal(t, secretPrompt, in.Messages[1].Content, "the caller's request is untouched")
	e := ended()[0].(llm.RequestEndPayload)
	require.NotNil(t, e.Response, "a nil transform publishes content as is")
	assert.Equal(t, "answer hunter2", e.Response.Content)
}

func TestRedactRequestAndResponse(t *testing.T) {
	r := redact.New()
	_, err := r.AddRule("pw", `hunter2`, "[REDACTED]")
	require.NoError(t, err)

	b := bus.New()
	defer func() { _ = b.Close(context.Background()) }()
	got := capture(t, b, string(llm.TopicRequestStart))
	ended := capture(t, b, string(llm.TopicRequestEnd))

	req := secretRequest()
	req.Messages = append(req.Messages, llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{
		{ID: "c1", Name: "login", Arguments: json.RawMessage(`{"pw":"hunter2"}`)},
	}}, llm.Message{Role: "user", Parts: []llm.ContentPart{
		{Type: llm.PartTypeText, Text: "again hunter2"},
		{Type: llm.PartTypeImage, MimeType: "image/png", Source: llm.InlineSource([]byte("png"), "image/png")},
	}})
	c := llm.NewClient(&fullProvider{tools: llm.ToolResponse{Content: "ok hunter2", ToolCalls: []llm.ToolCall{
		{ID: "c2", Name: "login", Arguments: json.RawMessage(`{"pw":"hunter2"}`)},
	}}}, llm.WithBus(b),
		llm.WithRequestContent(llm.RedactRequest(r)),
		llm.WithResponseContent(llm.RedactResponse(r)),
	)
	_, err = c.CallWithTools(context.Background(), req, []llm.ToolDef{{Name: "login"}})
	require.NoError(t, err)

	p := got()[0].(llm.RequestStartPayload)
	require.NotNil(t, p.Request)
	raw, err := json.Marshal(p)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "hunter2")
	assert.Contains(t, string(raw), "REDACTED")
	assert.Nil(t, p.Request.Extensions, "extensions are not published")
	assert.Nil(t, p.Request.Messages[3].Parts[1].Source, "media bytes are not published")
	assert.True(t, json.Valid(p.Request.Messages[2].ToolCalls[0].Arguments))
	assert.Equal(t, 1, p.Tools)
	assert.Equal(t, "tool_call", p.Op)
	assert.Equal(t, map[string]int{"text": 1, "image": 1}, p.Parts)
	assert.Contains(t, req.Messages[1].Content, "hunter2", "the caller's request is untouched")

	e := ended()[0].(llm.RequestEndPayload)
	raw, err = json.Marshal(e)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "hunter2")
	assert.Equal(t, 1, e.ToolCalls)
	assert.True(t, strings.Contains(e.Response.Content, "REDACTED"))
}

func TestStream_RequestStartedIsMetadata(t *testing.T) {
	b := bus.New()
	defer func() { _ = b.Close(context.Background()) }()
	got := capture(t, b, string(llm.TopicRequestStart))
	c := llm.NewClient(&fullProvider{}, llm.WithBus(b))
	it, err := c.Stream(context.Background(), secretRequest())
	require.NoError(t, err)
	_ = it.Close()
	p := got()[0].(llm.RequestStartPayload)
	assert.Equal(t, "stream", p.Op)
	assert.Nil(t, p.Request)
}
