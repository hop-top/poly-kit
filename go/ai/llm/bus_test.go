package llm_test

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/runtime/bus"
)

// ---------------------------------------------------------------------------
// Mock provider
// ---------------------------------------------------------------------------

type busMockProvider struct {
	completeResp llm.Response
	completeErr  error
	streamTokens []llm.Token
	streamErr    error
}

func (m *busMockProvider) Close() error { return nil }

func (m *busMockProvider) Complete(_ context.Context, _ llm.Request) (llm.Response, error) {
	return m.completeResp, m.completeErr
}

func (m *busMockProvider) Stream(_ context.Context, _ llm.Request) (llm.TokenIterator, error) {
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	return &busMockIterator{tokens: m.streamTokens}, nil
}

type busMockIterator struct {
	tokens []llm.Token
	idx    int
}

func (it *busMockIterator) Next() (llm.Token, error) {
	if it.idx >= len(it.tokens) {
		return llm.Token{}, io.EOF
	}
	tok := it.tokens[it.idx]
	it.idx++
	return tok, nil
}

func (it *busMockIterator) Close() error { return nil }

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestComplete_BusEvents(t *testing.T) {
	b := bus.New()
	defer func() { _ = b.Close(context.Background()) }()

	provider := &busMockProvider{
		completeResp: llm.Response{
			Content: "hello",
			Usage:   llm.Usage{TotalTokens: 42},
		},
	}

	var mu sync.Mutex
	var topics []string

	b.Subscribe("kit.ai.#", func(_ context.Context, e bus.Event) error {
		mu.Lock()
		topics = append(topics, string(e.Topic))
		mu.Unlock()
		return nil
	})

	client := llm.NewClient(provider, llm.WithBus(b))
	resp, err := client.Complete(context.Background(), llm.Request{
		Model:    "test-model",
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})

	require.NoError(t, err)
	assert.Equal(t, "hello", resp.Content)

	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, topics, "kit.ai.request.started")
	assert.Contains(t, topics, "kit.ai.response.received")
}

func TestComplete_BusErrorEvent(t *testing.T) {
	b := bus.New()
	defer func() { _ = b.Close(context.Background()) }()

	provider := &busMockProvider{
		completeErr: errors.New("boom"),
	}

	var gotErr bool
	b.Subscribe("kit.ai.request.errored", func(_ context.Context, e bus.Event) error {
		if p, ok := e.Payload.(llm.RequestErrorPayload); ok {
			gotErr = p.Err != nil
		}
		return nil
	})

	client := llm.NewClient(provider, llm.WithBus(b))
	_, err := client.Complete(context.Background(), llm.Request{})

	require.Error(t, err)
	assert.True(t, gotErr, "expected error event on bus")
}

func TestStream_BusEvents(t *testing.T) {
	b := bus.New()
	defer func() { _ = b.Close(context.Background()) }()

	provider := &busMockProvider{
		streamTokens: []llm.Token{
			{Content: "hel"},
			{Content: "lo", Done: true},
		},
	}

	var mu sync.Mutex
	var topics []string

	b.Subscribe("kit.ai.#", func(_ context.Context, e bus.Event) error {
		mu.Lock()
		topics = append(topics, string(e.Topic))
		mu.Unlock()
		return nil
	})

	client := llm.NewClient(provider, llm.WithBus(b))
	iter, err := client.Stream(context.Background(), llm.Request{
		Model: "test-model",
	})
	require.NoError(t, err)

	// Drain iterator
	for {
		tok, err := iter.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if tok.Done {
			break
		}
	}
	_ = iter.Close()

	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, topics, "kit.ai.request.started")
}

func TestOnRequest_WithBus(t *testing.T) {
	b := bus.New()
	defer func() { _ = b.Close(context.Background()) }()

	provider := &busMockProvider{
		completeResp: llm.Response{Content: "ok"},
	}

	var callbackReq llm.Request
	client := llm.NewClient(provider,
		llm.WithBus(b),
		llm.OnRequest(func(req llm.Request) {
			callbackReq = req
		}),
	)

	_, err := client.Complete(context.Background(), llm.Request{
		Model: "callback-test",
	})
	require.NoError(t, err)
	assert.Equal(t, "callback-test", callbackReq.Model)
}

func TestOnResponse_WithBus(t *testing.T) {
	b := bus.New()
	defer func() { _ = b.Close(context.Background()) }()

	provider := &busMockProvider{
		completeResp: llm.Response{Content: "ok"},
	}

	var gotResp llm.Response
	var gotDur time.Duration
	client := llm.NewClient(provider,
		llm.WithBus(b),
		llm.OnResponse(func(resp llm.Response, dur time.Duration) {
			gotResp = resp
			gotDur = dur
		}),
	)

	_, err := client.Complete(context.Background(), llm.Request{})
	require.NoError(t, err)
	assert.Equal(t, "ok", gotResp.Content)
	assert.GreaterOrEqual(t, gotDur, time.Duration(0))
}

func TestNilBus_NoPanic(t *testing.T) {
	provider := &busMockProvider{
		completeResp: llm.Response{Content: "ok"},
	}

	// No WithBus option — bus is nil
	client := llm.NewClient(provider)
	resp, err := client.Complete(context.Background(), llm.Request{})

	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Content)
}

func TestOnRequest_WithoutExplicitBus(t *testing.T) {
	provider := &busMockProvider{
		completeResp: llm.Response{Content: "ok"},
	}

	var called bool
	client := llm.NewClient(provider,
		llm.OnRequest(func(_ llm.Request) { called = true }),
	)

	_, err := client.Complete(context.Background(), llm.Request{})
	require.NoError(t, err)
	assert.True(t, called, "OnRequest should auto-create bus and fire")
}
