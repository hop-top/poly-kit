package bus

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Stream defaults.
const (
	// DefaultHeartbeat is the acceptor's heartbeat interval on stream
	// links; a dialer closes a link after three silent intervals.
	DefaultHeartbeat = 15 * time.Second
	// DefaultMaxSubscriptions caps the patterns one link may subscribe.
	DefaultMaxSubscriptions = 1000
)

// WithStreamLog sets the size of the default ring log (entries). Size
// it for the reconnect window of the adapter's dialers.
func WithStreamLog(size int) NetworkOption {
	return func(n *NetworkAdapter) { n.logSize = size }
}

// WithLog replaces the default ring log, for example with a durable
// log that keeps its stream id across restarts.
func WithLog(l StreamLog) NetworkOption {
	return func(n *NetworkAdapter) { n.log = l }
}

// WithHeartbeat sets the interval at which an acceptor proves a quiet
// stream link alive. It is advertised to dialers in the ack, and is
// clamped to 1 s - 24 h.
func WithHeartbeat(d time.Duration) NetworkOption {
	return func(n *NetworkAdapter) {
		switch {
		case d <= 0:
		case d < minHeartbeatMs*time.Millisecond:
			n.heartbeat = minHeartbeatMs * time.Millisecond
		case d > maxHeartbeatMs*time.Millisecond:
			n.heartbeat = maxHeartbeatMs * time.Millisecond
		default:
			n.heartbeat = d
		}
	}
}

// WithMaxSubscriptions caps the patterns one link may subscribe.
func WithMaxSubscriptions(max int) NetworkOption {
	return func(n *NetworkAdapter) {
		if max > 0 {
			n.maxSubs = max
		}
	}
}

// WithSubscribe sets the patterns this dialer asks every acceptor for,
// in the handshake. Without it a link receives every topic.
func WithSubscribe(patterns ...string) NetworkOption {
	return func(n *NetworkAdapter) { n.initialSubs = append([]string{}, patterns...) }
}

// WithScope limits what this dialer asks every acceptor for to some
// workspaces (global events always pass).
func WithScope(s Scope) NetworkOption {
	return func(n *NetworkAdapter) { n.scope = &s }
}

// WithResumeFrom seeds the position for addr, for a process that
// persisted it; the first connect then asks to resume after it.
func WithResumeFrom(addr string, p StreamPosition) NetworkOption {
	return func(n *NetworkAdapter) { n.seeds[addr] = p }
}

// WithOutbox keeps events published while an outbound peer is
// disconnected and sends them, in order, when it reconnects. The log
// size bounds it; the oldest events are dropped first and counted.
func WithOutbox() NetworkOption {
	return func(n *NetworkAdapter) { n.outbox = true }
}

// WithOutboxDropFunc is called with each batch of events the outbox
// dropped for addr, when the flush finds them.
func WithOutboxDropFunc(f func(addr string, n uint64)) NetworkOption {
	return func(n *NetworkAdapter) { n.onOutboxDrop = f }
}

// WithSkipFunc is called with each batch of entries a slow link skipped
// because the log evicted them first.
func WithSkipFunc(f func(addr string, n uint64)) NetworkOption {
	return func(n *NetworkAdapter) { n.onSkip = f }
}

// WithSubscribeAuthorizer checks every effective subscription of an
// inbound link: at the handshake (including the implicit "#"), at
// accept time for legacy links, and on each subscribe request. An
// error refuses it with code forbidden.
//
// It must be fast and must not block: subscribe requests are
// authorized on the link's writer, which also sends events and
// heartbeats. That keeps the ack's offset an exact fence; a slow
// authorizer delays the link, and one slower than three heartbeat
// intervals gets the link closed by the dialer.
func WithSubscribeAuthorizer(f func(ctx context.Context, p Peer, s Subscription) error) NetworkOption {
	return func(n *NetworkAdapter) { n.authorize = f }
}

// WithLinkFilter adds a per-link event predicate, applied on replay and
// live delivery on every link. Gap markers bypass it.
func WithLinkFilter(f func(p Peer, e Event) bool) NetworkOption {
	return func(n *NetworkAdapter) { n.linkFilter = f }
}

// Peer identifies the remote end of a link for authorizers and link
// filters. Token is what the link presented in its auth frame.
type Peer struct {
	Addr   string
	Origin string
	Token  string
}

