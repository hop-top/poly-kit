package llm_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
	"hop.top/kit/go/runtime/bus"
)

// ---------------------------------------------------------------------------
// Mock adapters
// ---------------------------------------------------------------------------

// mockProvider implements Provider (base interface).
type mockProvider struct{}

func (m *mockProvider) Close() error { return nil }

// mockCompleter implements Provider + Completer.
type mockCompleter struct {
	mockProvider
	resp llm.Response
	err  error
}

func (m *mockCompleter) Complete(_ context.Context, _ llm.Request) (llm.Response, error) {
	return m.resp, m.err
}

// mockStreamer implements Provider + Completer + Streamer.
type mockStreamer struct {
	mockCompleter
	iter llm.TokenIterator
	serr error
}

func (m *mockStreamer) Stream(_ context.Context, _ llm.Request) (llm.TokenIterator, error) {
	return m.iter, m.serr
}

// mockToolCaller implements Provider + Completer + ToolCaller.
type mockToolCaller struct {
	mockCompleter
	toolResp llm.ToolResponse
	toolErr  error
}

func (m *mockToolCaller) CallWithTools(
	_ context.Context, _ llm.Request, _ []llm.ToolDef,
) (llm.ToolResponse, error) {
	return m.toolResp, m.toolErr
}

// mockFullProvider implements all three capabilities.
type mockFullProvider struct {
	mockProvider
	completeResp llm.Response
	streamIter   llm.TokenIterator
	toolResp     llm.ToolResponse
}

func (m *mockFullProvider) Complete(_ context.Context, _ llm.Request) (llm.Response, error) {
	return m.completeResp, nil
}

func (m *mockFullProvider) Stream(_ context.Context, _ llm.Request) (llm.TokenIterator, error) {
	return m.streamIter, nil
}

func (m *mockFullProvider) CallWithTools(
	_ context.Context, _ llm.Request, _ []llm.ToolDef,
) (llm.ToolResponse, error) {
	return m.toolResp, nil
}

// mockTokenIterator is a simple token iterator for tests.
type mockTokenIterator struct {
	tokens []llm.Token
	pos    int
}

func (m *mockTokenIterator) Next() (llm.Token, error) {
	if m.pos >= len(m.tokens) {
		return llm.Token{Done: true}, nil
	}
	t := m.tokens[m.pos]
	m.pos++
	return t, nil
}

func (m *mockTokenIterator) Close() error { return nil }

// ---------------------------------------------------------------------------
// Registry tests
// ---------------------------------------------------------------------------

func TestRegister_ResolveByScheme(t *testing.T) {
	reg := llm.NewRegistry()
	reg.Register("mock", func(cfg llm.ResolvedConfig) (llm.Provider, error) {
		return &mockCompleter{
			resp: llm.Response{Content: "hello"},
		}, nil
	})

	p, err := reg.Resolve("mock://model-x")
	require.NoError(t, err)
	assert.NotNil(t, p)

	c, ok := p.(llm.Completer)
	require.True(t, ok)
	resp, err := c.Complete(context.Background(), llm.Request{})
	require.NoError(t, err)
	assert.Equal(t, "hello", resp.Content)
}

func TestRegister_UnknownScheme(t *testing.T) {
	reg := llm.NewRegistry()
	_, err := reg.Resolve("unknown://foo")

	var pnf *llmerrors.ErrProviderNotFound
	assert.ErrorAs(t, err, &pnf)
	assert.Equal(t, "unknown", pnf.Scheme)
}

func TestRegister_DuplicatePanics(t *testing.T) {
	reg := llm.NewRegistry()
	factory := func(cfg llm.ResolvedConfig) (llm.Provider, error) {
		return &mockProvider{}, nil
	}
	reg.Register("dup", factory)
	assert.Panics(t, func() {
		reg.Register("dup", factory)
	})
}

// ---------------------------------------------------------------------------
// Client.Complete() delegation
// ---------------------------------------------------------------------------

