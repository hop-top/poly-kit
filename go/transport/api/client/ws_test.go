package client_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/api/client"
)

func setupWS(t *testing.T) (*httptest.Server, *api.Hub) {
	t.Helper()
	hub := api.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)
	t.Cleanup(cancel)

	srv := httptest.NewServer(api.WSHandler(hub))
	t.Cleanup(srv.Close)
	return srv, hub
}

func wsURL(srv *httptest.Server) string {
	return "ws" + srv.URL[len("http"):]
}

// waitFor blocks until predicate returns true or timeout fires.
func waitFor(t *testing.T, timeout time.Duration, pred func() bool) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		if pred() {
			return
		}
		select {
		case <-deadline:
			t.Fatal("timeout waiting for condition")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestWS_SubscribeAndReceive(t *testing.T) {
	srv, hub := setupWS(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ws, err := client.DialWS(ctx, wsURL(srv))
	require.NoError(t, err)
	defer ws.Close()

	msgCh := make(chan api.WSMessage, 16)
	ws.OnMessage(func(msg api.WSMessage) {
		msgCh <- msg
	})

	go ws.Listen(ctx)

	// wait for welcome
	select {
	case <-msgCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for welcome")
	}

	require.NoError(t, ws.Subscribe(ctx, "events"))
	// wait for ack
	select {
	case <-msgCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for subscribe ack")
	}

	require.NoError(t, hub.Publish("events", map[string]string{"k": "v"}))

	select {
	case msg := <-msgCh:
		assert.Equal(t, "message", msg.Type)
		assert.Equal(t, "events", msg.Topic)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for published message")
	}
}

func TestWS_Unsubscribe(t *testing.T) {
	srv, hub := setupWS(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ws, err := client.DialWS(ctx, wsURL(srv))
	require.NoError(t, err)
	defer ws.Close()

	msgCh := make(chan api.WSMessage, 16)
	ws.OnMessage(func(msg api.WSMessage) {
		msgCh <- msg
	})

	go ws.Listen(ctx)

	// drain welcome
	select {
	case <-msgCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for welcome")
	}

	require.NoError(t, ws.Subscribe(ctx, "ch"))
	// drain ack
	select {
	case <-msgCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for subscribe ack")
	}

	require.NoError(t, hub.Publish("ch", "first"))
	select {
	case msg := <-msgCh:
		assert.Equal(t, "message", msg.Type)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for first message")
	}

	require.NoError(t, ws.Unsubscribe(ctx, "ch"))
	// drain unsubscribe ack
	select {
	case <-msgCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for unsubscribe ack")
	}

	require.NoError(t, hub.Publish("ch", "second"))

	// second message should NOT arrive
	select {
	case msg := <-msgCh:
		t.Fatalf("received unexpected message after unsubscribe: %+v", msg)
	case <-time.After(200 * time.Millisecond):
		// expected: no message
	}
}

func TestWS_Close(t *testing.T) {
	srv, _ := setupWS(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ws, err := client.DialWS(ctx, wsURL(srv))
	require.NoError(t, err)

	require.NoError(t, ws.Close())

	// Listen should return an error after close
	err = ws.Listen(ctx)
	assert.Error(t, err)
}
