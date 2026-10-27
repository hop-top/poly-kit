package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

const (
	statusNormal      = websocket.StatusNormalClosure
	statusGoingAway   = websocket.StatusGoingAway
	statusProtocol    = websocket.StatusProtocolError
	statusPolicy      = websocket.StatusPolicyViolation
	statusInternal    = websocket.StatusInternalError
	writeTimeout      = 5 * time.Second
	maxWriteFailures  = 3
	writerBatch       = 256
	deadAfterHeartbts = 3
)

// linkMsg is work for an acceptor's writer: a request to apply, or an
// error reply to send.
type linkMsg struct {
	req   *ctlRequest
	reply *ctlError
}

// link is one WebSocket connection. Its writer sends log entries after
// its cursor plus every control frame, so replies and events share one
// ordered sequence; its reader injects inbound events into the bus.
type link struct {
	id       uint64 // unique per link; see LogEntry.via
	n        *NetworkAdapter
	conn     *websocket.Conn
	addr     string
	outbound bool
	stream   bool        // negotiated a stream link
	acks     bool        // negotiated pubacks (dialer to acceptor)
	format   FrameFormat // send format
	peer     Peer
	ps       *peerState // outbound only
	cursor   *sendCursor
	subs     subState // writer-owned
	ctl      chan linkMsg
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{} // reader exited
	wdone    chan struct{} // writer exited

	learnedOrigin atomic.Value // string; legacy links learn it from frames
	frames        atomic.Uint64
	closeCode     atomic.Value // string
	closeOnce     sync.Once

	pendMu  sync.Mutex
	pending map[string]chan dialerCtl
	nextID  atomic.Uint64

	flushing   bool // first drain after link up (outbox flush)
	lastSent   uint64
	wroteSince bool
	writeFail  int
	heartbeatD time.Duration // dialer: acceptor's advertised interval

	dialerStream string    // acceptor of an acks link: the dialer's stream id
	ackq         *ackQueue // acceptor of an acks link: pubacks in frame order
}

func (n *NetworkAdapter) newLink(conn *websocket.Conn, addr string, outbound bool) *link {
	ctx, cancel := context.WithCancel(context.Background())
	return &link{
		id: linkIDs.Add(1),
		n:  n, conn: conn, addr: addr, outbound: outbound,
		subs: newSubState(), ctl: make(chan linkMsg, 16),
		ctx: ctx, cancel: cancel,
		done: make(chan struct{}), wdone: make(chan struct{}),
		pending: map[string]chan dialerCtl{}, flushing: true,
	}
}

// logAddr is the link's address for errors: a dial URL without its
// userinfo, query and fragment, which can carry credentials. An inbound
// address is the peer's host:port and is kept as is.
func (l *link) logAddr() string {
	if l.outbound {
		return redactAddr(l.addr)
	}
	return l.addr
}

func (l *link) peerOrigin() string {
	if l.peer.Origin != "" {
		return l.peer.Origin
	}
	s, _ := l.learnedOrigin.Load().(string)
	return s
}

// close closes the connection once, recording why.
func (l *link) close(status websocket.StatusCode, reason, code string) {
	l.closeOnce.Do(func() {
		if code != "" {
			l.closeCode.Store(code)
		}
		l.cancel()
		_ = l.conn.Close(status, reason)
	})
}

// start runs the link's goroutines. Tickers are created here, before
// start returns, so a link is fully armed once Connect or the accept
// handler is done.
// The caller registered the link and added it to the adapter's
// WaitGroup under n.mu, so Close cannot miss it.
func (l *link) start() {
	var tick <-chan time.Time
	stopTick := func() {}
	if l.stream && !l.outbound {
		tick, stopTick = l.n.newTicker(positiveDuration(l.n.heartbeat))
	}
	go l.writeLoop(tick, stopTick)
	go l.readLoop()
	if l.outbound && l.stream {
		c, stop := l.n.newTicker(positiveDuration(l.heartbeatD))
		go l.watchdog(c, stop)
	}
}