func TestClient_Complete_DelegatesToAdapter(t *testing.T) {
	want := llm.Response{Content: "world", Role: "assistant"}
	adapter := &mockCompleter{resp: want}

	client := llm.NewClient(adapter)
	got, err := client.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

// ---------------------------------------------------------------------------
// Client.Stream() — ErrCapabilityNotSupported
// ---------------------------------------------------------------------------

func TestClient_Stream_UnsupportedReturnsError(t *testing.T) {
	adapter := &mockCompleter{}
	client := llm.NewClient(adapter)

	_, err := client.Stream(context.Background(), llm.Request{})
	var capErr *llmerrors.ErrCapabilityNotSupported
	assert.ErrorAs(t, err, &capErr)
}

func TestClient_Stream_Supported(t *testing.T) {
	iter := &mockTokenIterator{
		tokens: []llm.Token{{Content: "hi"}, {Content: " there", Done: true}},
	}
	adapter := &mockStreamer{iter: iter}
	client := llm.NewClient(adapter)

	got, err := client.Stream(context.Background(), llm.Request{})
	require.NoError(t, err)
	assert.NotNil(t, got)
}

// ---------------------------------------------------------------------------
// Client.CallWithTools() — ErrCapabilityNotSupported
// ---------------------------------------------------------------------------

func TestClient_CallWithTools_UnsupportedReturnsError(t *testing.T) {
	adapter := &mockCompleter{}
	client := llm.NewClient(adapter)

	_, err := client.CallWithTools(context.Background(), llm.Request{}, nil)
	var capErr *llmerrors.ErrCapabilityNotSupported
	assert.ErrorAs(t, err, &capErr)
}

// ---------------------------------------------------------------------------
// Client.Capabilities()
// ---------------------------------------------------------------------------

func TestClient_Capabilities_CompleterOnly(t *testing.T) {
	client := llm.NewClient(&mockCompleter{})
	caps := client.Capabilities()
	assert.Contains(t, caps, "complete")
	assert.NotContains(t, caps, "stream")
	assert.NotContains(t, caps, "tool_call")
}

func TestClient_Capabilities_Full(t *testing.T) {
	client := llm.NewClient(&mockFullProvider{})
	caps := client.Capabilities()
	assert.Contains(t, caps, "complete")
	assert.Contains(t, caps, "stream")
	assert.Contains(t, caps, "tool_call")
}

func TestClient_Capabilities_BaseOnly(t *testing.T) {
	client := llm.NewClient(&mockProvider{})
	caps := client.Capabilities()
	assert.Empty(t, caps)
}

// ---------------------------------------------------------------------------
// Client.Provider() returns underlying adapter
// ---------------------------------------------------------------------------

func TestClient_Provider_ReturnsUnderlying(t *testing.T) {
	adapter := &mockCompleter{}
	client := llm.NewClient(adapter)
	assert.Equal(t, adapter, client.Provider())
}

// ---------------------------------------------------------------------------
// Fallback chain — 5xx triggers fallback
// ---------------------------------------------------------------------------

func TestFallback_5xxTriggersNext(t *testing.T) {
	primary := &mockCompleter{
		err: llmerrors.NewHTTPStatusError(500, "internal"),
	}
	fallback := &mockCompleter{
		resp: llm.Response{Content: "from-fallback"},
	}

	client := llm.NewClient(primary, llm.WithFallback(fallback))

	got, err := client.Complete(context.Background(), llm.Request{})
	require.NoError(t, err)
	assert.Equal(t, "from-fallback", got.Content)
}

// ---------------------------------------------------------------------------
// Fallback chain — 4xx does NOT trigger fallback
// ---------------------------------------------------------------------------

func TestFallback_4xxDoesNotTrigger(t *testing.T) {
	primary := &mockCompleter{
		err: llmerrors.NewAuth("openai", fmt.Errorf("bad key")),
	}
	fallback := &mockCompleter{
		resp: llm.Response{Content: "should-not-reach"},
	}

	client := llm.NewClient(primary, llm.WithFallback(fallback))

	_, err := client.Complete(context.Background(), llm.Request{})
	var authErr *llmerrors.ErrAuth
	assert.ErrorAs(t, err, &authErr)
}

// ---------------------------------------------------------------------------
// Fallback chain — all exhausted
// ---------------------------------------------------------------------------

func TestFallback_AllExhausted(t *testing.T) {
	primary := &mockCompleter{
		err: llmerrors.NewHTTPStatusError(502, "bad gateway"),
	}
	fb := &mockCompleter{
		err: llmerrors.NewHTTPStatusError(503, "unavailable"),
	}

	client := llm.NewClient(primary, llm.WithFallback(fb))

	_, err := client.Complete(context.Background(), llm.Request{})
	var fe *llmerrors.ErrFallbackExhausted
	assert.ErrorAs(t, err, &fe)
	assert.Len(t, fe.Errors, 2)
}

// ---------------------------------------------------------------------------
// Event hooks
// ---------------------------------------------------------------------------

func TestHooks_OnRequest_OnResponse(t *testing.T) {
	var (
		mu          sync.Mutex
		reqFired    bool
		respFired   bool
		respContent string
		respDur     time.Duration
	)

	adapter := &mockCompleter{
		resp: llm.Response{Content: "ok"},
	}

	client := llm.NewClient(adapter,
		llm.OnRequest(func(req llm.Request) {
			mu.Lock()
			defer mu.Unlock()
			reqFired = true
		}),
		llm.OnResponse(func(resp llm.Response, dur time.Duration) {
			mu.Lock()
			defer mu.Unlock()
			respFired = true
			respContent = resp.Content
			respDur = dur
		}),
	)

	_, err := client.Complete(context.Background(), llm.Request{})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	assert.True(t, reqFired)
	assert.True(t, respFired)
	assert.Equal(t, "ok", respContent)
	assert.GreaterOrEqual(t, respDur, time.Duration(0))
}

func TestHooks_OnError(t *testing.T) {
	var (
		mu       sync.Mutex
		errFired bool
		gotErr   error
	)

	adapter := &mockCompleter{
		err: llmerrors.NewAuth("openai", fmt.Errorf("bad key")),
	}

	client := llm.NewClient(adapter,
		llm.OnError(func(err error) {
			mu.Lock()
			defer mu.Unlock()
			errFired = true
			gotErr = err
		}),
	)

	_, _ = client.Complete(context.Background(), llm.Request{})

	mu.Lock()
	defer mu.Unlock()
	assert.True(t, errFired)
	var authErr *llmerrors.ErrAuth
	assert.ErrorAs(t, gotErr, &authErr)
}

func TestHooks_OnFallback(t *testing.T) {
	var (
		mu      sync.Mutex
		fbFired bool
		fromIdx int
		toIdx   int
		fbErr   error
	)

	primary := &mockCompleter{
		err: llmerrors.NewHTTPStatusError(500, "fail"),
	}
	fb := &mockCompleter{
		resp: llm.Response{Content: "recovered"},
	}

	client := llm.NewClient(primary,
		llm.WithFallback(fb),
		llm.OnFallback(func(from, to int, err error) {
			mu.Lock()
			defer mu.Unlock()
			fbFired = true
			fromIdx = from
			toIdx = to
			fbErr = err
		}),
	)

	got, err := client.Complete(context.Background(), llm.Request{})
	require.NoError(t, err)
	assert.Equal(t, "recovered", got.Content)

	mu.Lock()
	defer mu.Unlock()
	assert.True(t, fbFired)
	assert.Equal(t, 0, fromIdx)
	assert.Equal(t, 1, toIdx)
	assert.NotNil(t, fbErr)
}

// ---------------------------------------------------------------------------
// Request / Response / ToolDef struct tests
// ---------------------------------------------------------------------------

func TestRequest_Extensions(t *testing.T) {
	req := llm.Request{
		Messages:    []llm.Message{{Role: "user", Content: "test"}},
		Model:       "gpt-4",
		Temperature: 0.7,
		MaxTokens:   100,
		Extensions:  map[string]any{"custom": true},
	}
	assert.Equal(t, "gpt-4", req.Model)
	assert.Equal(t, true, req.Extensions["custom"])
}

func TestToolDef_ParametersIsRawJSON(t *testing.T) {
	raw := json.RawMessage(`{"type":"object"}`)
	td := llm.ToolDef{
		Name:        "search",
		Description: "search the web",
		Parameters:  raw,
	}
	assert.Equal(t, "search", td.Name)
	assert.JSONEq(t, `{"type":"object"}`, string(td.Parameters))
}

// ---------------------------------------------------------------------------
// Registry URI parsing
// ---------------------------------------------------------------------------

func TestResolve_PassesConfigToFactory(t *testing.T) {
	reg := llm.NewRegistry()
	var got llm.ResolvedConfig

	reg.Register("test", func(cfg llm.ResolvedConfig) (llm.Provider, error) {
		got = cfg
		return &mockProvider{}, nil
	})

	_, err := reg.Resolve("test://my-model")
	require.NoError(t, err)
	assert.Equal(t, "test", got.URI.Scheme)
	assert.Equal(t, "my-model", got.URI.Model)
	assert.Equal(t, "my-model", got.Provider.Model)
}

// ---------------------------------------------------------------------------
// Fallback with network error (fallbackable)
// ---------------------------------------------------------------------------

func TestFallback_NetworkErrorTriggersNext(t *testing.T) {
	primary := &mockCompleter{
		err: &net.OpError{
			Op:  "dial",
			Net: "tcp",
			Err: fmt.Errorf("connection refused"),
		},
	}
	fb := &mockCompleter{
		resp: llm.Response{Content: "recovered"},
	}

	client := llm.NewClient(primary, llm.WithFallback(fb))

	got, err := client.Complete(context.Background(), llm.Request{})
	require.NoError(t, err)
	assert.Equal(t, "recovered", got.Content)
}

// ---------------------------------------------------------------------------
// Fallback with rate limit (fallbackable)
// ---------------------------------------------------------------------------

func TestFallback_RateLimitTriggersNext(t *testing.T) {
	primary := &mockCompleter{
		err: llmerrors.NewRateLimit("openai", 5*time.Second),
	}
	fb := &mockCompleter{
		resp: llm.Response{Content: "ok"},
	}

	client := llm.NewClient(primary, llm.WithFallback(fb))

	got, err := client.Complete(context.Background(), llm.Request{})
	require.NoError(t, err)
	assert.Equal(t, "ok", got.Content)
}

// ---------------------------------------------------------------------------
// Stream fallback
// ---------------------------------------------------------------------------

func TestStream_Fallback_5xxTriggersNext(t *testing.T) {
	primary := &mockStreamer{
		serr: llmerrors.NewHTTPStatusError(500, "internal"),
	}
	iter := &mockTokenIterator{
		tokens: []llm.Token{{Content: "hi", Done: true}},
	}
	fb := &mockStreamer{iter: iter}

	client := llm.NewClient(primary, llm.WithFallback(fb))

	got, err := client.Stream(context.Background(), llm.Request{})
	require.NoError(t, err)
	assert.NotNil(t, got)
}

func TestStream_Fallback_4xxDoesNotTrigger(t *testing.T) {
	primary := &mockStreamer{
		serr: llmerrors.NewAuth("openai", fmt.Errorf("bad key")),
	}
	fb := &mockStreamer{
		iter: &mockTokenIterator{},
	}

	client := llm.NewClient(primary, llm.WithFallback(fb))

	_, err := client.Stream(context.Background(), llm.Request{})
	var authErr *llmerrors.ErrAuth
	assert.ErrorAs(t, err, &authErr)
}

// ---------------------------------------------------------------------------
// CallWithTools fallback
// ---------------------------------------------------------------------------

func TestCallWithTools_Fallback_5xxTriggersNext(t *testing.T) {
	primary := &mockToolCaller{
		toolErr: llmerrors.NewHTTPStatusError(500, "internal"),
	}
	fb := &mockToolCaller{
		toolResp: llm.ToolResponse{Content: "tool-ok"},
	}

	client := llm.NewClient(primary, llm.WithFallback(fb))

	got, err := client.CallWithTools(context.Background(), llm.Request{}, nil)
	require.NoError(t, err)
	assert.Equal(t, "tool-ok", got.Content)
}

func TestCallWithTools_Fallback_4xxDoesNotTrigger(t *testing.T) {
	primary := &mockToolCaller{
		toolErr: llmerrors.NewAuth("openai", fmt.Errorf("bad key")),
	}
	fb := &mockToolCaller{
		toolResp: llm.ToolResponse{Content: "should-not-reach"},
	}

	client := llm.NewClient(primary, llm.WithFallback(fb))

	_, err := client.CallWithTools(context.Background(), llm.Request{}, nil)
	var authErr *llmerrors.ErrAuth
	assert.ErrorAs(t, err, &authErr)
}

// ---------------------------------------------------------------------------
// Multimodal backward-compat: Content string still works (no Parts)
// ---------------------------------------------------------------------------

type mockCompleterCapture struct {
	mockProvider
	lastReq llm.Request
	resp    llm.Response
}

func (m *mockCompleterCapture) Complete(_ context.Context, req llm.Request) (llm.Response, error) {
	m.lastReq = req
	return m.resp, nil
}

func TestMultimodal_BackwardCompat_ContentString(t *testing.T) {
	adapter := &mockCompleterCapture{
		resp: llm.Response{Content: "ok"},
	}
	client := llm.NewClient(adapter)

	req := llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	}
	_, err := client.Complete(context.Background(), req)
	require.NoError(t, err)

	got := adapter.lastReq.Messages[0]
	assert.Equal(t, "hi", got.Content)
	assert.Empty(t, got.Parts)
}

