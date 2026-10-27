package bus

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"sync"
)

// AcksVersion is the acknowledged-publish protocol version this adapter
// speaks (caps.acks).
const AcksVersion = 1

// Puback statuses on the wire.
const (
	pubackOK  = "ok"
	pubackErr = "err"
)

// Ack codes. A [PeerAck] with an empty Code was acknowledged. Codes are
// contract; messages are not.
const (
	// AckRejected: a sync handler on the peer returned an error.
	AckRejected = "rejected"
	// AckInvalid: the peer could not use the frame (undecodable event,
	// reserved topic).
	AckInvalid = "invalid"
	// AckTimeout: no acknowledgement before the deadline. Delivery is
	// unknown, not failed: the event may still arrive, so retry with the
	// same event id.
	AckTimeout = "timeout"
	// AckEvicted: the send log evicted the event before it was sent.
	AckEvicted = "evicted"
	// AckUnavailable: the address was disconnected, unknown, or the
	// adapter closed.
	AckUnavailable = "unavailable"
	// AckUnknown: the peer received this frame before but no longer
	// knows its result. Delivery is unknown, as with a timeout; retry with
	// the same event id.
	AckUnknown = "unknown"
	// AckFiltered: the event was not sent to the peer (topic filter,
	// link filter, or the peer is its origin).
	AckFiltered = "filtered"
	// AckUnsupported: the peer did not negotiate acks.
	AckUnsupported = "unsupported"
)

// Protocol error kinds for puback frames.
const (
	pkPubackMalformed = "puback_malformed" // offset, status or code missing or malformed
	pkPubackAhead     = "puback_ahead"     // offset beyond the last frame sent on the link
	pkPubackID        = "puback_id"        // id differs from the event sent at that offset
)

// negotiateAcks reports whether a link carries pubacks: a stream link
// on which this side speaks acks (mine) and the peer advertised them.
func negotiateAcks(mine int, peer *streamCaps, streamLink bool) bool {
	return streamLink && mine >= AcksVersion && peer != nil && peer.Acks >= AcksVersion
}

// pubackInfo is a puback frame: the peer handled every event frame up to
// Offset on this link; Status and Code report the frame at Offset. Every
// earlier frame not reported by its own puback is acknowledged ok.
type pubackInfo struct {
	Offset  uint64
	ID      string
	Status  string
	Code    string
	Message string
}

func encodePuback(p pubackInfo) []byte {
	f := map[string]string{"type": "puback", "offset": formatOffset(p.Offset), "status": p.Status}
	if p.ID != "" {
		f["id"] = p.ID
	}
	if p.Status == pubackErr {
		f["code"] = p.Code
		if p.Message != "" {
			f["message"] = p.Message
		}
	}
	b, _ := json.Marshal(f)
	return b
}

// parsePuback decodes a puback control frame. It breaks the link when
// malformed: a lost acknowledgement cannot be told from a lost frame.
func parsePuback(m map[string]json.RawMessage) (pubackInfo, error) {
	off, okO := parseOffsetToken(m["offset"])
	status, okS := memberString(m, "status")
	if !okO || !okS || (status != pubackOK && status != pubackErr) {
		return pubackInfo{}, protocolErr(pkPubackMalformed, "malformed puback")
	}
	p := pubackInfo{Offset: off, Status: status}
	if raw, ok := m["id"]; ok && !isNull(raw) {
		id, okID := memberString(m, "id")
		if !okID {
			return pubackInfo{}, protocolErr(pkPubackMalformed, "puback id must be a string")
		}
		p.ID = id
	}
	if status == pubackErr {
		code, okC := memberString(m, "code")
		if !okC || code == "" {
			return pubackInfo{}, protocolErr(pkPubackMalformed, "puback err without a code")
		}
		p.Code = code
		p.Message, _ = memberString(m, "message")
	}
	return p, nil
}

// ackResolution is one outcome for a waiting publish: Code empty means
// acknowledged.
type ackResolution struct {
	Offset  uint64
	T       *ackTicket
	Code    string
	Message string
}

