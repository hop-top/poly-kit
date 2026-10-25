package bus

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"sync/atomic"
	"time"

	"hop.top/kit/go/core/netpolicy"
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
	// WorkspaceID mirrors Event.WorkspaceID. Omitted when empty, so
	// global events keep the original frame shape, and frames from
	// older peers, which never carry the key, decode as global.
	WorkspaceID string `json:"workspace_id,omitempty"`
	// ID mirrors Event.ID; omitted when empty. Older peers ignore it,
	// and frames without it decode with an empty ID.
	ID string `json:"id,omitempty"`
	// Offset is the sender's stream offset, a decimal string, sent on
	// stream links only.
	Offset string `json:"offset,omitempty"`
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
// The auth exchange is also the stream handshake: without an
// authenticator every link is a legacy link (see [OpenAuth]).
func WithAuth(a Authenticator) NetworkOption {
	return func(n *NetworkAdapter) { n.auth = a }
}

// WithRelay enables star-topology relay mode. When enabled, inbound
// network events are re-forwarded to all connected peers EXCEPT the
// origin peer. Default off — preserves prior point-to-point semantics.
//
// Use on a hub adapter that bridges multiple peers.
func WithRelay(enabled bool) NetworkOption {
	return func(n *NetworkAdapter) { n.relay = enabled }
}