// ---------------------------------------------------------------------------
// ContentPart flow: image part forwarded to adapter
// ---------------------------------------------------------------------------

func TestMultimodal_ContentPart_ForwardedToAdapter(t *testing.T) {
	adapter := &mockCompleterCapture{
		resp: llm.Response{Content: "saw image"},
	}
	client := llm.NewClient(adapter)

	imgPart := llm.ContentPart{
		Type:     llm.PartTypeImage,
		MimeType: "image/png",
		Source:   llm.InlineSource([]byte{0x89, 0x50}, "image/png"),
	}
	req := llm.Request{
		Messages: []llm.Message{{
			Role:  "user",
			Parts: []llm.ContentPart{imgPart},
		}},
	}
	_, err := client.Complete(context.Background(), req)
	require.NoError(t, err)

	parts := adapter.lastReq.Messages[0].Parts
	require.Len(t, parts, 1)
	assert.Equal(t, llm.PartTypeImage, parts[0].Type)
	assert.Equal(t, "image/png", parts[0].MimeType)
}

// ---------------------------------------------------------------------------
// GenerateImage fallback chain: primary lacks cap, fallback fires
// ---------------------------------------------------------------------------

type mockImageGenerator struct {
	mockProvider
	resp llm.ImageResponse
	err  error
}

