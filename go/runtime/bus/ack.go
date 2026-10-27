package bus

import (
	"context"
	"errors"
	"sort"
	"sync"
)

// ErrAcksUnsupported is returned by an acknowledged publish that cannot
// be confirmed: the bus cannot track its handlers, the adapter was built
// without [WithAcks], or (as an [*AckError] with code [AckUnsupported])
// a peer did not negotiate acks.
var ErrAcksUnsupported = errors.New("bus: acknowledged publish unsupported")

// ErrNack matches every [*AckError] with errors.Is.
var ErrNack = errors.New("bus: publish not acknowledged")

// AckError reports a publish that was not acknowledged. Peer is the
// outbound address, empty for local handlers.
type AckError struct {
	Peer    string
	Code    string
	Message string
}

func (e *AckError) Error() string {
	s := "bus: publish not acknowledged"
	if e.Peer != "" {
		s += " by " + redactAddr(e.Peer)
	}
	s += ": " + e.Code
	if e.Message != "" {
		s += ": " + e.Message
	}
	return s
}

// Is reports whether target is [ErrNack], or [ErrAcksUnsupported] for
// code [AckUnsupported].
func (e *AckError) Is(target error) bool {
	return target == ErrNack || (target == ErrAcksUnsupported && e.Code == AckUnsupported)
}

// PeerAck is one peer's answer to an acknowledged publish. An empty Code
// means acknowledged.
type PeerAck struct {
	Peer    string
	Code    string
	Message string
}

// Ack reports an acknowledged publish: the event id, its offset in the
// sender's stream (0 when not logged), and each awaited peer in address
// order.
type Ack struct {
	ID     string
	Offset uint64
	Peers  []PeerAck

	published bool // the bus accepted the event
}

// AckPublisher publishes an event and waits until it is handled: every
// local sync and async handler returned, and every awaited peer
// acknowledged it. The error is nil only then; otherwise it is the sync
// handler's veto, an [*AckError] for the first peer that did not
// acknowledge, or ctx's error.
type AckPublisher interface {
	PublishAcked(ctx context.Context, e Event) (Ack, error)
}

// AckPublisherOf returns the acknowledged-publish side of b: a bus from
// [New] without a network, or with a network built with [WithAcks], or
// any other value implementing [AckPublisher]. It returns nil when b cannot confirm delivery to everything it forwards
// to, so a caller can fall back to Publish knowingly.
func AckPublisherOf(b any) AckPublisher {
	switch w := b.(type) {
	case *memBus:
		return w
	case *networkedBus:
		if w.network.acks {
			return w
		}
	case *TeeBus:
		if AckPublisherOf(w.bus) != nil {
			return w
		}
	case *adapterBus:
		if _, ok := w.adapter.(*MemoryAdapter); ok {
			return w
		}
	case AckPublisher:
		return w // a bus of the caller's own that confirms delivery
	}
	return nil
}

// PublishAcked publishes e on b and waits for it to be handled; see
// [AckPublisher]. It returns [ErrAcksUnsupported] when [AckPublisherOf]
// finds nothing.
func PublishAcked(ctx context.Context, b Bus, e Event) (Ack, error) {
	ap := AckPublisherOf(b)
	if ap == nil {
		return Ack{}, ErrAcksUnsupported
	}
	return ap.PublishAcked(ctx, e)
}

// PublishAcked publishes e and waits for every handler, sync and async.
func (b *memBus) PublishAcked(ctx context.Context, e Event) (Ack, error) {
	t := newAckTicket(e.ID, false)
	return t.run(ctx, ctx, b, e, nil)
}

// PublishAcked checks the topic and publishes through the memory
// adapter's bus.
func (a *adapterBus) PublishAcked(ctx context.Context, e Event) (Ack, error) {
	ma, ok := a.adapter.(*MemoryAdapter)
	if !ok {
		return Ack{}, ErrAcksUnsupported
	}
	if err := checkTopic(a.enforce, a.onInvalid, e.Topic); err != nil {
		return Ack{}, err
	}
	return ma.bus.PublishAcked(ctx, e)
}

// PublishAcked publishes through the wrapped bus, then drains the sinks,
// as Publish does.
func (t *TeeBus) PublishAcked(ctx context.Context, e Event) (Ack, error) {
	ap := AckPublisherOf(t.bus)
	if ap == nil {
		return Ack{}, ErrAcksUnsupported
	}
	ack, err := ap.PublishAcked(ctx, e)
	if !ack.published {
		return ack, err // vetoed or never published
	}
	for _, s := range t.sinks {
		if derr := s.Drain(ctx, e); derr != nil && t.onErr != nil {
			t.onErr(derr)
		}
	}
	return ack, err
}

