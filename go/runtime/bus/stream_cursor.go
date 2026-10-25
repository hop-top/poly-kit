package bus

import "encoding/json"

// Reset reasons.
const (
	ReasonStream      = "stream"
	ReasonAhead       = "ahead"
	ReasonEvicted     = "evicted"
	ReasonLagged      = "lagged"
	ReasonUpstream    = "upstream"
	ReasonUnsupported = "unsupported"
)

// Resume statuses reported in link.connected.
const (
	ResumeNone        = "none"
	ResumeReplaying   = "replaying"
	ResumeReset       = "reset"
	ResumeUnsupported = "unsupported"
)

// decideResume is the acceptor's resume decision for a log (id, oldest,
// head) and an optional request. It returns the ack's outcome (nil when
// no resume was asked for) and the link's starting cursor.
func decideResume(id string, oldest, head uint64, r *resumeReq) (*resumeOutcome, uint64) {
	if r == nil {
		return nil, head
	}
	reset := func(reason string) (*resumeOutcome, uint64) {
		return &resumeOutcome{Status: ResumeReset, Reason: reason, After: r.After, Head: head}, head
	}
	switch {
	case r.Stream != id:
		return reset(ReasonStream)
	case r.After > head:
		return reset(ReasonAhead)
	case r.After < oldest-1: // oldest >= 1, so no underflow
		return reset(ReasonEvicted)
	}
	return &resumeOutcome{Status: ResumeReplaying, After: r.After, Head: head}, r.After
}

// gapInfo describes one invalidation the dialer publishes.
type gapInfo struct {
	Reason      string
	Stream      string
	After, Head uint64
}

// payload renders the kit.runtime.stream.invalidated payload. Offsets
// are decimal strings; stream and offsets are empty for unsupported.
func (g *gapInfo) payload(peer string) map[string]any {
	p := map[string]any{"peer": peer, "reason": g.Reason, "stream": "", "after": "", "head": ""}
	if g.Reason != ReasonUnsupported {
		p["stream"] = g.Stream
		p["after"] = formatOffset(g.After)
		p["head"] = formatOffset(g.Head)
	}
	return p
}

// dialerCursor is a dialer's position for one outbound address. It
// survives reconnects.
type dialerCursor struct {
	Stream string
	Offset uint64
	Held   bool
}

// request returns the resume request to send, nil when no position is
// held.
func (c *dialerCursor) request() *resumeReq {
	if !c.Held {
		return nil
	}
	return &resumeReq{Stream: c.Stream, After: c.Offset}
}

// linkUp applies the acceptor's ack (nil for a legacy link: a plain ack
// or no handshake). It returns the resume status for link.connected and
// the invalidation to publish before anything else, if any.
func (c *dialerCursor) linkUp(a *ackFrame, reconnect bool) (string, *gapInfo) {
	wanted := c.Held || reconnect
	if a == nil || a.Stream == nil {
		c.Held = false
		if wanted {
			return ResumeUnsupported, &gapInfo{Reason: ReasonUnsupported}
		}
		return ResumeUnsupported, nil
	}
	s := a.Stream
	asked, askedStream := c.Offset, c.Stream
	held := c.Held
	c.Stream, c.Held = s.ID, true
	switch {
	case a.Resume == nil:
		c.Offset = s.Head
		if wanted {
			return ResumeNone, &gapInfo{Reason: ReasonUnsupported}
		}
		return ResumeNone, nil
	case a.Resume.Status == ResumeReset:
		c.Offset = a.Resume.Head
		return ResumeReset, &gapInfo{Reason: a.Resume.Reason, Stream: s.ID, After: a.Resume.After, Head: a.Resume.Head}
	case held && askedStream != s.ID:
		// A replay of a stream we did not ask for: treat it as a reset.
		c.Offset = s.Head
		return ResumeReset, &gapInfo{Reason: ReasonStream, Stream: s.ID, After: asked, Head: s.Head}
	default:
		c.Offset = a.Resume.After
		return ResumeReplaying, nil
	}
}

// onEventOffset applies an event frame's raw offset member. It reports
// whether to deliver the event (false: a duplicate) or a protocol error.
func (c *dialerCursor) onEventOffset(raw json.RawMessage) (bool, error) {
	off, ok := parseOffsetToken(raw)
	if !ok {
		return false, protocolErr("event frame without a valid offset")
	}
	return c.advance(off), nil
}

func (c *dialerCursor) advance(off uint64) bool {
	if off <= c.Offset {
		return false
	}
	c.Offset = off
	return true
}

// onReset applies a reset frame: the position moves to the head.
func (c *dialerCursor) onReset(r resetInfo) (*gapInfo, error) {
	if r.Stream != c.Stream {
		return nil, protocolErr("reset for stream %q on a link to %q", r.Stream, c.Stream)
	}
	if r.Head < c.Offset {
		// Offsets only grow: a reset to below the position would make
		// the dialer re-deliver or drop events.
		return nil, protocolErr("reset head %d below position %d", r.Head, c.Offset)
	}
	c.Offset = r.Head
	return &gapInfo{Reason: r.Reason, Stream: r.Stream, After: r.After, Head: r.Head}, nil
}

// onPosition applies a position frame: max of the two offsets.
func (c *dialerCursor) onPosition(p positionInfo) error {
	if p.Stream != c.Stream {
		return protocolErr("position for stream %q on a link to %q", p.Stream, c.Stream)
	}
	if p.Offset > c.Offset {
		c.Offset = p.Offset
	}
	return nil
}

// inboundQueue is the reference model of a dialer that cannot apply
// backpressure (a browser): frames queue up to capacity, and receiving
// into a full queue overflows. The Go adapter delivers from its read
// loop, a queue of one with TCP backpressure, so it does not use this
// type; ports that queue must match it.
type inboundQueue struct {
	capacity   int
	q          []uint64
	delivered  uint64
	overflowed bool
}

func newInboundQueue(capacity int, after uint64) *inboundQueue {
	return &inboundQueue{capacity: capacity, delivered: after}
}

func (b *inboundQueue) recv(off uint64) {
	if b.overflowed {
		return
	}
	if len(b.q) >= b.capacity {
		b.overflowed, b.q = true, nil
		return
	}
	b.q = append(b.q, off)
}

func (b *inboundQueue) deliver(n int) []uint64 {
	if b.overflowed {
		return nil
	}
	n = min(n, len(b.q))
	out := b.q[:n:n]
	b.q = b.q[n:]
	if n > 0 {
		b.delivered = out[n-1]
	}
	return out
}

// resumeAfter is the position the reconnect resumes from.
func (b *inboundQueue) resumeAfter() uint64 { return b.delivered }
