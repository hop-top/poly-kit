package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dialHub(t *testing.T, url string) (*websocket.Conn, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	c, _, err := websocket.Dial(ctx, url, nil)
	require.NoError(t, err)
	t.Cleanup(func() { c.Close(websocket.StatusNormalClosure, "") })
	return c, ctx
}

func readMsg(t *testing.T, ctx context.Context, c *websocket.Conn) WSMessage {
	t.Helper()
	_, data, err := c.Read(ctx)
	require.NoError(t, err)
	var msg WSMessage
	require.NoError(t, json.Unmarshal(data, &msg))
	return msg
}

func writeMsg(t *testing.T, ctx context.Context, c *websocket.Conn, msg WSMessage) {
	t.Helper()
	data, err := json.Marshal(msg)
	require.NoError(t, err)
	require.NoError(t, c.Write(ctx, websocket.MessageText, data))
}

func startHub(t *testing.T) (*Hub, context.CancelFunc) {
	t.Helper()
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)
	<-hub.ready
	return hub, cancel
}

func TestHub_WelcomeOnConnect(t *testing.T) {
	hub, cancel := startHub(t)
	defer cancel()

	srv := httptest.NewServer(WSHandler(hub))
	defer srv.Close()

	wsURL := "ws" + srv.URL[len("http"):]
	c, ctx := dialHub(t, wsURL)

	msg := readMsg(t, ctx, c)
	assert.Equal(t, "welcome", msg.Type)
}

func TestHub_SubscribeAndReceive(t *testing.T) {
	hub, cancel := startHub(t)
	defer cancel()

	srv := httptest.NewServer(WSHandler(hub))
	defer srv.Close()

	wsURL := "ws" + srv.URL[len("http"):]
	c, ctx := dialHub(t, wsURL)

	// drain welcome
	readMsg(t, ctx, c)

	// subscribe
	writeMsg(t, ctx, c, WSMessage{Type: "subscribe", Topic: "order.created"})
	ack := readMsg(t, ctx, c)
	assert.Equal(t, "ack", ack.Type)
	assert.Equal(t, "order.created", ack.Topic)

	// publish
	require.NoError(t, hub.Publish("order.created", map[string]string{"id": "42"}))

	msg := readMsg(t, ctx, c)
	assert.Equal(t, "message", msg.Type)
	assert.Equal(t, "order.created", msg.Topic)

	var payload map[string]string
	require.NoError(t, json.Unmarshal(msg.Payload, &payload))
	assert.Equal(t, "42", payload["id"])
}

func TestHub_UnsubscribeStopsMessages(t *testing.T) {
	hub, cancel := startHub(t)
	defer cancel()

	srv := httptest.NewServer(WSHandler(hub))
	defer srv.Close()

	wsURL := "ws" + srv.URL[len("http"):]
	c, ctx := dialHub(t, wsURL)
	readMsg(t, ctx, c) // welcome

	writeMsg(t, ctx, c, WSMessage{Type: "subscribe", Topic: "order.created"})
	readMsg(t, ctx, c) // ack

	writeMsg(t, ctx, c, WSMessage{Type: "unsubscribe", Topic: "order.created"})
	readMsg(t, ctx, c) // ack

	require.NoError(t, hub.Publish("order.created", "ignored"))

	// should not receive; wait briefly
	shortCtx, shortCancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer shortCancel()
	_, _, err := c.Read(shortCtx)
	assert.Error(t, err, "should not receive after unsubscribe")
}