func (m *mockImageGenerator) GenerateImage(
	_ context.Context, _ llm.ImageRequest,
) (llm.ImageResponse, error) {
	return m.resp, m.err
}

func TestGenerateImage_FallbackChain(t *testing.T) {
	var (
		mu      sync.Mutex
		fbFired bool
		fromIdx int
		toIdx   int
	)

	// primary: ImageGenerator that returns a fallbackable 5xx error
	primary := &mockImageGenerator{
		err: llmerrors.NewHTTPStatusError(503, "unavailable"),
	}

	imgResp := llm.ImageResponse{
		Images: []llm.ContentPart{{Type: llm.PartTypeImage}},
	}
	fb := &mockImageGenerator{resp: imgResp}

	client := llm.NewClient(primary,
		llm.WithFallback(fb),
		llm.OnFallback(func(from, to int, err error) {
			mu.Lock()
			defer mu.Unlock()
			fbFired = true
			fromIdx = from
			toIdx = to
		}),
	)

	got, err := client.GenerateImage(context.Background(), llm.ImageRequest{
		Prompt: "a cat",
	})
	require.NoError(t, err)
	require.Len(t, got.Images, 1)

	mu.Lock()
	defer mu.Unlock()
	assert.True(t, fbFired)
	assert.Equal(t, 0, fromIdx)
	assert.Equal(t, 1, toIdx)
}