// ackTracker is a dialer's acknowledgement state for one outbound
// address. It survives reconnects. Pure: no clock, no I/O; the caller
// holds its lock and applies the resolutions.
type ackTracker struct {
	// acked is cumulative: the peer handled every frame up to it. A new
	// acks link rewinds the send cursor here, so frames lost with a link
	// are sent again (the peer dedupes them).
	acked uint64
	// sent is the highest offset written on the current link.
	sent uint64
	// waits are the acked publishes for this address, by offset.
	waits map[uint64]*ackTicket
}

func newAckTracker(start uint64) *ackTracker {
	return &ackTracker{acked: start, sent: start, waits: map[uint64]*ackTicket{}}
}

// register records an acked publish logged at off.
func (a *ackTracker) register(off uint64, t *ackTicket) {
	if _, ok := a.waits[off]; !ok {
		a.waits[off] = t
	}
}

// linkUp applies a new link. On an acks link the cursor rewinds to the
// last acknowledged offset; on any other link every wait fails with
// unsupported and the cumulative position follows the cursor, since
// nothing will ever acknowledge what this link sends.
func (a *ackTracker) linkUp(acks bool, c *sendCursor) []ackResolution {
	if acks {
		if a.acked < c.at {
			c.at = a.acked
		}
		a.sent = c.at
		return nil
	}
	a.acked, a.sent = c.at, c.at
	return a.drain(func(uint64) bool { return true }, AckUnsupported)
}

// onSend records that the frame at off is being written. On a link
// without acks its wait fails with unsupported.
func (a *ackTracker) onSend(off uint64, t *ackTicket, acks bool) *ackResolution {
	if !acks {
		a.acked, a.sent = off, off
		if w := a.take(off, t); w != nil {
			return &ackResolution{Offset: off, T: w, Code: AckUnsupported}
		}
		return nil
	}
	if off > a.sent {
		a.sent = off
	}
	if t != nil {
		a.register(off, t)
	}
	return nil
}

// onSkip resolves a wait for an entry the writer did not send.
func (a *ackTracker) onSkip(off uint64, t *ackTicket) *ackResolution {
	if w := a.take(off, t); w != nil {
		return &ackResolution{Offset: off, T: w, Code: AckFiltered}
	}
	return nil
}

// onGap resolves waits for entries evicted before they were sent:
// offsets after before and below first.
func (a *ackTracker) onGap(before, first uint64) []ackResolution {
	return a.drain(func(off uint64) bool { return off > before && off < first }, AckEvicted)
}

// onPuback applies a cumulative acknowledgement.
func (a *ackTracker) onPuback(p pubackInfo) ([]ackResolution, error) {
	if p.Offset > a.sent {
		return nil, protocolErr(pkPubackAhead, "puback for %d beyond last sent %d", p.Offset, a.sent)
	}
	if w := a.waits[p.Offset]; w != nil && p.ID != "" && w.id != "" && p.ID != w.id {
		return nil, protocolErr(pkPubackID, "puback id %q for %d, sent %q", p.ID, p.Offset, w.id)
	}
	if p.Offset <= a.acked {
		return nil, nil
	}
	a.acked = p.Offset
	var out []ackResolution
	for _, off := range a.offsets(func(off uint64) bool { return off <= p.Offset }) {
		r := ackResolution{Offset: off, T: a.waits[off]}
		if off == p.Offset && p.Status == pubackErr {
			r.Code, r.Message = p.Code, p.Message
		}
		delete(a.waits, off)
		out = append(out, r)
	}
	return out, nil
}

// failAll resolves every wait with code.
func (a *ackTracker) failAll(code string) []ackResolution {
	return a.drain(func(uint64) bool { return true }, code)
}

// prune drops waits whose ticket no longer waits for addr.
func (a *ackTracker) prune(addr string) {
	for off, t := range a.waits {
		if t.resolvedFor(addr) {
			delete(a.waits, off)
		}
	}
}

func (a *ackTracker) take(off uint64, t *ackTicket) *ackTicket {
	w := a.waits[off]
	if w == nil {
		w = t
	}
	delete(a.waits, off)
	return w
}

func (a *ackTracker) drain(keep func(uint64) bool, code string) []ackResolution {
	var out []ackResolution
	for _, off := range a.offsets(keep) {
		out = append(out, ackResolution{Offset: off, T: a.waits[off], Code: code})
		delete(a.waits, off)
	}
	return out
}