// StreamPosition is delivery metadata: where an event sits in the
// stream of the peer it came from. It is not part of the event.
type StreamPosition struct {
	Peer   string
	Stream string
	Offset uint64
}

type streamPosKey struct{}

// StreamPositionFrom returns the position of an event a dialer received
// over a stream link, from the handler's context.
func StreamPositionFrom(ctx context.Context) (StreamPosition, bool) {
	p, ok := ctx.Value(streamPosKey{}).(StreamPosition)
	return p, ok
}

// LinkState describes an outbound address.
type LinkState struct {
	Connected bool
	Streams   bool        // the link negotiated streams
	Format    FrameFormat // negotiated send format
	Stream    string      // acceptor's stream id on a stream link
	Resume    string      // none, replaying, reset or unsupported
	Final     bool        // the adapter will not reconnect
	Code      string      // why the link last closed, if known
}

// peerState is everything the adapter keeps for an outbound address
// across reconnects.
type peerState struct {
	addr          string
	send          *sendCursor
	outboxDropped atomic.Uint64

	mu        sync.Mutex
	cursor    dialerCursor
	subs      subState
	connected bool // has connected at least once
	state     LinkState
	upAt      time.Time // when the current or last link came up
	flaps     int       // consecutive links shorter than the stability window

	ackMu    sync.Mutex
	acks     *ackTracker
	ackSlots chan struct{} // one per unresolved acknowledged publish
}

func (ps *peerState) setFinal(code string) {
	ps.mu.Lock()
	ps.state.Final = true
	if code != "" {
		ps.state.Code = code
	}
	ps.mu.Unlock()
}

// peerFor returns addr's state, registering it on first use.
func (n *NetworkAdapter) peerFor(addr string) *peerState {
	n.mu.Lock()
	defer n.mu.Unlock()
	if ps, ok := n.peers[addr]; ok {
		return ps
	}
	ps := &peerState{addr: addr, send: newSendCursor(n.log, n.outbox), subs: newSubState(),
		ackSlots: make(chan struct{}, n.maxPendingAcks)}
	ps.acks = newAckTracker(ps.send.at)
	if n.initialSubs != nil {
		ps.subs = ps.subs.withPatterns(n.initialSubs)
	}
	ps.subs.Scope = n.scope
	if p, ok := n.seeds[addr]; ok {
		ps.cursor = dialerCursor{Stream: p.Stream, Offset: p.Offset, Held: true}
	}
	n.peers[addr] = ps
	return ps
}

func (n *NetworkAdapter) peer(addr string) *peerState {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.peers[addr]
}

// Position returns the dialer's position for addr: the last offset it
// handled from that peer's stream.
func (n *NetworkAdapter) Position(addr string) (StreamPosition, bool) {
	ps := n.peer(addr)
	if ps == nil {
		return StreamPosition{}, false
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if !ps.cursor.Held {
		return StreamPosition{}, false
	}
	return StreamPosition{Peer: addr, Stream: ps.cursor.Stream, Offset: ps.cursor.Offset}, true
}

// LinkState reports the state of an outbound address.
func (n *NetworkAdapter) LinkState(addr string) (LinkState, bool) {
	ps := n.peer(addr)
	if ps == nil {
		return LinkState{}, false
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.state, true
}

// OutboxDropped counts events the outbox dropped for addr.
func (n *NetworkAdapter) OutboxDropped(addr string) uint64 {
	if ps := n.peer(addr); ps != nil {
		return ps.outboxDropped.Load()
	}
	return 0
}

// Skipped counts log entries a link to addr skipped because they were
// evicted before its writer reached them.
func (n *NetworkAdapter) Skipped(addr string) uint64 {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if c := n.skipped[addr]; c != nil {
		return c.Load()
	}
	return 0
}

func (n *NetworkAdapter) skipCounter(addr string) *atomic.Uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	c := n.skipped[addr]
	if c == nil {
		c = &atomic.Uint64{}
		n.skipped[addr] = c
	}
	return c
}

// OpenAuth returns an authenticator for deployments without a secret
// that still want the handshake (and so streams): it sends the token
// "open" and accepts any non-empty token.
func OpenAuth() Authenticator { return openAuth{} }

type openAuth struct{}

func (openAuth) Token() (string, error) { return "open", nil }
func (openAuth) Verify(token string) error {
	if token == "" {
		return ErrAuthFailed
	}
	return nil
}
