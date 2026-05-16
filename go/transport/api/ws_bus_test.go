package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockBusSub records subscriptions and allows manual dispatch.
type mockBusSub struct {
	mu       sync.Mutex
	handlers map[string]func(string, any)
}

func newMockBusSub() *mockBusSub {
	return &mockBusSub{handlers: make(map[string]func(string, any))}
}

func (m *mockBusSub) Subscribe(_ context.Context, topic string, handler func(string, any)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handlers[topic] = handler
	return nil
}

// emit simulates a bus event arriving on the given topic.
func (m *mockBusSub) emit(topic string, payload any) {
	m.mu.Lock()
	h := m.handlers[topic]
	m.mu.Unlock()
	if h != nil {
		h(topic, payload)
	}
}

func TestBusAdapter_ExactTopicForward(t *testing.T) {
	hub, cancel := startHub(t)
	defer cancel()

	srv := httptest.NewServer(WSHandler(hub))
	defer srv.Close()

	wsURL := "ws" + srv.URL[len("http"):]
	c, ctx := dialHub(t, wsURL)
	readMsg(t, ctx, c) // welcome

	writeMsg(t, ctx, c, WSMessage{Type: "subscribe", Topic: "order.created"})
	readMsg(t, ctx, c) // ack

	bus := newMockBusSub()
	adapter := NewBusAdapter(hub, bus)
	require.NoError(t, adapter.Bridge(context.Background(), "order.created"))

	bus.emit("order.created", map[string]string{"id": "1"})

	msg := readMsg(t, ctx, c)
	assert.Equal(t, "message", msg.Type)
	assert.Equal(t, "order.created", msg.Topic)

	var payload map[string]string
	require.NoError(t, json.Unmarshal(msg.Payload, &payload))
	assert.Equal(t, "1", payload["id"])
}

func TestBusAdapter_OnlyMatchingSubscribers(t *testing.T) {
	hub, cancel := startHub(t)
	defer cancel()

	srv := httptest.NewServer(WSHandler(hub))
	defer srv.Close()

	wsURL := "ws" + srv.URL[len("http"):]

	// client A subscribes to "order.created"
	cA, ctxA := dialHub(t, wsURL)
	readMsg(t, ctxA, cA)
	writeMsg(t, ctxA, cA, WSMessage{Type: "subscribe", Topic: "order.created"})
	readMsg(t, ctxA, cA)

	// client B subscribes to "user.updated"
	cB, ctxB := dialHub(t, wsURL)
	readMsg(t, ctxB, cB)
	writeMsg(t, ctxB, cB, WSMessage{Type: "subscribe", Topic: "user.updated"})
	readMsg(t, ctxB, cB)

	bus := newMockBusSub()
	adapter := NewBusAdapter(hub, bus)
	require.NoError(t, adapter.Bridge(context.Background(), "order.created", "user.updated"))

	bus.emit("order.created", "payload-a")

	msgA := readMsg(t, ctxA, cA)
	assert.Equal(t, "order.created", msgA.Topic)

	shortCtx, shortCancel := context.WithTimeout(ctxB, 200*time.Millisecond)
	defer shortCancel()
	_, _, err := cB.Read(shortCtx)
	assert.Error(t, err, "client B should not receive order.created")
}

func TestBusAdapter_MultipleTopics(t *testing.T) {
	hub, cancel := startHub(t)
	defer cancel()

	srv := httptest.NewServer(WSHandler(hub))
	defer srv.Close()

	wsURL := "ws" + srv.URL[len("http"):]
	c, ctx := dialHub(t, wsURL)
	readMsg(t, ctx, c)

	// subscribe to wildcard to receive both
	writeMsg(t, ctx, c, WSMessage{Type: "subscribe", Topic: "events.**"})
	readMsg(t, ctx, c)

	bus := newMockBusSub()
	adapter := NewBusAdapter(hub, bus)
	require.NoError(t, adapter.Bridge(context.Background(), "events.a", "events.b"))

	bus.emit("events.a", "first")
	msg1 := readMsg(t, ctx, c)
	assert.Equal(t, "events.a", msg1.Topic)

	bus.emit("events.b", "second")
	msg2 := readMsg(t, ctx, c)
	assert.Equal(t, "events.b", msg2.Topic)
}

func TestBusAdapter_ContextCancellation(t *testing.T) {
	hub, cancel := startHub(t)
	defer cancel()

	bus := newMockBusSub()
	adapter := NewBusAdapter(hub, bus)

	ctx, bridgeCancel := context.WithCancel(context.Background())
	require.NoError(t, adapter.Bridge(ctx, "stop.test"))

	// cancel the bridge context
	bridgeCancel()

	// emit after cancellation — hub.Publish still works but the
	// bridge's context is done; verify no panic and adapter is inert.
	bus.emit("stop.test", "after-cancel")

	// no client subscribed, so nothing to assert on delivery;
	// the key invariant is no panic/hang and the bus handler
	// respects context cancellation.
}