func (a *ackTracker) offsets(keep func(uint64) bool) []uint64 {
	var offs []uint64
	for off := range a.waits {
		if keep(off) {
			offs = append(offs, off)
		}
	}
	sort.Slice(offs, func(i, j int) bool { return offs[i] < offs[j] })
	return offs
}

// ackResult is an acceptor's outcome for one inbound frame: shared by
// every link that receives the frame (a redelivery while the first copy
// is still being handled waits for the same result).
type ackResult struct {
	done chan struct{}
	once sync.Once
	p    pubackInfo // Offset and ID are those of the first copy
}

func newAckResult(off uint64, id string) *ackResult {
	return &ackResult{done: make(chan struct{}), p: pubackInfo{Offset: off, ID: id, Status: pubackOK}}
}

func completedAck(p pubackInfo) *ackResult {
	r := &ackResult{done: make(chan struct{}), p: p}
	r.once.Do(func() { close(r.done) })
	return r
}

// complete settles the result once; code empty means ok.
func (r *ackResult) complete(code, msg string, after func(pubackInfo)) {
	r.once.Do(func() {
		if code != "" {
			r.p.Status, r.p.Code, r.p.Message = pubackErr, code, msg
		}
		if after != nil {
			after(r.p)
		}
		close(r.done)
	})
}

func (r *ackResult) settled() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}

// Acceptor dedupe bounds: dialer streams remembered in all, per
// principal (see [PrincipalVerifier]), and failed results kept per
// stream. Dialers known only by their token share the overall bound.
const (
	ackSeenStreams             = 1024
	ackSeenStreamsPerPrincipal = 64
	ackSeenErrors              = 1024
)

// seenPeer is whose dedupe memory a link uses. key is the identity the
// stream bound counts under: a digest of the verified principal, or of
// the token when the authenticator names none; max is how many streams
// it may keep (0: the overall bound). cred is a digest of the token: a
// dialer stream id is the dialer's own claim, so it only names a stream
// together with the credential that presented it, and two credentials
// can never share one. Raw tokens never become keys.
type seenPeer struct {
	key  string
	cred string
	max  int
}

func dedupePeer(principal, token string) seenPeer {
	cred := digest("token", token)
	if principal != "" {
		return seenPeer{key: digest("principal", principal), cred: cred, max: ackSeenStreamsPerPrincipal}
	}
	return seenPeer{key: cred, cred: cred}
}

func digest(kind, v string) string {
	h := sha256.Sum256([]byte(kind + "\x00" + v))
	return kind + ":" + hex.EncodeToString(h[:16])
}

// ackSeen is an acceptor's memory of the dialer streams it acknowledged,
// so a frame redelivered after a reconnect is acknowledged again without
// being published twice.
//
// Bounded per peer first: a peer that opens many streams only forgets
// its own least recently used one; the overall bound is a backstop. A
// peer is a verified principal, else a token: dialers sharing a token
// cannot be told apart, so they share a bound as large as the overall
// one, and one of them can evict another's streams (its frames are then
// published again, never answered with a false ok). A
// forgotten stream's frames are treated as new (published again: at
// least once). Within a stream, failed results are kept up to a bound;
// past it the oldest is pruned and the stream's floor rises, and a copy
// at or below the floor is answered err/unknown, never ok: an ok that
// is not known to be one must not confirm delivery.
type ackSeen struct {
	mu      sync.Mutex
	streams map[seenKey]*seenStream
	perPeer map[string]int
	clock   uint64

	maxStreams, maxErrors int
}

type seenKey struct{ peer, cred, stream string }

type seenStream struct {
	last     uint64
	floor    uint64 // results at or below it may have been pruned
	used     uint64
	inflight map[uint64]*ackResult
	errs     map[uint64]pubackInfo
}

func newAckSeen() *ackSeen {
	return &ackSeen{
		streams: map[seenKey]*seenStream{}, perPeer: map[string]int{},
		maxStreams: ackSeenStreams, maxErrors: ackSeenErrors,
	}
}