// positiveDuration guards ticker creation: time.NewTicker panics on a
// non-positive interval.
func positiveDuration(d time.Duration) time.Duration {
	if d <= 0 {
		return DefaultHeartbeat
	}
	return d
}

func (l *link) write(data []byte) bool {
	ctx, cancel := context.WithTimeout(l.ctx, writeTimeout)
	var err error
	if f := l.n.writeFault; f != nil {
		err = f(data)
	}
	if err == nil {
		err = l.conn.Write(ctx, websocket.MessageText, data)
	}
	cancel()
	if err != nil {
		l.writeFail++
		// A stream link closes on the first failure: the acceptor's
		// cursor has passed the frame and the dialer resumes it. An
		// outbox link closes too, and drain rewinds its cursor so the
		// next link sends the frame again.
		if l.stream || (l.outbound && l.n.outbox) || l.writeFail >= maxWriteFailures || l.ctx.Err() != nil {
			l.close(statusGoingAway, "write failures", "")
			return false
		}
		return true
	}
	l.writeFail = 0
	l.wroteSince = true
	return true
}

func (l *link) writeJSON(v any) bool {
	b, _ := json.Marshal(v)
	return l.write(b)
}

func (l *link) writeLoop(tick <-chan time.Time, stopTick func()) {
	defer close(l.wdone)
	defer stopTick()
	for {
		wake := l.n.waitCh()
		if !l.drain() || !l.flushAcks() {
			return
		}
		l.n.hook("drained:" + formatOffset(l.cursor.at))
		select {
		case <-l.ctx.Done():
			return
		case <-l.ackWait():
		case m := <-l.ctl:
			if !l.handleCtl(m) {
				return
			}
		case <-wake:
		case <-tick:
			if !l.heartbeatTick() {
				return
			}
			l.n.hook("heartbeat")
		}
	}
}

// drain sends every entry after the cursor that passes the link's
// filters. A gap means the log evicted entries first: an outbox drop on
// the first drain of an outbound link, a lagged reset on an acceptor's
// stream link, a counted skip otherwise.
func (l *link) drain() bool {
	n := l.n
	for {
		before := l.cursor.at
		entries, gap := l.cursor.next(n.log, writerBatch)
		flush := l.flushing
		l.flushing = false
		if len(entries) == 0 {
			return true
		}
		if gap > 0 && l.outbound {
			l.ackGap(before, entries[0].Offset)
		}
		if gap > 0 {
			switch {
			case l.stream && !l.outbound:
				l.cursor.at = n.log.Head()
				if !l.writeJSON(map[string]string{"type": "reset", "stream": n.log.ID(), "reason": ReasonLagged,
					"after": formatOffset(before), "head": formatOffset(l.cursor.at)}) {
					return false
				}
				l.lastSent = l.cursor.at
				continue
			case flush && l.outbound && n.outbox:
				l.ps.outboxDropped.Add(gap)
				if n.onOutboxDrop != nil {
					n.onOutboxDrop(l.addr, gap)
				}
			default:
				n.skipCounter(l.addr).Add(gap)
				if n.onSkip != nil {
					n.onSkip(l.addr, gap)
				}
				n.reportErr(fmt.Errorf("bus: link %s skipped %d evicted entries", l.logAddr(), gap))
			}
		}
		for _, e := range entries {
			if !l.send(e) {
				if l.outbound && n.outbox {
					l.cursor.at = e.Offset - 1 // resend it on the next link
				}
				return false
			}
		}
	}
}

