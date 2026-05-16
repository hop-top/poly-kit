package client

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/coder/websocket"

	"hop.top/kit/go/transport/api"
)

// WSOption configures a WSClient.
type WSOption func(*wsOptions)

type wsOptions struct{}

// WSClient wraps a WebSocket connection for pub/sub messaging.
type WSClient struct {
	conn *websocket.Conn

	mu       sync.Mutex
	callback func(msg api.WSMessage)
}

// DialWS connects to url and returns a WSClient.
func DialWS(ctx context.Context, url string, _ ...WSOption) (*WSClient, error) {
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return nil, err
	}
	return &WSClient{conn: conn}, nil
}

// Subscribe sends a subscribe message for the given topic.
func (c *WSClient) Subscribe(ctx context.Context, topic string) error {
	return c.sendMsg(ctx, api.WSMessage{Type: "subscribe", Topic: topic})
}

// Unsubscribe sends an unsubscribe message for the given topic.
func (c *WSClient) Unsubscribe(ctx context.Context, topic string) error {
	return c.sendMsg(ctx, api.WSMessage{Type: "unsubscribe", Topic: topic})
}

// OnMessage registers a callback invoked for each received message.
func (c *WSClient) OnMessage(fn func(msg api.WSMessage)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.callback = fn
}

// Listen reads messages until ctx is canceled or the connection
// closes. It dispatches non-control messages to the registered
// callback.
func (c *WSClient) Listen(ctx context.Context) error {
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			return err
		}
		var msg api.WSMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		c.mu.Lock()
		cb := c.callback
		c.mu.Unlock()

		if cb != nil {
			cb(msg)
		}
	}
}

// Close gracefully closes the WebSocket connection.
func (c *WSClient) Close() error {
	return c.conn.Close(websocket.StatusNormalClosure, "client closed")
}

func (c *WSClient) sendMsg(ctx context.Context, msg api.WSMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return c.conn.Write(ctx, websocket.MessageText, data)
}
