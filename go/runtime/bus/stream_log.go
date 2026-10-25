package bus

import (
	"sync"

	"hop.top/kit/go/core/id"
)

// StreamIDPrefix is the TypeID prefix of stream ids minted by
// [NewRingLog] when no id is given.
const StreamIDPrefix = "stm"

// LogEntry is one record of an adapter's stream: an event with its wire
// origin, or a gap marker that tells downstream peers the adapter lost
// events from upstream. Offset is assigned by [StreamLog.Append].
type LogEntry struct {
	Offset uint64
	Event  Event
	Origin string
	Marker bool
}

// StreamLog stores the recent part of an adapter's stream. Offsets are
// dense: 1, 2, 3, ... with no holes, so a reader that gets a first entry
// above after+1 knows the entries in between were evicted.
//
// Implementations must be safe for concurrent use. Append runs inside
// the bus's publish path and must not block.
type StreamLog interface {
	// ID returns the stream id: an opaque ASCII string of 1-128 bytes.
	// A log that loses its contents must also change its id.
	ID() string
	// Append assigns the next offset to e and returns it.
	Append(e LogEntry) uint64
	// ReadAfter returns up to max entries with offset > after, ascending.
	ReadAfter(after uint64, max int) []LogEntry
	// Oldest returns the first retained offset, Head()+1 when empty.
	Oldest() uint64
	// Head returns the last assigned offset, 0 before the first Append.
	Head() uint64
}

// DefaultStreamLogSize is the ring size used when no log is configured.
const DefaultStreamLogSize = 1024

type ringLog struct {
	mu   sync.RWMutex
	id   string
	buf  []LogEntry
	head uint64
}

// NewRingLog returns an in-memory StreamLog holding the last size
// entries. An empty id mints a fresh "stm_" TypeID; size below 1 means
// [DefaultStreamLogSize].
func NewRingLog(streamID string, size int) StreamLog {
	if streamID == "" {
		streamID, _ = id.New(StreamIDPrefix)
	}
	if size < 1 {
		size = DefaultStreamLogSize
	}
	return &ringLog{id: streamID, buf: make([]LogEntry, size)}
}

func (r *ringLog) ID() string { return r.id }

func (r *ringLog) Append(e LogEntry) uint64 {
	r.mu.Lock()
	r.head++
	e.Offset = r.head
	r.buf[(r.head-1)%uint64(len(r.buf))] = e
	off := r.head
	r.mu.Unlock()
	return off
}

func (r *ringLog) oldestLocked() uint64 {
	n := uint64(len(r.buf))
	if r.head <= n {
		return 1
	}
	return r.head - n + 1
}

func (r *ringLog) Oldest() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.oldestLocked()
}

func (r *ringLog) Head() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.head
}

func (r *ringLog) ReadAfter(after uint64, max int) []LogEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	start := after + 1
	if o := r.oldestLocked(); start < o {
		start = o
	}
	if start > r.head || max <= 0 {
		return nil
	}
	n := r.head - start + 1
	if n > uint64(max) {
		n = uint64(max)
	}
	out := make([]LogEntry, 0, n)
	for off := start; off < start+n; off++ {
		out = append(out, r.buf[(off-1)%uint64(len(r.buf))])
	}
	return out
}

// sendCursor tracks how far one destination has read a log. It is the
// writer's cursor for a link and, kept across disconnects, the outbox.
type sendCursor struct {
	at     uint64
	outbox bool
}

// newSendCursor starts a cursor at the log's head: with the outbox on,
// everything appended from now on is kept for the destination.
func newSendCursor(l StreamLog, outbox bool) *sendCursor {
	return &sendCursor{at: l.Head(), outbox: outbox}
}

// linkUp prepares the cursor for a new link: without the outbox,
// entries appended while the destination was down are skipped.
func (c *sendCursor) linkUp(l StreamLog) {
	if !c.outbox {
		c.at = l.Head()
	}
}

// next returns up to max entries after the cursor and advances it past
// them. gap counts entries that were evicted before the cursor reached
// them.
func (c *sendCursor) next(l StreamLog, max int) (entries []LogEntry, gap uint64) {
	entries = l.ReadAfter(c.at, max)
	if len(entries) == 0 {
		return nil, 0
	}
	if first := entries[0].Offset; first > c.at+1 {
		gap = first - c.at - 1
	}
	c.at = entries[len(entries)-1].Offset
	return entries, gap
}