// PublishAcked waits for local handlers and every outbound address; see
// [NetworkAdapter.PublishAcked].
func (nb *networkedBus) PublishAcked(ctx context.Context, e Event) (Ack, error) {
	return nb.network.PublishAcked(ctx, e)
}

type ackTicketKey struct{}

func ticketFrom(ctx context.Context) *ackTicket {
	t, _ := ctx.Value(ackTicketKey{}).(*ackTicket)
	return t
}

// ackTicket follows one acknowledged publish: the local handlers still
// running and each awaited peer.
type ackTicket struct {
	id      string
	network bool // the network adapter logs and tracks it

	mu       sync.Mutex
	offset   uint64
	local    int // the publish itself, then one per async handler
	peers    map[string]*peerWait
	open     int // unresolved peers
	done     chan struct{}
	closed   bool
	onLocal  func()           // called once local handlers are done (acceptor)
	onLogged func(off uint64) // called by the adapter's observer, under the bus lock
}

type peerWait struct {
	res      *PeerAck
	release  func()
	resolved bool
}

func newAckTicket(id string, network bool) *ackTicket {
	return &ackTicket{id: id, network: network, local: 1, peers: map[string]*peerWait{}, done: make(chan struct{})}
}

// expect adds a peer to wait for; release frees its pending slot once.
func (t *ackTicket) expect(addr string, release func()) {
	t.mu.Lock()
	t.peers[addr] = &peerWait{release: release}
	t.open++
	t.mu.Unlock()
}

func (t *ackTicket) setOffset(off uint64) {
	t.mu.Lock()
	t.offset = off
	t.mu.Unlock()
}

func (t *ackTicket) logged() uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.offset
}

func (t *ackTicket) addLocal() {
	t.mu.Lock()
	t.local++
	t.mu.Unlock()
}

func (t *ackTicket) localDone() {
	t.mu.Lock()
	t.local--
	cb := t.local == 0 && t.onLocal != nil
	t.checkLocked()
	t.mu.Unlock()
	if cb {
		t.onLocal()
	}
}

// resolve records addr's outcome; the first one wins.
func (t *ackTicket) resolve(addr, code, msg string) {
	t.mu.Lock()
	w := t.peers[addr]
	if w == nil || w.resolved {
		t.mu.Unlock()
		return
	}
	w.resolved = true
	w.res = &PeerAck{Peer: addr, Code: code, Message: msg}
	t.open--
	rel := w.release
	t.checkLocked()
	t.mu.Unlock()
	if rel != nil {
		rel()
	}
}

func (t *ackTicket) resolvedFor(addr string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	w := t.peers[addr]
	return w == nil || w.resolved
}

// unresolved lists peers still waiting.
func (t *ackTicket) unresolved() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []string
	for addr, w := range t.peers {
		if !w.resolved {
			out = append(out, addr)
		}
	}
	return out
}

func (t *ackTicket) checkLocked() {
	if !t.closed && t.local == 0 && t.open == 0 {
		t.closed = true
		close(t.done)
	}
}

// run publishes e on b with the ticket in pubCtx and waits on waitCtx.
// after, when set, runs once the publish returned nil.
func (t *ackTicket) run(pubCtx, waitCtx context.Context, b Bus, e Event, after func()) (Ack, error) {
	err := b.Publish(context.WithValue(pubCtx, ackTicketKey{}, t), e)
	if err != nil {
		for _, addr := range t.unresolved() {
			t.resolve(addr, AckUnavailable, "")
		}
		return Ack{}, err
	}
	if after != nil {
		after()
	}
	t.localDone()
	select {
	case <-t.done:
	case <-waitCtx.Done():
		for _, addr := range t.unresolved() {
			t.resolve(addr, AckTimeout, "")
		}
	}
	return t.result()
}

func (t *ackTicket) result() (Ack, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	ack := Ack{ID: t.id, Offset: t.offset, published: true}
	var first error
	if t.local > 0 {
		first = &AckError{Code: AckTimeout, Message: "local handlers still running"}
	}
	addrs := make([]string, 0, len(t.peers))
	for addr := range t.peers {
		addrs = append(addrs, addr)
	}
	sort.Strings(addrs)
	for _, addr := range addrs {
		w := t.peers[addr]
		pa := PeerAck{Peer: addr, Code: AckTimeout}
		if w.res != nil {
			pa = *w.res
		}
		ack.Peers = append(ack.Peers, pa)
		if pa.Code != "" && first == nil {
			first = &AckError{Peer: addr, Code: pa.Code, Message: pa.Message}
		}
	}

	return ack, first
}
