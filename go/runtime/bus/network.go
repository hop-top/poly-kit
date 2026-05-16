package bus

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"hop.top/kit/go/core/util"
)

// networkMsg is the wire format for events over WebSocket.
// Compatible with JSON text frames.
type networkMsg struct {
	Topic     string    `json:"topic"`
	Source    string    `json:"source"`
	Timestamp time.Time `json:"timestamp"`
	Payload   any       `json:"payload"`
	Origin    string    `json:"origin"` // peer addr that first published
}

// wsConn wraps a single peer connection.
type wsConn struct {
	conn      *websocket.Conn
	addr      string
	outbound  bool
	cancel    context.CancelFunc
	done      chan struct{}
	writeFail atomic.Int32
	// peerOriginID is the remote's originID, learned from the first
	// inbound networkMsg. Used by relay mode to exclude the origin
	// peer when re-forwarding network events.
	peerOriginID atomic.Value // string
}

// NetworkOption configures NetworkAdapter.
type NetworkOption func(*NetworkAdapter)

// WithFilter sets the topic filter for outbound forwarding.
func WithFilter(f TopicFilter) NetworkOption {
	return func(n *NetworkAdapter) { n.filter = f }
}

// WithBackoff overrides reconnect backoff parameters.
func WithBackoff(base, cap time.Duration) NetworkOption {
	return func(n *NetworkAdapter) {
		n.reconnect.BaseDelay = base
		n.reconnect.MaxDelay = cap
	}
}

// WithOriginID sets the origin identifier for loop prevention.
// Defaults to a random string if not set.
func WithOriginID(id string) NetworkOption {
	return func(n *NetworkAdapter) { n.originID = id }
}

// WithAuth sets the authenticator for outbound/inbound connections.
func WithAuth(a Authenticator) NetworkOption {
	return func(n *NetworkAdapter) { n.auth = a }
}

// WithRelay enables star-topology relay mode. When enabled, inbound
// network events are re-forwarded to all connected peers EXCEPT the
// origin peer. Default off — preserves prior point-to-point semantics.
//
// Use on a hub adapter that bridges multiple peers (T-0182).
func WithRelay(enabled bool) NetworkOption {
	return func(n *NetworkAdapter) { n.relay = enabled }
}

// Authenticator handles auth handshake on network connections.
type Authenticator interface {
	// Token returns the auth token to send on outbound connect.
	Token() (string, error)
	// Verify validates an inbound auth token. Returns error if invalid.
	Verify(token string) error
}

type ctxKey struct{}

// networkOriginKey marks events injected from the network to prevent re-forwarding.
var networkOriginKey = ctxKey{}

// NetworkAdapter bridges a local bus to remote peers over WebSocket.
type NetworkAdapter struct {
	bus          Bus
	conns        map[string]*wsConn
	reconnecting map[string]bool
	mu           sync.RWMutex
	reconnect    util.RetryConfig
	filter       TopicFilter
	originID     string
	auth         Authenticator
	relay        bool // T-0182: star-topology relay (re-forward inbound to non-origin peers)
	closed       atomic.Bool
	wg           sync.WaitGroup
	unsub        Unsubscribe
}

// NewNetworkAdapter creates a network adapter bridging the given bus.
func NewNetworkAdapter(b Bus, opts ...NetworkOption) *NetworkAdapter {
	n := &NetworkAdapter{
		bus:          b,
		conns:        make(map[string]*wsConn),
		reconnecting: make(map[string]bool),
		reconnect: util.RetryConfig{
			BaseDelay: 100 * time.Millisecond,
			MaxDelay:  30 * time.Second,
			Jitter:    true,
		},
		originID: randomID(),
	}
	for _, o := range opts {
		o(n)
	}

	// Subscribe to all local events for outbound forwarding.
	n.unsub = b.SubscribeAsync("#", func(ctx context.Context, e Event) {
		// Skip events that arrived from the network unless relay is on.
		// In relay mode we re-forward to all peers EXCEPT the origin.
		if origin := ctx.Value(networkOriginKey); origin != nil {
			if !n.relay {
				return
			}
			originID, _ := origin.(string)
			n.forwardToRemotes(e, originID)
			return
		}
		n.forwardToRemotes(e, "")
	})

	return n
}