func (l *link) send(e LogEntry) bool {
	n := l.n
	if e.via != 0 && e.via == l.id {
		l.ackSkipped(e)
		return true
	}
	if !entryDelivers(l.subs, l.peerOrigin(), e) {
		l.ackSkipped(e)
		return true
	}
	if e.Marker {
		if !l.stream || l.outbound {
			return true
		}
		l.lastSent = e.Offset
		return l.writeJSON(map[string]string{"type": "reset", "stream": n.log.ID(), "reason": ReasonUpstream,
			"after": formatOffset(e.Offset - 1), "head": formatOffset(e.Offset)})
	}
	if n.linkFilter != nil && !n.linkFilter(l.peer, e.Event) {
		l.ackSkipped(e)
		return true
	}
	data, err := encodeFrameOffset(l.format, e.Event, e.Origin, e.Offset, l.stream)
	if err != nil {
		n.reportErr(err)
		l.ackSkipped(e)
		return true
	}
	if l.outbound {
		l.ackSent(e)
	}
	l.lastSent = e.Offset
	return l.write(data)
}

// heartbeatTick sends a position frame when nothing was written since
// the last tick, or the cursor passed entries the link did not get.
func (l *link) heartbeatTick() bool {
	ok := true
	if !l.wroteSince || l.cursor.at > l.lastSent {
		ok = l.writeJSON(map[string]string{"type": "position", "stream": l.n.log.ID(), "offset": formatOffset(l.cursor.at)})
		l.lastSent = l.cursor.at
	}
	l.wroteSince = false
	return ok
}

// handleCtl applies a subscribe or unsubscribe request, or sends an
// error reply, in order with the event frames.
func (l *link) handleCtl(m linkMsg) bool {
	if m.reply != nil {
		return l.writeJSON(errorFrame(m.reply))
	}
	next, err := l.subs.apply(*m.req, l.n.maxSubs)
	if err == nil && m.req.Type == "subscribe" && l.n.authorize != nil {
		if aerr := l.n.authorize(l.ctx, l.peer, next.effective()); aerr != nil {
			err = streamErr(CodeForbidden, "%v", aerr)
		}
	}
	if err != nil {
		return l.writeJSON(errorFrame(&ctlError{m.req.ID, err}))
	}
	l.subs = next
	return l.writeJSON(map[string]string{"type": "ack", "id": m.req.ID, "at": formatOffset(l.cursor.at)})
}

func errorFrame(e *ctlError) map[string]string {
	f := map[string]string{"type": "error", "code": e.Code, "message": e.Message}
	if e.ID != "" {
		f["id"] = e.ID
	}
	return f
}

// watchdog closes a dialer's stream link after three heartbeat
// intervals with no frame of any kind.
func (l *link) watchdog(c <-chan time.Time, stop func()) {
	defer stop()
	last, misses := l.frames.Load(), 0
	for {
		select {
		case <-l.ctx.Done():
			return
		case <-c:
		}
		if cur := l.frames.Load(); cur != last {
			last, misses = cur, 0
			continue
		}
		if misses++; misses >= deadAfterHeartbts {
			l.close(statusGoingAway, "heartbeat timeout", "timeout")
			return
		}
	}
}

func (l *link) readLoop() {
	n := l.n
	defer func() {
		l.cancel()
		<-l.wdone
		n.mu.Lock()
		if n.conns[l.addr] == l {
			delete(n.conns, l.addr)
		}
		if !l.outbound {
			// Inbound addresses are ephemeral ports: drop their counter.
			delete(n.skipped, l.addr)
		}
		n.mu.Unlock()
		l.failPending()
		if l.outbound {
			l.linkDown()
		}
		close(l.done)
		n.wg.Done()
	}()
	for {
		_, data, err := l.conn.Read(l.ctx)
		if err != nil {
			return
		}
		l.frames.Add(1)
		kind, members := classifyFrame(data, l.stream)
		switch kind {
		case frameKindDrop:
			continue
		case frameKindControl:
			if !l.onControl(members) {
				return
			}
			continue
		}
		if l.ackq != nil {
			if !l.onAckedEvent(data) {
				return
			}
			continue
		}
		if !l.onEvent(data) {
			return
		}
	}
}