func TestGenerateImage_PrimaryNoCapability_FallbackUsed(t *testing.T) {
	// primary: Completer only — no ImageGenerator
	primary := &mockCompleter{}

	imgResp := llm.ImageResponse{
		Images: []llm.ContentPart{{Type: llm.PartTypeImage}},
	}
	fb := &mockImageGenerator{resp: imgResp}

	client := llm.NewClient(primary, llm.WithFallback(fb))

	got, err := client.GenerateImage(context.Background(), llm.ImageRequest{
		Prompt: "a cat",
	})
	require.NoError(t, err)
	require.Len(t, got.Images, 1)
	assert.Equal(t, llm.PartTypeImage, got.Images[0].Type)
}

// ---------------------------------------------------------------------------
// Transcribe: mock Transcriber, hooks fire
// ---------------------------------------------------------------------------

type mockTranscriber struct {
	mockProvider
	resp llm.TranscribeResponse
	err  error
}

func (m *mockTranscriber) Transcribe(
	_ context.Context, _ llm.TranscribeRequest,
) (llm.TranscribeResponse, error) {
	return m.resp, m.err
}

func TestTranscribe_HooksAndResponse(t *testing.T) {
	var (
		mu        sync.Mutex
		reqFired  bool
		respFired bool
		gotText   string
	)

	adapter := &mockTranscriber{
		resp: llm.TranscribeResponse{Text: "hello world"},
	}
	client := llm.NewClient(adapter,
		llm.OnRequest(func(_ llm.Request) {
			mu.Lock()
			defer mu.Unlock()
			reqFired = true
		}),
		llm.OnResponse(func(resp llm.Response, _ time.Duration) {
			mu.Lock()
			defer mu.Unlock()
			respFired = true
			gotText = resp.Content
		}),
	)

	got, err := client.Transcribe(context.Background(), llm.TranscribeRequest{
		Source: llm.InlineSource([]byte("audio"), "audio/wav"),
	})
	require.NoError(t, err)
	assert.Equal(t, "hello world", got.Text)

	mu.Lock()
	defer mu.Unlock()
	assert.True(t, reqFired)
	assert.True(t, respFired)
	assert.Equal(t, "hello world", gotText)
}