// Connect establishes a WebSocket connection to a remote peer.
func (n *NetworkAdapter) Connect(ctx context.Context, addr string) error {
	if n.closed.Load() {
		return ErrBusClosed
	}

	conn, _, err := websocket.Dial(ctx, addr, nil)
	if err != nil {
		return err
	}

	// Auth handshake: send token as first message, wait for ack.
	if n.auth != nil {
		token, err := n.auth.Token()
		if err != nil {
			_ = conn.Close(websocket.StatusInternalError, "auth failed")
			return err
		}
		authMsg, _ := json.Marshal(map[string]string{"auth": token})
		if err := conn.Write(ctx, websocket.MessageText, authMsg); err != nil {
			_ = conn.Close(websocket.StatusInternalError, "auth write failed")
			return err
		}
		// Read auth ack from server.
		ackCtx, ackCancel := context.WithTimeout(ctx, 10*time.Second)
		_, ackData, err := conn.Read(ackCtx)
		ackCancel()
		if err != nil {
			_ = conn.Close(websocket.StatusPolicyViolation, "auth ack timeout")
			return ErrAuthFailed
		}
		var ack struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(ackData, &ack); err != nil || ack.Type != "auth_ok" {
			_ = conn.Close(websocket.StatusPolicyViolation, "auth rejected")
			return ErrAuthFailed
		}
	}

	connCtx, cancel := context.WithCancel(context.Background())
	wc := &wsConn{
		conn:     conn,
		addr:     addr,
		outbound: true,
		cancel:   cancel,
		done:     make(chan struct{}),
	}

	n.mu.Lock()
	if existing, ok := n.conns[addr]; ok {
		// Already connected (race with reconnect). Close the new conn.
		n.mu.Unlock()
		cancel()
		_ = conn.Close(websocket.StatusNormalClosure, "duplicate")
		_ = existing
		return nil
	}
	n.conns[addr] = wc
	n.mu.Unlock()

	n.wg.Add(1)
	go n.readLoop(connCtx, wc)

	return nil
}

// Disconnect closes the connection to a specific peer.
func (n *NetworkAdapter) Disconnect(addr string) error {
	n.mu.Lock()
	wc, ok := n.conns[addr]
	if ok {
		delete(n.conns, addr)
	}
	n.mu.Unlock()

	if !ok {
		return nil
	}

	wc.cancel()
	_ = wc.conn.Close(websocket.StatusNormalClosure, "disconnect")
	<-wc.done
	return nil
}

// Close shuts down all connections and stops forwarding.
func (n *NetworkAdapter) Close() error {
	if n.closed.Swap(true) {
		return nil
	}

	if n.unsub != nil {
		n.unsub()
	}

	n.mu.Lock()
	for addr, wc := range n.conns {
		wc.cancel()
		_ = wc.conn.Close(websocket.StatusGoingAway, "closing")
		delete(n.conns, addr)
	}
	n.mu.Unlock()

	n.wg.Wait()
	return nil
}

// Handler returns an http.Handler that accepts inbound WebSocket peers.
func (n *NetworkAdapter) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}

		ctx := r.Context()

		// Auth handshake: expect token as first message with timeout.
		if n.auth != nil {
			authCtx, authCancel := context.WithTimeout(ctx, 10*time.Second)
			_, msg, err := conn.Read(authCtx)
			authCancel()
			if err != nil {
				_ = conn.Close(websocket.StatusPolicyViolation, "auth required")
				return
			}
			var authMsg struct {
				Auth string `json:"auth"`
			}
			if err := json.Unmarshal(msg, &authMsg); err != nil || authMsg.Auth == "" {
				_ = conn.Close(websocket.StatusPolicyViolation, "invalid auth")
				return
			}
			if err := n.auth.Verify(authMsg.Auth); err != nil {
				_ = conn.Close(websocket.StatusPolicyViolation, "auth failed")
				return
			}
			// Send auth ack.
			ack, _ := json.Marshal(map[string]string{"type": "auth_ok"})
			if err := conn.Write(ctx, websocket.MessageText, ack); err != nil {
				_ = conn.Close(websocket.StatusInternalError, "ack write failed")
				return
			}
		}

		addr := r.RemoteAddr
		connCtx, cancel := context.WithCancel(context.Background())
		wc := &wsConn{
			conn:     conn,
			addr:     addr,
			outbound: false,
			cancel:   cancel,
			done:     make(chan struct{}),
		}

		n.mu.Lock()
		n.conns[addr] = wc
		n.mu.Unlock()

		n.wg.Add(1)
		go n.readLoop(connCtx, wc)
	})
}