func (l *link) onControl(members map[string]json.RawMessage) bool {
	if !l.outbound {
		req, reply := acceptorControl(members)
		if req == nil && reply == nil {
			return true
		}
		select {
		case l.ctl <- linkMsg{req: req, reply: reply}:
			return true
		case <-l.ctx.Done():
			return false
		}
	}
	if l.acks {
		if typ, _ := memberString(members, "type"); typ == "puback" {
			return l.onPuback(members)
		}
	}
	dc, err := dialerControl(members)
	if err != nil {
		return l.protocolError(err)
	}
	switch dc.Kind {
	case "ack", "error":
		l.pendMu.Lock()
		ch := l.pending[dc.ID]
		delete(l.pending, dc.ID)
		l.pendMu.Unlock()
		if ch != nil {
			ch <- dc
		}
	case "reset":
		l.ps.mu.Lock()
		g, err := l.ps.cursor.onReset(dc.Reset)
		l.ps.mu.Unlock()
		if err != nil {
			return l.protocolError(err)
		}
		l.n.invalidate(l.addr, g)
	case "position":
		l.ps.mu.Lock()
		err := l.ps.cursor.onPosition(dc.Position)
		l.ps.mu.Unlock()
		if err != nil {
			return l.protocolError(err)
		}
		l.n.hook("position")
	}
	return true
}

func (l *link) onEvent(data []byte) bool {
	n := l.n
	e, origin, off, err := decodeEventFrame(data, l.stream)
	if err != nil {
		if isProtocolError(err) {
			return l.protocolError(err)
		}
		return true
	}
	if l.peerOrigin() == "" && origin != "" {
		l.learnedOrigin.Store(origin)
	}
	if origin == n.originID || isReservedTopic(e.Topic) {
		return true
	}
	if origin == "" {
		// A frame without an origin (a CloudEvents frame without
		// kitorigin, say) would slip past every loop check. Stamp it
		// with the link's origin, else this adapter's own.
		if origin = l.peerOrigin(); origin == "" {
			origin = n.originID
		}
	}
	ctx := context.WithValue(l.ctx, networkOriginKey, origin)
	ctx = context.WithValue(ctx, viaKey{}, l.id)
	if l.outbound {
		ctx = context.WithValue(ctx, deliveryKey{}, l.addr)
	}
	if l.stream && l.outbound {
		l.ps.mu.Lock()
		deliver := l.ps.cursor.advance(off.value)
		stream := l.ps.cursor.Stream
		l.ps.mu.Unlock()
		if !deliver {
			return true
		}
		ctx = context.WithValue(ctx, streamPosKey{}, StreamPosition{Peer: l.addr, Stream: stream, Offset: off.value})
	}
	_ = n.bus.Publish(ctx, e)
	return true
}

func (l *link) protocolError(err error) bool {
	l.n.reportErr(err)
	l.close(statusProtocol, "protocol error", "protocol")
	return false
}

func (l *link) failPending() {
	l.pendMu.Lock()
	defer l.pendMu.Unlock()
	for id, ch := range l.pending {
		close(ch)
		delete(l.pending, id)
	}
}

// linkDown publishes link.disconnected for an outbound link and
// reconnects unless the address is final or the adapter closed.
func (l *link) linkDown() {
	n := l.n
	code, _ := l.closeCode.Load().(string)
	ps := l.ps
	ps.mu.Lock()
	ps.state.Connected = false
	if code != "" {
		ps.state.Code = code
	}
	final := ps.state.Final
	// A link shorter than the stability window counts as a failed
	// attempt, so a peer that drops links right after they come up is
	// redialed with a growing backoff instead of at once.
	if time.Since(ps.upAt) < n.stableAfter() {
		ps.flaps++
	} else {
		ps.flaps = 0
	}
	ps.mu.Unlock()
	if n.closed.Load() {
		return
	}
	n.publishDisconnected(l.addr, final, code)
	if !final {
		n.goReconnectAfterDrop(l.addr)
	}
}