// ---------------------------------------------------------------------------
// Synthesize: mock SpeechSynthesizer, hooks fire
// ---------------------------------------------------------------------------

type mockSynthesizer struct {
	mockProvider
	resp llm.SynthesizeResponse
	err  error
}

func (m *mockSynthesizer) Synthesize(
	_ context.Context, _ llm.SynthesizeRequest,
) (llm.SynthesizeResponse, error) {
	return m.resp, m.err
}

func TestSynthesize_HooksAndResponse(t *testing.T) {
	var (
		mu        sync.Mutex
		reqFired  bool
		respFired bool
	)

	audioPart := llm.ContentPart{Type: llm.PartTypeAudio, MimeType: "audio/mp3"}
	adapter := &mockSynthesizer{
		resp: llm.SynthesizeResponse{Audio: audioPart},
	}
	client := llm.NewClient(adapter,
		llm.OnRequest(func(_ llm.Request) {
			mu.Lock()
			defer mu.Unlock()
			reqFired = true
		}),
		llm.OnResponse(func(resp llm.Response, _ time.Duration) {
			mu.Lock()
			defer mu.Unlock()
			respFired = true
		}),
	)

	got, err := client.Synthesize(context.Background(), llm.SynthesizeRequest{
		Text: "hello",
	})
	require.NoError(t, err)
	assert.Equal(t, llm.PartTypeAudio, got.Audio.Type)

	mu.Lock()
	defer mu.Unlock()
	assert.True(t, reqFired)
	assert.True(t, respFired)
}