func TestHub_GlobTopicMatching(t *testing.T) {
	hub, cancel := startHub(t)
	defer cancel()

	srv := httptest.NewServer(WSHandler(hub))
	defer srv.Close()

	wsURL := "ws" + srv.URL[len("http"):]

	t.Run("single wildcard", func(t *testing.T) {
		c, ctx := dialHub(t, wsURL)
		readMsg(t, ctx, c)

		writeMsg(t, ctx, c, WSMessage{Type: "subscribe", Topic: "order.*"})
		readMsg(t, ctx, c) // ack

		require.NoError(t, hub.Publish("order.created", "a"))
		msg := readMsg(t, ctx, c)
		assert.Equal(t, "order.created", msg.Topic)

		require.NoError(t, hub.Publish("order.deleted", "b"))
		msg = readMsg(t, ctx, c)
		assert.Equal(t, "order.deleted", msg.Topic)

		// should NOT match nested
		require.NoError(t, hub.Publish("order.item.added", "c"))
		shortCtx, shortCancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer shortCancel()
		_, _, err := c.Read(shortCtx)
		assert.Error(t, err)
	})

	t.Run("double wildcard", func(t *testing.T) {
		c, ctx := dialHub(t, wsURL)
		readMsg(t, ctx, c)

		writeMsg(t, ctx, c, WSMessage{Type: "subscribe", Topic: "domain.**"})
		readMsg(t, ctx, c)

		require.NoError(t, hub.Publish("domain.entity.created", "x"))
		msg := readMsg(t, ctx, c)
		assert.Equal(t, "domain.entity.created", msg.Topic)

		require.NoError(t, hub.Publish("domain.service.started", "y"))
		msg = readMsg(t, ctx, c)
		assert.Equal(t, "domain.service.started", msg.Topic)
	})
}

func TestHub_MultipleClients_OnlySubscribersReceive(t *testing.T) {
	hub, cancel := startHub(t)
	defer cancel()

	srv := httptest.NewServer(WSHandler(hub))
	defer srv.Close()

	wsURL := "ws" + srv.URL[len("http"):]

	// client A subscribes to "chat"
	cA, ctxA := dialHub(t, wsURL)
	readMsg(t, ctxA, cA)
	writeMsg(t, ctxA, cA, WSMessage{Type: "subscribe", Topic: "chat"})
	readMsg(t, ctxA, cA) // ack

	// client B subscribes to "alerts"
	cB, ctxB := dialHub(t, wsURL)
	readMsg(t, ctxB, cB)
	writeMsg(t, ctxB, cB, WSMessage{Type: "subscribe", Topic: "alerts"})
	readMsg(t, ctxB, cB) // ack

	// publish to "chat" — only A should receive
	require.NoError(t, hub.Publish("chat", "hello"))

	msgA := readMsg(t, ctxA, cA)
	assert.Equal(t, "chat", msgA.Topic)

	shortCtx, shortCancel := context.WithTimeout(ctxB, 200*time.Millisecond)
	defer shortCancel()
	_, _, err := cB.Read(shortCtx)
	assert.Error(t, err, "client B should not receive chat message")
}

func TestHub_ShutdownClosesConnections(t *testing.T) {
	hub, cancel := startHub(t)

	srv := httptest.NewServer(WSHandler(hub))
	defer srv.Close()

	wsURL := "ws" + srv.URL[len("http"):]
	c, ctx := dialHub(t, wsURL)
	readMsg(t, ctx, c) // welcome

	// cancel hub context
	cancel()

	// wait for hub done
	select {
	case <-hub.done:
	case <-time.After(2 * time.Second):
		t.Fatal("hub did not shut down in time")
	}

	// read should fail since connection was closed by hub
	_, _, err := c.Read(ctx)
	assert.Error(t, err)
}

func TestHub_SubscriptionLimitEnforced(t *testing.T) {
	hub, cancel := startHub(t)
	defer cancel()

	srv := httptest.NewServer(WSHandler(hub))
	defer srv.Close()

	wsURL := "ws" + srv.URL[len("http"):]
	c, ctx := dialHub(t, wsURL)
	readMsg(t, ctx, c) // welcome

	// Subscribe to 1000 topics (the max).
	for i := range 1000 {
		writeMsg(t, ctx, c, WSMessage{Type: "subscribe", Topic: "t." + string(rune('a'+i%26)) + "." + fmt.Sprintf("%d", i)})
		ack := readMsg(t, ctx, c)
		require.Equal(t, "ack", ack.Type)
	}

	// 1001st subscription should be rejected with error.
	writeMsg(t, ctx, c, WSMessage{Type: "subscribe", Topic: "overflow.topic"})
	errMsg := readMsg(t, ctx, c)
	assert.Equal(t, "error", errMsg.Type)
	assert.Equal(t, "overflow.topic", errMsg.Topic)
}

