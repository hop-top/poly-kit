package consent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"hop.top/kit/go/runtime/bus"
)

// Publisher is the part of bus.Bus the relay needs. Publish returning
// nil is the ack: on an in-process bus every sync subscriber returned
// nil. Async subscribers and network peers are not covered by it.
type Publisher interface {
	Publish(ctx context.Context, e bus.Event) error
}

// Relay delivers the audit log onto the bus with confirmation: an
// outbox over FileAuditor. Records after the ack cursor are published
// in sequence order as bus events whose id is the record's idempotency
// key; the cursor moves past a record only when Publish returned nil,
// and is persisted atomically next to the log. Delivery is
// at-least-once: a record published but not acked (subscriber error,
// crash, cursor write failure) is published again, with the same key,
// on the next Flush, including after a restart.
//
// A failing record blocks the ones after it until it is acked, so a
// subscriber that always errors stalls delivery; Pending reports it.
type Relay struct {
	log    *FileAuditor
	pub    Publisher
	topics Topics
	cursor string
	retry  time.Duration

	mu     sync.Mutex
	notify chan struct{}

	// writeCursor is swapped by tests to simulate a crash between
	// publish and ack.
	writeCursor func(path string, c relayCursor) error
}

// RelayOption configures a Relay.
type RelayOption func(*Relay)

// WithRelayTopics overrides the topics; empty fields keep the default.
func WithRelayTopics(t Topics) RelayOption {
	return func(r *Relay) {
		def := DefaultTopics
		for _, p := range []struct{ dst, src *bus.Topic }{
			{&def.Granted, &t.Granted}, {&def.Denied, &t.Denied}, {&def.Revoked, &t.Revoked},
			{&def.Used, &t.Used}, {&def.Refused, &t.Refused},
		} {
			if *p.src != "" {
				*p.dst = *p.src
			}
		}
		r.topics = def
	}
}

// WithRetryInterval sets how long Run waits before retrying after a
// failed flush. Default 5s.
func WithRetryInterval(d time.Duration) RelayOption {
	return func(r *Relay) {
		if d > 0 {
			r.retry = d
		}
	}
}

// NewRelay relays log onto pub. The cursor lives next to the log
// (audit.cursor).
func NewRelay(log *FileAuditor, pub Publisher, opts ...RelayOption) *Relay {
	if log == nil || pub == nil {
		panic("consent: NewRelay needs a FileAuditor and a Publisher")
	}
	r := &Relay{
		log: log, pub: pub, topics: DefaultTopics,
		cursor:      filepath.Join(filepath.Dir(log.Path()), cursorFileName),
		retry:       5 * time.Second,
		notify:      make(chan struct{}, 1),
		writeCursor: writeCursorFile,
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// CursorPath returns the ack cursor path.
func (r *Relay) CursorPath() string { return r.cursor }

// FlushResult reports one Flush.
type FlushResult struct {
	// Delivered lists the sequence numbers acked by this flush.
	Delivered []uint64
	// Cursor is the last acked sequence number.
	Cursor uint64
	// Pending counts records still waiting for an ack.
	Pending int
}

// Flush publishes every record after the cursor, in order, and stops
// at the first failure, which it returns.
func (r *Relay) Flush(ctx context.Context) (FlushResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	logID, err := r.log.LogID()
	if err != nil {
		return FlushResult{}, err
	}
	cur, err := readCursorFile(r.cursor)
	if err != nil {
		pending, _ := r.log.ReadAfter(0)
		return FlushResult{Pending: len(pending)}, err
	}
	if cur.Log != logID {
		cur = relayCursor{Log: logID}
	}
	recs, err := r.log.ReadAfter(cur.Seq)
	if err != nil {
		return FlushResult{Cursor: cur.Seq}, err
	}
	res := FlushResult{Delivered: []uint64{}, Cursor: cur.Seq, Pending: len(recs)}
	for _, e := range recs {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if err := r.pub.Publish(ctx, r.event(e)); err != nil {
			return res, fmt.Errorf("consent: deliver %s: %w", e.Key(), err)
		}
		next := relayCursor{Log: logID, Seq: e.Seq}
		if err := r.writeCursor(r.cursor, next); err != nil {
			return res, fmt.Errorf("consent: ack %s: %w", e.Key(), err)
		}
		res.Delivered = append(res.Delivered, e.Seq)
		res.Cursor, res.Pending = e.Seq, res.Pending-1
	}
	return res, nil
}

// Notify asks Run to flush soon. It never blocks.
func (r *Relay) Notify() {
	select {
	case r.notify <- struct{}{}:
	default:
	}
}

// Run flushes at start, on every Notify, and after a failure every
// retry interval, until ctx ends.
func (r *Relay) Run(ctx context.Context) error {
	for {
		_, err := r.Flush(ctx)
		var wait <-chan time.Time
		if err != nil {
			wait = time.After(r.retry)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.notify:
		case <-wait:
		}
	}
}

// publishUnrecorded sends an event no auditor recorded (the auditor
// itself failed). Best effort: no key, no redelivery, errors ignored.
func (r *Relay) publishUnrecorded(ctx context.Context, e AuditEvent) {
	_ = r.pub.Publish(ctx, r.event(e))
}

func (r *Relay) event(e AuditEvent) bus.Event {
	ev := bus.NewEvent(r.topics.topic(e.Event), eventSource, e)
	if k := e.Key(); k != "" {
		ev.ID = k
	}
	ev.Subject = string(e.Capability)
	return ev
}

// relayCursor is the persisted ack position.
type relayCursor struct {
	Log string `json:"log"`
	Seq uint64 `json:"seq"`
}

func readCursorFile(path string) (relayCursor, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return relayCursor{}, nil
	}
	if err != nil {
		return relayCursor{}, fmt.Errorf("consent: read cursor: %w", err)
	}
	var c relayCursor
	if err := json.Unmarshal(b, &c); err != nil {
		return relayCursor{}, fmt.Errorf("consent: cursor %s is corrupt: %w", path, err)
	}
	return c, nil
}

func writeCursorFile(path string, c relayCursor) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(b, '\n'))
}
