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

// viaKey carries the id of the link an inbound event arrived on, so a
// relay never sends it back there, whatever origin the frame claimed.
type viaKey struct{}

// linkIDs numbers links from 1; 0 marks a local event.
var linkIDs atomic.Uint64

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
	// pendingRedial holds the redial request an address's running loop
	// has yet to serve; the value is whether that run waits the backoff
	// first. See requestRedial.
	pendingRedial map[string]bool
	// wakes holds each running loop's wake signal: a user request (Dial,
	// Connect) or Disconnect ends the loop's backoff wait at once.
	wakes     map[string]chan struct{}
	mu        sync.RWMutex
	reconnect util.RetryConfig
	filter    TopicFilter
	originID  string
	auth      Authenticator
	relay     bool // star-topology relay (re-forward inbound to non-origin peers)
	frames    FrameFormat
	offline   atomic.Bool
	closed    atomic.Bool
	wg        sync.WaitGroup
	unsub     Unsubscribe
	onErr     ErrFunc

	log       StreamLog
	logSize   int
	ordered   bool // the bus reports accepted events in order
	notifyMu  sync.Mutex
	notify    chan struct{}
	heartbeat time.Duration
	newTicker func(time.Duration) (<-chan time.Time, func())
	testHook  func(event string)
	// backoffWait is a test seam for a reconnect loop's backoff timer:
	// true lets the wait of d for attempt elapse at once, false holds it
	// until it is woken or canceled.
	backoffWait func(ctx context.Context, attempt int, d time.Duration) bool
	wakesSent   atomic.Int64            // test seam: wakes sent to backoff waits
	writeFault  func(data []byte) error // test seam: fail a write
	ackTimeout  time.Duration

	maxSubs      int
	initialSubs  []string
	scope        *Scope
	seeds        map[string]StreamPosition
	outbox       bool
	onOutboxDrop func(addr string, n uint64)
	onSkip       func(addr string, n uint64)
	authorize    func(context.Context, Peer, Subscription) error
	linkFilter   func(Peer, Event) bool

	acks           bool // dialer: ask acceptors for pubacks
	pubAckTimeout  time.Duration
	maxPendingAcks int
	ackSeen        *ackSeen
	refuseAcks     bool // test seam: an acceptor that predates acks
}