func TestMatchTopic(t *testing.T) {
	tests := []struct {
		pattern string
		topic   string
		want    bool
	}{
		{"order.created", "order.created", true},
		{"order.created", "order.deleted", false},
		{"order.*", "order.created", true},
		{"order.*", "order.deleted", true},
		{"order.*", "order.item.added", false},
		{"domain.**", "domain.entity.created", true},
		{"domain.**", "domain.service.started", true},
		{"domain.**", "domain.a.b.c", true},
		{"*", "anything", true},
		{"*", "a.b", false},
		{"a.*.c", "a.b.c", true},
		{"a.*.c", "a.b.d", false},
	}
	for _, tt := range tests {
		t.Run(tt.pattern+"_vs_"+tt.topic, func(t *testing.T) {
			assert.Equal(t, tt.want, matchTopic(tt.pattern, tt.topic))
		})
	}
}

func subscribeExpect(
	t *testing.T, ctx context.Context, c *websocket.Conn, topic, want string,
) {
	t.Helper()
	writeMsg(t, ctx, c, WSMessage{Type: "subscribe", Topic: topic})
	got := readMsg(t, ctx, c)
	assert.Equal(t, want, got.Type, "subscribe %.20q", topic)
	assert.Equal(t, topic, got.Topic)
}

func TestHub_OversizedOrDeepPatternRefusedAndNotStored(t *testing.T) {
	hub, cancel := startHub(t)
	defer cancel()
	srv := httptest.NewServer(WSHandler(hub))
	defer srv.Close()
	c, ctx := dialHub(t, "ws"+srv.URL[len("http"):])
	readMsg(t, ctx, c) // welcome

	long := strings.Repeat("a", maxPatternLen+1)
	deep := strings.TrimSuffix(strings.Repeat("a.", maxPatternSegments+1), ".")
	stars := strings.TrimSuffix(strings.Repeat("**.", maxPatternDoubleStars+1), ".")
	for _, p := range []string{long, deep, stars} {
		subscribeExpect(t, ctx, c, p, "error")
	}

	// Nothing stored: a publish those would match is silent.
	require.NoError(t, hub.Publish(long, 1))
	require.NoError(t, hub.Publish(deep, 1))
	require.NoError(t, hub.Publish("x.y", 1))
	// A held probe proves ordering: the ack is the next frame.
	subscribeExpect(t, ctx, c, "probe", "ack")

	// The bounds themselves are allowed.
	subscribeExpect(t, ctx, c, strings.Repeat("a", maxPatternLen), "ack")
	subscribeExpect(t, ctx, c,
		strings.TrimSuffix(strings.Repeat("a.", maxPatternSegments), "."), "ack")
	subscribeExpect(t, ctx, c,
		strings.TrimSuffix(strings.Repeat("**.", maxPatternDoubleStars), "."), "ack")
}

func TestHub_SubscriptionBytesBudgetedAndReleased(t *testing.T) {
	hub := NewHub(WithMaxMessageSize(1 << 20))
	hctx, hcancel := context.WithCancel(context.Background())
	defer hcancel()
	go hub.Run(hctx)
	<-hub.ready
	srv := httptest.NewServer(WSHandler(hub))
	defer srv.Close()
	c, ctx := dialHub(t, "ws"+srv.URL[len("http"):])
	readMsg(t, ctx, c) // welcome

	fits := maxSubscriptionBytes / maxPatternLen
	require.Less(t, fits, 1000, "byte budget bites before the count")
	pat := func(i int) string { return fmt.Sprintf("%0*d", maxPatternLen, i) }
	// One at a time: acks past the send buffer are dropped, not queued.
	for i := range fits {
		subscribeExpect(t, ctx, c, pat(i), "ack")
	}
	subscribeExpect(t, ctx, c, pat(fits), "error")
	// A held pattern costs nothing.
	subscribeExpect(t, ctx, c, pat(0), "ack")
	// Unsubscribe frees budget.
	writeMsg(t, ctx, c, WSMessage{Type: "unsubscribe", Topic: pat(0)})
	require.Equal(t, "ack", readMsg(t, ctx, c).Type)
	subscribeExpect(t, ctx, c, pat(fits), "ack")
}

func TestHub_ResubscribeAtCountCapAcks(t *testing.T) {
	var s subscriptions
	for i := range maxSubscriptions {
		require.True(t, s.add(fmt.Sprintf("t.%d", i)))
	}
	assert.False(t, s.add("overflow"))
	assert.True(t, s.add("t.0"), "held pattern costs nothing")
}