// ---------------------------------------------------------------------------
// Non-fallbackable error types: all return immediately
// ---------------------------------------------------------------------------

func TestNonFallbackable_UnsupportedModality(t *testing.T) {
	primary := &mockCompleter{
		err: llmerrors.NewUnsupportedModality("image", "test", fmt.Errorf("no image")),
	}
	fb := &mockCompleter{resp: llm.Response{Content: "should-not-reach"}}

	client := llm.NewClient(primary, llm.WithFallback(fb))
	_, err := client.Complete(context.Background(), llm.Request{})

	var modalErr *llmerrors.ErrUnsupportedModality
	assert.ErrorAs(t, err, &modalErr)
	assert.Equal(t, "image", modalErr.Modality)
}

func TestNonFallbackable_MediaTooLarge(t *testing.T) {
	primary := &mockCompleter{
		err: llmerrors.NewMediaTooLarge(1024, 512, "test", fmt.Errorf("too big")),
	}
	fb := &mockCompleter{resp: llm.Response{Content: "should-not-reach"}}

	client := llm.NewClient(primary, llm.WithFallback(fb))
	_, err := client.Complete(context.Background(), llm.Request{})

	var sizeErr *llmerrors.ErrMediaTooLarge
	assert.ErrorAs(t, err, &sizeErr)
	assert.Equal(t, int64(1024), sizeErr.Size)
}

func TestNonFallbackable_InvalidFormat(t *testing.T) {
	primary := &mockCompleter{
		err: llmerrors.NewInvalidFormat("bmp", "test", fmt.Errorf("unsupported")),
	}
	fb := &mockCompleter{resp: llm.Response{Content: "should-not-reach"}}

	client := llm.NewClient(primary, llm.WithFallback(fb))
	_, err := client.Complete(context.Background(), llm.Request{})

	var fmtErr *llmerrors.ErrInvalidFormat
	assert.ErrorAs(t, err, &fmtErr)
	assert.Equal(t, "bmp", fmtErr.Format)
}

// ---------------------------------------------------------------------------
// OutputParts in OnResponse hook
// ---------------------------------------------------------------------------

func TestOnResponse_ReceivesOutputParts(t *testing.T) {
	var (
		mu       sync.Mutex
		gotParts []llm.ContentPart
	)

	imgPart := llm.ContentPart{Type: llm.PartTypeImage, MimeType: "image/png"}
	adapter := &mockImageGenerator{
		resp: llm.ImageResponse{
			Images: []llm.ContentPart{imgPart},
		},
	}
	client := llm.NewClient(adapter,
		llm.OnResponse(func(resp llm.Response, _ time.Duration) {
			mu.Lock()
			defer mu.Unlock()
			gotParts = resp.OutputParts
		}),
	)

	_, err := client.GenerateImage(context.Background(), llm.ImageRequest{
		Prompt: "sunset",
	})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, gotParts, 1)
	assert.Equal(t, llm.PartTypeImage, gotParts[0].Type)
}