// forwardToRemotes sends a local event to all connected peers.
//
// If excludeOriginID is non-empty, peers whose learned peerOriginID
// equals excludeOriginID are skipped (relay mode origin exclusion,
// T-0182). The wire-level Origin field carries the source publisher's
// originID — preserved across relay so downstream peers can still
// dedupe via the existing msg.Origin == n.originID loop check.
func (n *NetworkAdapter) forwardToRemotes(e Event, excludeOriginID string) {
	if n.closed.Load() {
		return
	}

	if !n.filter.Match(string(e.Topic)) {
		return
	}

	// Wire Origin: in relay mode preserve the original publisher's
	// originID so the next hop's loop-prevention check still works.
	wireOrigin := n.originID
	if excludeOriginID != "" {
		wireOrigin = excludeOriginID
	}
	msg := networkMsg{
		Topic:     string(e.Topic),
		Source:    e.Source,
		Timestamp: e.Timestamp,
		Payload:   e.Payload,
		Origin:    wireOrigin,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	n.mu.RLock()
	conns := make([]*wsConn, 0, len(n.conns))
	for _, wc := range n.conns {
		if excludeOriginID != "" {
			if id, _ := wc.peerOriginID.Load().(string); id == excludeOriginID {
				continue
			}
		}
		conns = append(conns, wc)
	}
	n.mu.RUnlock()

	for _, wc := range conns {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := wc.conn.Write(ctx, websocket.MessageText, data)
		cancel()
		if err != nil {
			if wc.writeFail.Add(1) >= 3 {
				// Too many consecutive failures; close and trigger reconnect if outbound.
				n.mu.Lock()
				delete(n.conns, wc.addr)
				n.mu.Unlock()
				wc.cancel()
				_ = wc.conn.Close(websocket.StatusGoingAway, "write failures")
				if wc.outbound {
					go n.reconnectLoop(wc.addr)
				}
			}
		} else {
			wc.writeFail.Store(0)
		}
	}
}

// readLoop reads messages from a peer and injects them into the local bus.
func (n *NetworkAdapter) readLoop(ctx context.Context, wc *wsConn) {
	defer func() {
		close(wc.done)
		n.wg.Done()
	}()

	for {
		_, data, err := wc.conn.Read(ctx)
		if err != nil {
			// Connection closed or context canceled.
			n.mu.Lock()
			delete(n.conns, wc.addr)
			n.mu.Unlock()

			// Only reconnect outbound connections, with dedup guard.
			if wc.outbound && !n.closed.Load() && ctx.Err() == nil {
				n.mu.Lock()
				alreadyReconnecting := n.reconnecting[wc.addr]
				if !alreadyReconnecting {
					n.reconnecting[wc.addr] = true
				}
				n.mu.Unlock()
				if !alreadyReconnecting {
					go n.reconnectLoop(wc.addr)
				}
			}
			return
		}

		var msg networkMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		// Learn peer originID once (used by relay mode to exclude origin
		// when re-forwarding). Stored regardless of relay flag — cheap.
		if id, _ := wc.peerOriginID.Load().(string); id == "" && msg.Origin != "" {
			wc.peerOriginID.Store(msg.Origin)
		}

		// Loop prevention: don't re-inject events we originated.
		if msg.Origin == n.originID {
			continue
		}

		event := Event{
			Topic:     Topic(msg.Topic),
			Source:    msg.Source,
			Timestamp: msg.Timestamp,
			Payload:   msg.Payload,
		}

		// Mark context so outbound forwarder skips this event (or
		// re-forwards to non-origin peers when relay is on).
		pubCtx := context.WithValue(ctx, networkOriginKey, msg.Origin)
		_ = n.bus.Publish(pubCtx, event)
	}
}

// reconnectLoop attempts to re-establish a dropped connection.
func (n *NetworkAdapter) reconnectLoop(addr string) {
	defer func() {
		n.mu.Lock()
		delete(n.reconnecting, addr)
		n.mu.Unlock()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Stop retrying when adapter closes.
	go func() {
		for !n.closed.Load() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
		cancel()
	}()

	_ = util.Retry(ctx, n.reconnect, func() error {
		dialCtx, dialCancel := context.WithTimeout(ctx, 10*time.Second)
		defer dialCancel()
		return n.Connect(dialCtx, addr)
	})
}

// WithNetworkOption appends NetworkOptions used when constructing
// the NetworkAdapter via WithNetwork.
func WithNetworkOption(opts ...NetworkOption) Option {
	return func(o *busOpts) {
		o.networkOpts = append(o.networkOpts, opts...)
	}
}

// WithNetwork returns a BusOption that attaches a NetworkAdapter
// and auto-connects to the provided addresses.
func WithNetwork(addrs ...string) Option {
	return func(o *busOpts) {
		o.networkAddrs = addrs
	}
}

func randomID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