// WithNetworkErrorFunc receives errors the adapter cannot return to a
// caller: session errors on reconnect, protocol errors, encode errors
// and skipped entries.
func WithNetworkErrorFunc(f ErrFunc) NetworkOption {
	return func(n *NetworkAdapter) { n.onErr = f }
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
//
// Every adapter keeps a log of the events it forwards; each link has a
// cursor into it and one writer. See the bus streams design for the
// wire protocol.
type NetworkAdapter struct {
	bus          Bus
	conns        map[string]*link
	peers        map[string]*peerState
	skipped      map[string]*atomic.Uint64
	reconnecting map[string]bool
	mu           sync.RWMutex
	reconnect    util.RetryConfig
	filter       TopicFilter
	originID     string
	auth         Authenticator
	relay        bool // star-topology relay (re-forward inbound to non-origin peers)
	frames       FrameFormat
	offline      atomic.Bool
	closed       atomic.Bool
	wg           sync.WaitGroup
	unsub        Unsubscribe
	onErr        ErrFunc

	log       StreamLog
	logSize   int
	ordered   bool // the bus reports accepted events in order
	notifyMu  sync.Mutex
	notify    chan struct{}
	heartbeat time.Duration
	newTicker func(time.Duration) (<-chan time.Time, func())
	testHook  func(event string)

	maxSubs      int
	initialSubs  []string
	scope        *Scope
	seeds        map[string]StreamPosition
	outbox       bool
	onOutboxDrop func(addr string, n uint64)
	onSkip       func(addr string, n uint64)
	authorize    func(context.Context, Peer, Subscription) error
	linkFilter   func(Peer, Event) bool
}

// NewNetworkAdapter creates a network adapter bridging the given bus.
func NewNetworkAdapter(b Bus, opts ...NetworkOption) *NetworkAdapter {
	n := &NetworkAdapter{
		bus:          b,
		conns:        make(map[string]*link),
		peers:        make(map[string]*peerState),
		skipped:      make(map[string]*atomic.Uint64),
		reconnecting: make(map[string]bool),
		reconnect: util.RetryConfig{
			BaseDelay: 100 * time.Millisecond,
			MaxDelay:  30 * time.Second,
			Jitter:    true,
		},
		originID:  randomID(),
		notify:    make(chan struct{}),
		heartbeat: DefaultHeartbeat,
		maxSubs:   DefaultMaxSubscriptions,
		seeds:     map[string]StreamPosition{},
		newTicker: func(d time.Duration) (<-chan time.Time, func()) {
			t := time.NewTicker(d)
			return t.C, t.Stop
		},
	}
	for _, o := range opts {
		o(n)
	}
	if n.log == nil {
		n.log = NewRingLog("", n.logSize)
	}

	// Log every forwarded event. With the publish hook the log is in
	// publish order and the adapter speaks streams; without it, an
	// async subscriber keeps the previous unordered forwarding.
	if po := publishObserverOf(b); po != nil {
		n.ordered = true
		n.unsub = po.ObservePublished(n.observe)
	} else {
		n.unsub = b.SubscribeAsync("#", func(ctx context.Context, e Event) { n.observe(ctx, e) })
	}
	return n
}

// observe appends an accepted event to the log when the adapter would
// forward it: local events, and network events in relay mode.
func (n *NetworkAdapter) observe(ctx context.Context, e Event) {
	if n.closed.Load() || isReservedTopic(e.Topic) {
		return
	}
	origin := n.originID
	if v := ctx.Value(networkOriginKey); v != nil {
		if !n.relay {
			return
		}
		origin, _ = v.(string)
	}
	if !n.filter.Match(string(e.Topic)) {
		return
	}
	n.log.Append(LogEntry{Event: e, Origin: origin})
	n.wake()
}

// appendMarker logs a gap marker for downstream stream links.
func (n *NetworkAdapter) appendMarker() {
	n.log.Append(LogEntry{Marker: true})
	n.wake()
}

// waitCh returns a channel closed by the next append.
func (n *NetworkAdapter) waitCh() <-chan struct{} {
	n.notifyMu.Lock()
	defer n.notifyMu.Unlock()
	return n.notify
}

func (n *NetworkAdapter) wake() {
	n.notifyMu.Lock()
	close(n.notify)
	n.notify = make(chan struct{})
	n.notifyMu.Unlock()
}

func (n *NetworkAdapter) caps() streamCaps {
	c := streamCaps{Frames: append([]string(nil), knownFrames...)}
	if n.ordered {
		c.Streams = StreamsVersion
	}
	return c
}

func (n *NetworkAdapter) reportErr(err error) {
	if n.onErr != nil && err != nil {
		n.onErr(err)
	}
}

func (n *NetworkAdapter) hook(event string) {
	if n.testHook != nil {
		n.testHook(event)
	}
}

// baseCtx returns the root context for adapter-owned goroutines. A
// reconnect outlives the caller's context, so it cannot inherit it — but
// starting from a bare Background would silently drop the offline marker
// and let a dropped peer be re-dialed on a run that asked for no
// network. Connect records the policy it saw; reconnects replay it.
func (n *NetworkAdapter) baseCtx() context.Context {
	if n.offline.Load() {
		return netpolicy.WithOffline(context.Background(), true)
	}
	return context.Background()
}

// Disconnect closes the connection to a specific peer and forgets the
// address: no reconnect, and its outbox and position are dropped.
func (n *NetworkAdapter) Disconnect(addr string) error {
	n.mu.Lock()
	l, ok := n.conns[addr]
	ps := n.peers[addr]
	delete(n.peers, addr)
	n.mu.Unlock()
	if ps != nil {
		ps.setFinal("")
	}
	if !ok {
		return nil
	}
	l.close(statusNormal, "disconnect", "")
	<-l.done
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
	links := make([]*link, 0, len(n.conns))
	for _, l := range n.conns {
		links = append(links, l)
	}
	n.mu.Unlock()
	for _, l := range links {
		l.close(statusGoingAway, "closing", "")
	}
	n.wg.Wait()
	return nil
}

// Dial registers addr as an outbound peer and connects in the
// background, retrying with backoff until it succeeds, Disconnect, or
// Close. With [WithOutbox], events published from now on are kept for
// the peer even before the first connect.
func (n *NetworkAdapter) Dial(addr string) {
	n.peerFor(addr)
	n.goReconnect(addr)
}

// dialConfigured registers addr, tries one connect, and keeps retrying
// in the background unless the failure is final.
func (n *NetworkAdapter) dialConfigured(addr string) {
	n.peerFor(addr)
	if err := n.connect(context.Background(), addr, true); err != nil && !isFinalConnectErr(err) {
		n.goReconnect(addr)
	}
}

// goReconnect starts a reconnect loop for addr, tracked by the adapter's
// WaitGroup so Close waits for it.
func (n *NetworkAdapter) goReconnect(addr string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed.Load() || n.reconnecting[addr] || n.peers[addr] == nil {
		return
	}
	n.reconnecting[addr] = true
	n.wg.Add(1)
	go n.reconnectLoop(addr)
}

// reconnectLoop dials addr until connected, Disconnect, Close, or a
// final failure. Start it through goReconnect.
func (n *NetworkAdapter) reconnectLoop(addr string) {
	defer n.wg.Done()
	defer func() {
		n.mu.Lock()
		delete(n.reconnecting, addr)
		n.mu.Unlock()
	}()

	ctx, cancel := context.WithCancel(n.baseCtx())
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
		if err := n.connect(dialCtx, addr, true); err != nil && !isFinalConnectErr(err) {
			return err
		}
		return nil
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
// and auto-connects to the provided addresses. A failed first dial
// keeps retrying in the background.
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
