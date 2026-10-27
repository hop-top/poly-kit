package bus

import (
	"context"
	"encoding/json"
	"sort"
	"time"
)

// DefaultAckTimeout bounds an acknowledged publish whose context has no
// deadline.
const DefaultAckTimeout = 30 * time.Second

// WithAcks makes a dialer ask its acceptors for publish
// acknowledgements (caps.acks) and enables [NetworkAdapter.PublishAcked].
// It implies [WithOutbox]: events for an address are kept while it is
// down, and a reconnect sends again everything after the last
// acknowledged offset. Acceptors acknowledge whenever a dialer asks; they
// need no option.
func WithAcks() NetworkOption {
	return func(n *NetworkAdapter) {
		n.acks = true
		n.outbox = true
	}
}

// WithAckTimeout sets the wait of an acknowledged publish whose context
// has no deadline. Default [DefaultAckTimeout].
func WithAckTimeout(d time.Duration) NetworkOption {
	return func(n *NetworkAdapter) {
		if d > 0 {
			n.pubAckTimeout = d
		}
	}
}

// WithMaxPendingAcks bounds unresolved acknowledged publishes per
// outbound address (PublishAcked blocks for a slot until its context
// ends) and unanswered frames per inbound acks link (the link stops
// reading). Default [DefaultMaxPendingAcks].
func WithMaxPendingAcks(max int) NetworkOption {
	return func(n *NetworkAdapter) {
		if max > 0 {
			n.maxPendingAcks = max
		}
	}
}

// PublishAcked publishes e on the adapter's bus and waits until every
// local handler (sync and async) returned and every awaited outbound
// address acknowledged it: addrs, or every registered address when none
// are given. ctx bounds the wait; without a deadline the adapter's ack
// timeout applies.
//
// The error is nil only when everything acknowledged. Otherwise it is
// the local veto, ctx's error while waiting for a pending slot, or an
// [*AckError] for the first peer (by address) that did not acknowledge:
// [AckRejected], [AckInvalid] or [AckUnknown] from the peer, [AckTimeout],
// [AckEvicted], [AckUnavailable], [AckFiltered], or [AckUnsupported]
// (which also matches [ErrAcksUnsupported]). A timeout means unknown,
// not lost: retry with the same event id, and let the receiver dedupe.
//
// It returns [ErrAcksUnsupported] without publishing when the adapter
// was built without [WithAcks] or its bus cannot track handlers.
func (n *NetworkAdapter) PublishAcked(ctx context.Context, e Event, addrs ...string) (Ack, error) {
	if !n.acks || !tracksHandlers(n.bus) {
		return Ack{}, ErrAcksUnsupported
	}
	if n.closed.Load() {
		return Ack{}, ErrBusClosed
	}
	waitCtx := ctx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, n.pubAckTimeout)
		defer cancel()
	}

	t := newAckTicket(e.ID, true)
	type target struct {
		addr string
		ps   *peerState
	}
	var targets []target
	n.mu.RLock()
	if len(addrs) == 0 {
		for addr, ps := range n.peers {
			targets = append(targets, target{addr, ps})
		}
	} else {
		for _, addr := range addrs {
			targets = append(targets, target{addr, n.peers[addr]})
		}
	}
	n.mu.RUnlock()
	sort.Slice(targets, func(i, j int) bool { return targets[i].addr < targets[j].addr })

	for i, tg := range targets {
		if tg.ps == nil {
			t.expect(tg.addr, nil)
			t.resolve(tg.addr, AckUnavailable, "unknown address")
			continue
		}
		select {
		case tg.ps.ackSlots <- struct{}{}:
		case <-waitCtx.Done():
			for _, done := range targets[:i] {
				if done.ps != nil {
					t.resolve(done.addr, AckUnavailable, "")
				}
			}
			return Ack{}, waitCtx.Err()
		}
		ps := tg.ps
		t.expect(tg.addr, func() { <-ps.ackSlots })
	}

	// The observer logs the event under the bus lock and registers it
	// with each target's tracker before any writer can send it.
	t.onLogged = func(off uint64) {
		for _, tg := range targets {
			if tg.ps == nil {
				continue
			}
			tg.ps.ackMu.Lock()
			tg.ps.acks.register(off, t)
			tg.ps.ackMu.Unlock()
		}
	}
	return t.run(ctx, waitCtx, n.bus, e, func() {
		off := t.logged()
		for _, tg := range targets {
			if tg.ps == nil {
				continue
			}
			switch {
			case off == 0:
				t.resolve(tg.addr, AckFiltered, "topic filter")
			case n.peer(tg.addr) != tg.ps || n.closed.Load():
				t.resolve(tg.addr, AckUnavailable, "")
			case off < n.log.Oldest():
				t.resolve(tg.addr, AckEvicted, "")
			}
		}
	})
}

// tracksHandlers reports whether b counts async handlers for a ticket.
func tracksHandlers(b Bus) bool {
	_, ok := publishObserverOf(b).(*memBus)
	return ok
}

// applyAcks hands resolutions for addr to their tickets.
func applyAcks(addr string, rs []ackResolution) {
	for _, r := range rs {
		if r.T != nil {
			r.T.resolve(addr, r.Code, r.Message)
		}
	}
}

// failAcks resolves every wait of ps with code.
func (n *NetworkAdapter) failAcks(ps *peerState, code string) {
	ps.ackMu.Lock()
	rs := ps.acks.failAll(code)
	ps.ackMu.Unlock()
	applyAcks(ps.addr, rs)
}