// NewNetworkAdapter creates a network adapter bridging the given bus.
func NewNetworkAdapter(b Bus, opts ...NetworkOption) *NetworkAdapter {
	n := &NetworkAdapter{
		bus:           b,
		conns:         make(map[string]*link),
		peers:         make(map[string]*peerState),
		skipped:       make(map[string]*atomic.Uint64),
		reconnecting:  make(map[string]bool),
		pendingRedial: make(map[string]bool),
		wakes:         make(map[string]chan struct{}),
		reconnect: util.RetryConfig{
			BaseDelay: 100 * time.Millisecond,
			MaxDelay:  30 * time.Second,
			Jitter:    true,
		},
		originID:   randomID(),
		notify:     make(chan struct{}),
		heartbeat:  DefaultHeartbeat,
		ackTimeout: handshakeTimeout,
		maxSubs:    DefaultMaxSubscriptions,
		ackSeen:    newAckSeen(),

		pubAckTimeout:  DefaultAckTimeout,
		maxPendingAcks: DefaultMaxPendingAcks,
		seeds:          map[string]StreamPosition{},
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
	via, _ := ctx.Value(viaKey{}).(uint64)
	entry := LogEntry{Event: e, Origin: origin, via: via}
	t := ticketFrom(ctx)
	if t != nil && t.network {
		entry.ticket = t
	}
	off := n.log.Append(entry)
	if entry.ticket != nil {
		t.setOffset(off)
		if t.onLogged != nil {
			t.onLogged(off)
		}
	}
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
		if n.acks {
			c.Acks = AcksVersion
		}
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
	delete(n.pendingRedial, addr)
	n.wakeLocked(addr)
	n.mu.Unlock()
	if ps != nil {
		ps.setFinal("")
		n.failAcks(ps, AckUnavailable)
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
	clear(n.pendingRedial)
	links := make([]*link, 0, len(n.conns))
	for _, l := range n.conns {
		links = append(links, l)
	}
	n.mu.Unlock()
	for _, l := range links {
		l.close(statusGoingAway, "closing", "")
	}
	n.wg.Wait()
	n.mu.RLock()
	peers := make([]*peerState, 0, len(n.peers))
	for _, ps := range n.peers {
		peers = append(peers, ps)
	}
	n.mu.RUnlock()
	for _, ps := range peers {
		n.failAcks(ps, AckUnavailable)
	}
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

// goReconnect starts a reconnect loop for addr that dials at once,
// tracked by the adapter's WaitGroup so Close waits for it.
func (n *NetworkAdapter) goReconnect(addr string) {
	n.requestRedial(addr, false)
}

// The redial state machine is documented on requestRedial.

// goReconnectAfterDrop starts a reconnect loop for an address whose link
// went down. It never dials at once: it first waits the backoff for the
// address's run of short-lived links (see stableAfter).
func (n *NetworkAdapter) goReconnectAfterDrop(addr string) {
	n.requestRedial(addr, true)
}

// requestRedial is the one entry to an address's redial state machine.
// Dial, WithNetwork's retry after a failed first connect, and the redial
// after a link drop all go through it; wait says whether the run waits
// the backoff before it dials (a drop) or dials now (a dial request).
//
// Per registered address, under n.mu:
//
//	state            event                        next
//	idle / up        request                      running (loop started; waits if wait)
//	running          request                      running + mark (wait = AND of the requests);
//	                                              a dial request (wait false) also wakes the wait
//	backoff-wait     wake (Dial, Connect,         wait ends; exit check (mark: dial now,
//	                 Disconnect)                  attempts from 0; forgotten: loop gone)
//	backoff-wait     request(wait) (a drop)       no wake: the full backoff stands
//	running          dial fails                   running (attempt+1, waits)
//	running          dial succeeds / gives up     exit check
//	exit check       mark, address kept, open     running (mark consumed; waits as marked)
//	exit check       no mark (or forgotten/closed) idle / up (loop gone)
//	up               link drops                   request(wait), flaps+1 or reset (stableAfter)
//	any              Disconnect                   address forgotten, mark cleared, wait woken; the loop exits at its check
//	any              Connect                      dials inline; a running loop gets mark(now) and a wake
//	any              Close                        closed, marks cleared; loops stop, nothing dials
//
// Explicit Connect dials inline and owns no loop: a link it makes joins
// the table at up.
//
// A request that finds a loop running never just returns: the loop
// consumes its mark under this lock before it exits, so every request
// is served by the running loop or starts one.
func (n *NetworkAdapter) requestRedial(addr string, wait bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed.Load() || n.peers[addr] == nil {
		return
	}
	if n.reconnecting[addr] {
		n.markLocked(addr, wait)
		return
	}
	n.reconnecting[addr] = true
	n.wakes[addr] = make(chan struct{}, 1)
	n.wg.Add(1)
	go n.reconnectLoop(addr, wait)
}

// markLocked hands a request to addr's running loop: the mark (wait is
// the AND of the pending requests) and, for a user request, a wake that
// ends the loop's backoff wait. A drop (wait) never wakes: it honors
// the full backoff. Call with n.mu held and a loop running.
func (n *NetworkAdapter) markLocked(addr string, wait bool) {
	if prev, ok := n.pendingRedial[addr]; ok {
		wait = wait && prev
	}
	n.pendingRedial[addr] = wait
	if !wait {
		n.wakeLocked(addr)
	}
}

// wakeLocked ends addr's loop's backoff wait, if a loop runs. Call with
// n.mu held.
func (n *NetworkAdapter) wakeLocked(addr string) {
	if ch := n.wakes[addr]; ch != nil {
		select {
		case ch <- struct{}{}:
		default: // a wake is already pending
		}
		n.wakesSent.Add(1)
	}
}

// stableAfter is the stability window: a link that stayed up this long
// resets its address's backoff. It is the maximum backoff, so a link
// counts as healthy once it outlived the longest wait the policy would
// impose, and the window scales with WithBackoff.
func (n *NetworkAdapter) stableAfter() time.Duration {
	if n.reconnect.MaxDelay > 0 {
		return n.reconnect.MaxDelay
	}
	return 30 * time.Second
}

// reconnectLoop dials addr until connected, Disconnect, Close, or a
// final failure, waiting the backoff between attempts. With wait it
// also waits before the first dial, starting at the address's count of
// consecutive short-lived links, so backoff persists across them. Start
// it through goReconnect or goReconnectAfterDrop.
func (n *NetworkAdapter) reconnectLoop(addr string, wait bool) {
	defer n.wg.Done()

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

	for {
		n.redial(ctx, addr, wait)
		n.hook("reconnect-exit")
		// Under the lock startReconnect takes: a drop either left its
		// mark before this point or starts a loop of its own after it.
		n.mu.Lock()
		if marked, ok := n.pendingRedial[addr]; ok && n.peers[addr] != nil && !n.closed.Load() && ctx.Err() == nil {
			delete(n.pendingRedial, addr)
			select { // the mark carried any wake: drop it with the mark
			case <-n.wakes[addr]:
			default:
			}
			n.mu.Unlock()
			wait = marked
			continue
		}
		delete(n.pendingRedial, addr)
		delete(n.reconnecting, addr)
		delete(n.wakes, addr)
		n.mu.Unlock()
		return
	}
}

// redial is one run of a reconnect loop: dial until connected, a final
// failure, ctx done or out of attempts.
func (n *NetworkAdapter) redial(ctx context.Context, addr string, wait bool) {
	attempt := 0
	if wait {
		if ps := n.peer(addr); ps != nil {
			ps.mu.Lock()
			attempt = ps.flaps
			ps.mu.Unlock()
		}
	}
	for tries := 1; ; tries++ {
		if wait && !n.waitBackoff(ctx, addr, attempt) {
			return
		}
		wait = true
		dialCtx, dialCancel := context.WithTimeout(ctx, 10*time.Second)
		err := n.connect(dialCtx, addr, true)
		dialCancel()
		if err == nil || isFinalConnectErr(err) || ctx.Err() != nil {
			return
		}
		if n.reconnect.MaxAttempts > 0 && tries >= n.reconnect.MaxAttempts {
			return
		}
		attempt++
	}
}

// waitBackoff waits the backoff for attempt; false once ctx is done.
// waitBackoff waits the backoff for attempt. It returns false, ending
// the run, when ctx is done (Close) or the loop is woken (Dial, Connect,
// Disconnect); the exit check then serves the mark or sees the address
// gone.
func (n *NetworkAdapter) waitBackoff(ctx context.Context, addr string, attempt int) bool {
	d := n.reconnect.Backoff(attempt)
	n.mu.RLock()
	wake := n.wakes[addr]
	n.mu.RUnlock()
	var timer <-chan time.Time
	if n.backoffWait == nil {
		timer = time.After(d)
	} else if n.backoffWait(ctx, attempt, d) {
		return ctx.Err() == nil
	}
	select {
	case <-ctx.Done():
		return false
	case <-wake:
		return false
	case <-timer:
		return true
	}
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
