package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"
)

// ErrHubClosed is returned by Hub.Publish when the hub has shut down.
var ErrHubClosed = errors.New("hub closed")

// HubOption configures a Hub.
type HubOption func(*Hub)

// WithMaxMessageSize sets the read limit per WebSocket connection.
// Default: 65536 (64KB).
func WithMaxMessageSize(bytes int64) HubOption {
	return func(h *Hub) { h.maxMessageSize = bytes }
}

// WithAcceptOrigins configures allowed origins for WebSocket
// upgrades. Patterns are passed to websocket.AcceptOptions.
// OriginPatterns. Default is empty (same-origin only).
func WithAcceptOrigins(origins ...string) HubOption {
	return func(h *Hub) { h.originPatterns = origins }
}

// WSMessage is the JSON envelope for all WebSocket communication.
type WSMessage struct {
	Type    string          `json:"type"`
	Topic   string          `json:"topic,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type client struct {
	conn   *websocket.Conn
	addr   string
	topics map[string]bool
	send   chan []byte
	cancel context.CancelFunc
}

type hubRegister struct {
	c *client
}

type hubUnregister struct {
	c *client
}

type hubBroadcast struct {
	topic   string
	payload json.RawMessage
}

// Hub manages WebSocket connections and topic subscriptions.
type Hub struct {
	registerCh   chan hubRegister
	unregisterCh chan hubUnregister
	broadcastCh  chan hubBroadcast

	// clients tracked for shutdown; only accessed by Run goroutine.
	clients map[*client]struct{}

	ready chan struct{} // closed once Run loop starts
	done  chan struct{} // closed once Run loop exits

	// OnDrop is called when a message is dropped for a slow client.
	OnDrop func(clientAddr string, topic string)

	// Dropped counts total messages dropped across all clients.
	Dropped atomic.Int64

	maxMessageSize int64    // read limit per connection
	originPatterns []string // allowed origins for upgrades

	closeOnce sync.Once
}

// NewHub creates a Hub. Call Hub.Run in a goroutine before use.
func NewHub(opts ...HubOption) *Hub {
	h := &Hub{
		registerCh:     make(chan hubRegister, 64),
		unregisterCh:   make(chan hubUnregister, 64),
		broadcastCh:    make(chan hubBroadcast, 256),
		clients:        make(map[*client]struct{}),
		ready:          make(chan struct{}),
		done:           make(chan struct{}),
		maxMessageSize: 65536,
	}
	for _, o := range opts {
		o(h)
	}
	return h
}

// Run is the Hub's main loop. It processes register, unregister, and
// broadcast operations sequentially, avoiding mutexes. Exits when ctx
// is canceled; all connections are closed on exit.
func (h *Hub) Run(ctx context.Context) {
	h.closeOnce.Do(func() { close(h.ready) })

	defer func() {
		for c := range h.clients {
			c.cancel()
			_ = c.conn.Close(websocket.StatusGoingAway, "hub shutdown")
		}
		close(h.done)
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case reg := <-h.registerCh:
			h.clients[reg.c] = struct{}{}
		case unreg := <-h.unregisterCh:
			if _, ok := h.clients[unreg.c]; ok {
				unreg.c.cancel()
				delete(h.clients, unreg.c)
			}
		case msg := <-h.broadcastCh:
			data := mustMarshal(WSMessage{
				Type:    "message",
				Topic:   msg.topic,
				Payload: msg.payload,
			})
			for c := range h.clients {
				if !matchesAny(msg.topic, c.topics) {
					continue
				}
				select {
				case c.send <- data:
				default:
					h.Dropped.Add(1)
					if h.OnDrop != nil {
						h.OnDrop(c.addr, msg.topic)
					}
				}
			}
		}
	}
}

// Publish sends a message to all clients subscribed to topic.
func (h *Hub) Publish(topic string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	select {
	case h.broadcastCh <- hubBroadcast{topic: topic, payload: raw}:
	case <-h.done:
		return ErrHubClosed
	}
	return nil
}

// WSHandler returns an http.HandlerFunc that upgrades connections to
// WebSocket and registers them with the hub.
func WSHandler(hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			OriginPatterns: hub.originPatterns,
		})
		if err != nil {
			return
		}

		conn.SetReadLimit(hub.maxMessageSize)

		ctx, cancel := context.WithCancel(r.Context())
		c := &client{
			conn:   conn,
			addr:   r.RemoteAddr,
			topics: make(map[string]bool),
			send:   make(chan []byte, 64),
			cancel: cancel,
		}

		hub.registerCh <- hubRegister{c: c}

		// writer goroutine
		go func() {
			defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
			for {
				select {
				case <-ctx.Done():
					return
				case data := <-c.send:
					if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
						return
					}
				}
			}
		}()

		// send welcome
		welcome := mustMarshal(WSMessage{Type: "welcome"})
		select {
		case c.send <- welcome:
		default:
		}

		// reader loop (blocks until close/error)
		readLoop(ctx, hub, c, conn)

		hub.unregisterCh <- hubUnregister{c: c}
		cancel()
	}
}

func readLoop(
	ctx context.Context,
	hub *Hub,
	c *client,
	conn *websocket.Conn,
) {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var msg WSMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "subscribe":
			const maxSubscriptions = 1000
			if len(c.topics) >= maxSubscriptions {
				errMsg := mustMarshal(WSMessage{Type: "error", Topic: msg.Topic})
				select {
				case c.send <- errMsg:
				default:
				}
				continue
			}
			c.topics[msg.Topic] = true
			ack := mustMarshal(WSMessage{Type: "ack", Topic: msg.Topic})
			select {
			case c.send <- ack:
			default:
			}
		case "unsubscribe":
			delete(c.topics, msg.Topic)
			ack := mustMarshal(WSMessage{Type: "ack", Topic: msg.Topic})
			select {
			case c.send <- ack:
			default:
			}
		}
	}
}

// matchesAny checks if topic matches any of the subscription patterns.
func matchesAny(topic string, patterns map[string]bool) bool {
	for p := range patterns {
		if matchTopic(p, topic) {
			return true
		}
	}
	return false
}

// matchTopic matches a topic against a glob pattern.
// `*` matches exactly one segment; `**` matches one or more segments.
func matchTopic(pattern, topic string) bool {
	if pattern == topic {
		return true
	}
	pParts := strings.Split(pattern, ".")
	tParts := strings.Split(topic, ".")
	return matchParts(pParts, tParts)
}

func matchParts(pattern, topic []string) bool {
	pi, ti := 0, 0
	for pi < len(pattern) && ti < len(topic) {
		if pattern[pi] == "**" {
			// ** must be last segment in pattern
			if pi == len(pattern)-1 {
				return true
			}
			// try matching rest of pattern against every suffix of topic
			for k := ti; k < len(topic); k++ {
				if matchParts(pattern[pi+1:], topic[k:]) {
					return true
				}
			}
			return false
		}
		if pattern[pi] == "*" {
			pi++
			ti++
			continue
		}
		if pattern[pi] != topic[ti] {
			return false
		}
		pi++
		ti++
	}
	return pi == len(pattern) && ti == len(topic)
}

func mustMarshal(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic("ws: marshal: " + err.Error())
	}
	return data
}