// onPuback applies a puback on a dialer's acks link.
func (l *link) onPuback(m map[string]json.RawMessage) bool {
	p, err := parsePuback(m)
	if err != nil {
		return l.protocolError(err)
	}
	ps := l.ps
	ps.ackMu.Lock()
	rs, err := ps.acks.onPuback(p)
	if len(ps.acks.waits) > 2*cap(ps.ackSlots) {
		ps.acks.prune(ps.addr)
	}
	ps.ackMu.Unlock()
	if err != nil {
		return l.protocolError(err)
	}
	applyAcks(ps.addr, rs)
	l.n.hook("puback:" + formatOffset(p.Offset))
	return true
}

// ackSent records a frame the dialer's writer is about to send.
func (l *link) ackSent(e LogEntry) {
	ps := l.ps
	ps.ackMu.Lock()
	r := ps.acks.onSend(e.Offset, e.ticket, l.acks)
	ps.ackMu.Unlock()
	if r != nil {
		applyAcks(ps.addr, []ackResolution{*r})
	}
}

// ackSkipped resolves an entry the dialer's writer did not send.
func (l *link) ackSkipped(e LogEntry) {
	if !l.outbound || e.ticket == nil {
		return
	}
	ps := l.ps
	ps.ackMu.Lock()
	r := ps.acks.onSkip(e.Offset, e.ticket)
	ps.ackMu.Unlock()
	if r != nil {
		applyAcks(ps.addr, []ackResolution{*r})
	}
}

// ackGap resolves waits for entries evicted before the writer reached
// them.
func (l *link) ackGap(before, first uint64) {
	ps := l.ps
	ps.ackMu.Lock()
	rs := ps.acks.onGap(before, first)
	ps.ackMu.Unlock()
	applyAcks(ps.addr, rs)
}

// onAckedEvent handles an event frame on an acceptor's acks link: every
// frame gets a puback, in offset order, once its handlers are done. A
// frame sent again after a reconnect is acknowledged without being
// published twice.
func (l *link) onAckedEvent(data []byte) bool {
	n := l.n
	e, origin, off, err := decodeEventFrame(data, true)
	if err != nil {
		if isProtocolError(err) {
			return l.protocolError(err)
		}
		// Unusable, but it must still be answered, or a later
		// cumulative puback would acknowledge it.
		raw := frameOffsetRaw(data)
		v, ok := parseOffsetToken(raw)
		if !ok {
			return l.protocolError(protocolErr(pkOffset, "event frame without a valid offset"))
		}
		res, dup := n.ackSeen.begin(l.peer.Token, l.dialerStream, v, "")
		if !dup {
			res.complete(AckInvalid, err.Error(), l.settleFn())
		}
		return l.queueAck(v, "", res)
	}
	if l.peerOrigin() == "" && origin != "" {
		l.learnedOrigin.Store(origin)
	}
	res, dup := n.ackSeen.begin(l.peer.Token, l.dialerStream, off.value, e.ID)
	if !l.queueAck(off.value, e.ID, res) {
		return false
	}
	if dup {
		return true
	}
	settle := l.settleFn()
	switch {
	case origin == n.originID:
		res.complete("", "", settle) // our own event, come back
		return true
	case isReservedTopic(e.Topic):
		res.complete(AckInvalid, "reserved topic", settle)
		return true
	}
	if origin == "" {
		if origin = l.peerOrigin(); origin == "" {
			origin = n.originID
		}
	}
	ctx := context.WithValue(l.ctx, networkOriginKey, origin)
	ctx = context.WithValue(ctx, viaKey{}, l.id)
	t := newAckTicket(e.ID, false)
	t.onLocal = func() { res.complete("", "", settle) }
	if err := n.bus.Publish(context.WithValue(ctx, ackTicketKey{}, t), e); err != nil {
		res.complete(AckRejected, err.Error(), settle)
		return true
	}
	t.localDone()
	return true
}

// settleFn records a settled result in the dedupe memory.
func (l *link) settleFn() func(pubackInfo) {
	n, peer, stream := l.n, l.peer.Token, l.dialerStream
	return func(p pubackInfo) { n.ackSeen.settle(peer, stream, p) }
}

// queueAck appends a frame's result to the link's puback queue, waiting
// for room; the reader stops, which backpressures the dialer.
func (l *link) queueAck(off uint64, id string, res *ackResult) bool {
	for {
		ok, space := l.ackq.push(off, id, res)
		if ok {
			return true
		}
		select {
		case <-space:
		case <-l.ctx.Done():
			return false
		}
	}
}

// flushAcks sends the settled prefix of the puback queue.
func (l *link) flushAcks() bool {
	if l.ackq == nil {
		return true
	}
	for _, p := range l.ackq.ready() {
		if !l.write(encodePuback(p)) {
			return false
		}
	}
	return true
}

// ackWait is the writer's wake-up for pubacks; nil without acks.
func (l *link) ackWait() <-chan struct{} {
	if l.ackq == nil {
		return nil
	}
	return l.ackq.wait()
}

// frameOffsetRaw returns the offset member of an event frame of either
// format, without decoding the event.
func frameOffsetRaw(data []byte) json.RawMessage {
	_, members, err := frameKind(data)
	if err != nil {
		return nil
	}
	if raw, ok := members["offset"]; ok {
		return raw
	}
	return members[OffsetExtension]
}