// begin returns the result for frame off of peer's stream with event id,
// and whether it is a redelivery (dup: do not publish it again).
func (s *ackSeen) begin(sp seenPeer, stream string, off uint64, id string) (*ackResult, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clock++
	peer, max := sp.key, sp.max
	if max <= 0 {
		max = s.maxStreams
	}
	k := seenKey{peer, sp.cred, stream}
	st := s.streams[k]
	if st == nil {
		switch {
		case s.perPeer[peer] >= max:
			s.evictLocked(func(o seenKey) bool { return o.peer == peer })
		case len(s.streams) >= s.maxStreams:
			s.evictLocked(func(seenKey) bool { return true })
		}
		st = &seenStream{inflight: map[uint64]*ackResult{}, errs: map[uint64]pubackInfo{}}
		s.streams[k] = st
		s.perPeer[peer]++
	}
	st.used = s.clock
	if off <= st.last {
		if r := st.inflight[off]; r != nil {
			return r, true
		}
		if p, ok := st.errs[off]; ok {
			p.ID = id
			return completedAck(p), true
		}
		if off <= st.floor {
			return completedAck(pubackInfo{Offset: off, ID: id, Status: pubackErr, Code: AckUnknown,
				Message: "result no longer known"}), true
		}
		return completedAck(pubackInfo{Offset: off, ID: id, Status: pubackOK}), true
	}
	st.last = off
	r := newAckResult(off, id)
	st.inflight[off] = r
	return r, false
}

// settle records a completed result of sp's stream.
func (s *ackSeen) settle(sp seenPeer, stream string, p pubackInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.streams[seenKey{sp.key, sp.cred, stream}]
	if st == nil {
		return
	}
	delete(st.inflight, p.Offset)
	if p.Status != pubackErr {
		return
	}
	st.errs[p.Offset] = p
	for len(st.errs) > s.maxErrors {
		lowest := p.Offset
		for off := range st.errs {
			lowest = min(lowest, off)
		}
		delete(st.errs, lowest)
		st.floor = max(st.floor, lowest)
	}
}

// evictLocked forgets the least recently used stream among those match
// selects.
func (s *ackSeen) evictLocked(match func(seenKey) bool) {
	var (
		victim seenKey
		found  bool
		oldest uint64
	)
	for k, st := range s.streams {
		if match(k) && (!found || st.used < oldest) {
			victim, oldest, found = k, st.used, true
		}
	}
	if !found {
		return
	}
	delete(s.streams, victim)
	if s.perPeer[victim.peer]--; s.perPeer[victim.peer] <= 0 {
		delete(s.perPeer, victim.peer)
	}
}

// DefaultMaxPendingAcks bounds unresolved acked publishes per outbound
// address, and unanswered frames per inbound acks link.
const DefaultMaxPendingAcks = 1024

// ackQueue is an acceptor link's pubacks in frame order. Results may
// settle in any order (async handlers); pubacks go out in offset order,
// so an acknowledgement stays cumulative.
type ackQueue struct {
	mu     sync.Mutex
	items  []*ackResult
	offs   []uint64
	ids    []string
	max    int
	pushed chan struct{} // closed by the next push
	space  chan struct{} // closed by the next pop
}

func newAckQueue(max int) *ackQueue {
	if max < 1 {
		max = DefaultMaxPendingAcks
	}
	return &ackQueue{max: max, pushed: make(chan struct{}), space: make(chan struct{})}
}

// push appends a frame's result. It reports false with the channel to
// wait on when the queue is full.
func (q *ackQueue) push(off uint64, id string, r *ackResult) (bool, <-chan struct{}) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) >= q.max {
		return false, q.space
	}
	q.items = append(q.items, r)
	q.offs = append(q.offs, off)
	q.ids = append(q.ids, id)
	close(q.pushed)
	q.pushed = make(chan struct{})
	return true, nil
}

// ready pops the settled prefix, as pubacks to send in order.
func (q *ackQueue) ready() []pubackInfo {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for n < len(q.items) && q.items[n].settled() {
		n++
	}
	if n == 0 {
		return nil
	}
	out := make([]pubackInfo, n)
	for i := 0; i < n; i++ {
		p := q.items[i].p
		p.Offset, p.ID = q.offs[i], q.ids[i]
		out[i] = p
	}
	q.items, q.offs, q.ids = q.items[n:], q.offs[n:], q.ids[n:]
	close(q.space)
	q.space = make(chan struct{})
	return out
}

// wait returns a channel that fires when ready may return more: the
// head's settlement, or the next push when empty.
func (q *ackQueue) wait() <-chan struct{} {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return q.pushed
	}
	return q.items[0].done
}
