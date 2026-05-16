package llm_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/runtime/bus"
)

func TestDefaultTopics_ByteForByte(t *testing.T) {
	// Lock in the canonical strings: anyone changing these breaks
	// every existing subscriber that hard-codes the literal.
	assert.Equal(t, bus.Topic("kit.ai.request.started"),
		llm.DefaultTopics.RequestStart)
	assert.Equal(t, bus.Topic("kit.ai.response.received"),
		llm.DefaultTopics.RequestEnd)
	assert.Equal(t, bus.Topic("kit.ai.request.errored"),
		llm.DefaultTopics.RequestError)
	assert.Equal(t, bus.Topic("kit.ai.fallback.applied"),
		llm.DefaultTopics.Fallback)
	assert.Equal(t, bus.Topic("kit.ai.route.selected"),
		llm.DefaultTopics.Route)
	assert.Equal(t, bus.Topic("kit.ai.eva.evaluated"),
		llm.DefaultTopics.EvaResult)
}

func TestPackageConsts_ReadFromDefaultTopics(t *testing.T) {
	// Backward-compat: package-level vars must equal the struct fields.
	assert.Equal(t, llm.DefaultTopics.RequestStart, llm.TopicRequestStart)
	assert.Equal(t, llm.DefaultTopics.RequestEnd, llm.TopicRequestEnd)
	assert.Equal(t, llm.DefaultTopics.RequestError, llm.TopicRequestError)
	assert.Equal(t, llm.DefaultTopics.Fallback, llm.TopicFallback)
	assert.Equal(t, llm.DefaultTopics.Route, llm.TopicRoute)
	assert.Equal(t, llm.DefaultTopics.EvaResult, llm.TopicEvaResult)
}

func TestWithTopicPrefix_PreservesObjectAction(t *testing.T) {
	provider := &busMockProvider{
		completeResp: llm.Response{Content: "ok"},
	}
	c := llm.NewClient(provider, llm.WithTopicPrefix("myapp.ai"))
	got := c.Topics()

	assert.Equal(t, bus.Topic("myapp.ai.request.started"), got.RequestStart)
	// Note the non-uniform shape: response.received NOT request.ended.
	assert.Equal(t, bus.Topic("myapp.ai.response.received"), got.RequestEnd)
	assert.Equal(t, bus.Topic("myapp.ai.request.errored"), got.RequestError)
	assert.Equal(t, bus.Topic("myapp.ai.fallback.applied"), got.Fallback)
	assert.Equal(t, bus.Topic("myapp.ai.route.selected"), got.Route)
	// Non-uniform: eva.evaluated NOT request.evaluated.
	assert.Equal(t, bus.Topic("myapp.ai.eva.evaluated"), got.EvaResult)
}

func TestWithTopicPrefix_PublishesUnderNewPrefix(t *testing.T) {
	b := bus.New()
	defer func() { _ = b.Close(context.Background()) }()

	provider := &busMockProvider{
		completeResp: llm.Response{Content: "ok"},
	}

	var mu sync.Mutex
	var topics []string
	b.Subscribe("myapp.ai.#", func(_ context.Context, e bus.Event) error {
		mu.Lock()
		topics = append(topics, string(e.Topic))
		mu.Unlock()
		return nil
	})

	client := llm.NewClient(provider,
		llm.WithBus(b),
		llm.WithTopicPrefix("myapp.ai"),
	)
	_, err := client.Complete(context.Background(), llm.Request{})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, topics, "myapp.ai.request.started")
	assert.Contains(t, topics, "myapp.ai.response.received")
}

func TestWithTopics_PerFieldOverride(t *testing.T) {
	provider := &busMockProvider{
		completeResp: llm.Response{Content: "ok"},
	}
	c := llm.NewClient(provider, llm.WithTopics(llm.Topics{
		RequestStart: "x.y.z.started",
	}))
	got := c.Topics()

	assert.Equal(t, bus.Topic("x.y.z.started"), got.RequestStart)
	// Other 5 stay at defaults.
	assert.Equal(t, llm.DefaultTopics.RequestEnd, got.RequestEnd)
	assert.Equal(t, llm.DefaultTopics.RequestError, got.RequestError)
	assert.Equal(t, llm.DefaultTopics.Fallback, got.Fallback)
	assert.Equal(t, llm.DefaultTopics.Route, got.Route)
	assert.Equal(t, llm.DefaultTopics.EvaResult, got.EvaResult)
}

func TestClient_Topics_ReturnsDefaults(t *testing.T) {
	provider := &busMockProvider{
		completeResp: llm.Response{Content: "ok"},
	}
	c := llm.NewClient(provider)
	assert.Equal(t, llm.DefaultTopics, c.Topics())
}

func TestWithTopicPrefix_InvalidPanics(t *testing.T) {
	cases := []string{
		"",               // empty
		"only-one-seg",   // 1 segment
		"too.many.parts", // 3 segments — too long for the source.category split
		"trailing.dot.",  // trailing dot
		"UPPER.case",     // invalid chars
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			assert.Panics(t, func() { llm.WithTopicPrefix(p) })
		})
	}
}

func TestWithTopics_InvalidPanics(t *testing.T) {
	provider := &busMockProvider{}
	// Non-past-tense action segment violates ValidateTopic.
	assert.Panics(t, func() {
		llm.NewClient(provider, llm.WithTopics(llm.Topics{
			RequestStart: "x.y.z.startish",
		}))
	})
	// Wrong segment count.
	assert.Panics(t, func() {
		llm.NewClient(provider, llm.WithTopics(llm.Topics{
			RequestEnd: "x.y.received",
		}))
	})
}

func TestOnRequest_AfterPrefix_SubscribesToOverriddenTopic(t *testing.T) {
	provider := &busMockProvider{
		completeResp: llm.Response{Content: "ok"},
	}

	var called bool
	client := llm.NewClient(provider,
		llm.WithTopicPrefix("myapp.ai"),
		llm.OnRequest(func(_ llm.Request) { called = true }),
	)
	_, err := client.Complete(context.Background(), llm.Request{})
	require.NoError(t, err)
	assert.True(t, called, "OnRequest applied AFTER WithTopicPrefix should fire")
}