// ---------------------------------------------------------------------------
// Multiple OnRequest handlers (bus supports multiple subscribers)
// ---------------------------------------------------------------------------

func TestHooks_MultipleOnRequest_BothFire(t *testing.T) {
	var (
		mu     sync.Mutex
		first  bool
		second bool
	)

	adapter := &mockCompleter{resp: llm.Response{Content: "ok"}}

	client := llm.NewClient(adapter,
		llm.OnRequest(func(_ llm.Request) {
			mu.Lock()
			defer mu.Unlock()
			first = true
		}),
		llm.OnRequest(func(_ llm.Request) {
			mu.Lock()
			defer mu.Unlock()
			second = true
		}),
	)

	_, err := client.Complete(context.Background(), llm.Request{})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	assert.True(t, first, "first OnRequest handler should fire")
	assert.True(t, second, "second OnRequest handler should fire")
}

// ---------------------------------------------------------------------------
// WithBus + OnRequest both receive events
// ---------------------------------------------------------------------------

func TestHooks_WithBus_PlusOnRequest_BothReceive(t *testing.T) {
	var (
		mu          sync.Mutex
		busReceived bool
		onReqFired  bool
	)

	b := bus.New()
	b.Subscribe(string(llm.TopicRequestStart),
		func(_ context.Context, e bus.Event) error {
			mu.Lock()
			defer mu.Unlock()
			busReceived = true
			return nil
		})

	adapter := &mockCompleter{resp: llm.Response{Content: "ok"}}

	client := llm.NewClient(adapter,
		llm.WithBus(b),
		llm.OnRequest(func(_ llm.Request) {
			mu.Lock()
			defer mu.Unlock()
			onReqFired = true
		}),
	)

	_, err := client.Complete(context.Background(), llm.Request{})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	assert.True(t, busReceived, "direct bus subscriber should receive event")
	assert.True(t, onReqFired, "OnRequest handler should fire via bus")
}

// ---------------------------------------------------------------------------
// Multiple OnResponse handlers both fire
// ---------------------------------------------------------------------------

func TestHooks_MultipleOnResponse_BothFire(t *testing.T) {
	var (
		mu     sync.Mutex
		first  bool
		second bool
	)

	adapter := &mockCompleter{resp: llm.Response{Content: "ok"}}

	client := llm.NewClient(adapter,
		llm.OnResponse(func(_ llm.Response, _ time.Duration) {
			mu.Lock()
			defer mu.Unlock()
			first = true
		}),
		llm.OnResponse(func(_ llm.Response, _ time.Duration) {
			mu.Lock()
			defer mu.Unlock()
			second = true
		}),
	)

	_, err := client.Complete(context.Background(), llm.Request{})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	assert.True(t, first, "first OnResponse handler should fire")
	assert.True(t, second, "second OnResponse handler should fire")
}

// ---------------------------------------------------------------------------
// WithBus + OnError both receive error events
// ---------------------------------------------------------------------------

func TestHooks_WithBus_PlusOnError_BothReceive(t *testing.T) {
	var (
		mu          sync.Mutex
		busReceived bool
		onErrFired  bool
	)

	b := bus.New()
	b.Subscribe(string(llm.TopicRequestError),
		func(_ context.Context, e bus.Event) error {
			mu.Lock()
			defer mu.Unlock()
			busReceived = true
			return nil
		})

	adapter := &mockCompleter{
		err: llmerrors.NewAuth("openai", fmt.Errorf("bad key")),
	}

	client := llm.NewClient(adapter,
		llm.WithBus(b),
		llm.OnError(func(_ error) {
			mu.Lock()
			defer mu.Unlock()
			onErrFired = true
		}),
	)

	_, _ = client.Complete(context.Background(), llm.Request{})

	mu.Lock()
	defer mu.Unlock()
	assert.True(t, busReceived, "direct bus subscriber should receive error event")
	assert.True(t, onErrFired, "OnError handler should fire via bus")
}
