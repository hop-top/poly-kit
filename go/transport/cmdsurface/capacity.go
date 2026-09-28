package cmdsurface

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
)

// ErrOverloaded is the refusal of the capacity gate: every in-flight
// slot of the service is taken and its queue is full. The error the
// bridge returns is an [*OverloadedError] wrapping it, which carries
// how long the caller should wait; read that with [RetryAfter].
// Transports answer it as overloaded: HTTP 503 with Retry-After,
// Connect Unavailable, an MCP isError result, the socket's OVERLOADED;
// a client turning it into an exit status uses TRANSIENT (6).
//
// It wraps no other class. None means "the service is busy":
// rate_limited is about one caller's pace, and exits 64.
var ErrOverloaded = errors.New("cmdsurface: overloaded")

// CodeOverloaded is the stable refusal code of [ErrOverloaded] on
// every surface.
const CodeOverloaded = "overloaded"

// Concurrency configures the capacity gate installed with
// [WithConcurrency].
type Concurrency struct {
	// MaxInflight is how many invocations run at once. Below 1 is
	// taken as 1.
	MaxInflight int
	// MaxQueue is how many more wait, first come first served, for a
	// slot to free. 0 is no queue: a call finding every slot taken is
	// refused at once. Below 0 is taken as 0.
	MaxQueue int
}

// DefaultConcurrency returns the kit defaults, per service: 32
// invocations in flight and 64 queued.
//
// 32 runs at once is ample for commands that wait on I/O or a child
// process, and bounds what a burst can hold of the host — memory for
// captured output, file descriptors, child processes. A queue twice
// that deep absorbs a burst the size of the default read-tier rate
// limit's (60) without refusing a caller that stays within its rate;
// a call that would wait behind more than that is better told to
// retry than held.
func DefaultConcurrency() Concurrency {
	return Concurrency{MaxInflight: 32, MaxQueue: 64}
}

// WithConcurrency installs the capacity gate: invocation-plane slot
// 11, in [Admission.Run] and [Admission.Stream], after every machine
// gate and after any confirmation a surface asks a person for. An
// invocation on a remote surface takes an in-flight slot, or waits
// for one in a first-come-first-served queue; with every slot taken
// and the queue full it is refused with an [*OverloadedError]. The
// CLI and library surfaces are never counted. A read answered from
// the result cache takes no slot, nor does a call waiting on an
// identical call's run.
//
// The per-command deadline ([AnnotationTimeout], else
// [WithCommandTimeout]) is armed before the call queues, so time
// spent waiting counts against it; a call that outwaits it is
// [ErrDeadlineExceeded] without having run. A caller that goes away
// while waiting gives its place up at once.
//
// How many run at once also depends on the Runner. The shared-tree
// [InProcessRunner] runs one invocation at a time, so over it the
// gate lets one run and queues the rest: waiting callers sit in the
// bounded, cancelable queue, counted, rather than on the runner's
// lock. With [WithRootFactory], or any other Runner, up to
// cfg.MaxInflight run in parallel. A shared-tree runner wrapped
// before it reaches [WithRunner] is not recognized, and its callers
// wait on its lock as before. [Bridge.Capacity] reports the bound in
// force.
//
// Slots are per bridge: each served service counts its own calls.
func WithConcurrency(cfg Concurrency) Option {
	return func(c *bridgeConfig) { c.concurrency = &cfg }
}

// QueueObserver is told when an invocation joins the capacity gate's
// queue (delta +1) and when it leaves it (delta -1), whether granted
// a slot, refused by its deadline, or abandoned by its caller. It is
// the seam an in-queue gauge counts through; it must not block.
type QueueObserver func(ctx context.Context, inv Invocation, delta int)

// WithQueueObserver adds fn to the observers of the capacity gate's
// queue. Repeated options append. Without [WithConcurrency] nothing
// queues and fn is never called.
func WithQueueObserver(fn QueueObserver) Option {
	return func(c *bridgeConfig) {
		if fn != nil {
			c.queueObservers = append(c.queueObservers, fn)
		}
	}
}

// CapacityLoad is the capacity gate's state at one moment.
type CapacityLoad struct {
	// Inflight is the number of invocations holding a slot.
	Inflight int
	// Queued is the number waiting for one.
	Queued int
	// MaxInflight is the bound in force: the configured one, or 1
	// over a runner that runs one invocation at a time.
	MaxInflight int
	// MaxQueue is the queue's bound.
	MaxQueue int
}

// Capacity reports the capacity gate's state; ok is false when the
// bridge has no gate.
func (b *Bridge) Capacity() (_ CapacityLoad, ok bool) {
	c := b.capacity
	if c == nil {
		return CapacityLoad{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return CapacityLoad{Inflight: c.inflight, Queued: len(c.queue), MaxInflight: c.maxInflight, MaxQueue: c.maxQueue}, true
}

// OverloadedError is the capacity gate's refusal. It wraps
// [ErrOverloaded].
type OverloadedError struct {
	// Path is the refused command's path key.
	Path string
	// Surface is the surface the call arrived on.
	Surface Surface
	// MaxInflight and MaxQueue are the bounds the call found full.
	MaxInflight, MaxQueue int
	// RetryAfter is how long the queue ahead of the call would take
	// to drain, estimated from recent run times.
	RetryAfter time.Duration
}

// Error implements error.
func (e *OverloadedError) Error() string {
	return fmt.Sprintf("%s: %s on %s: %d running and %d queued; retry after %s",
		ErrOverloaded, e.Path, e.Surface, e.MaxInflight, e.MaxQueue, e.RetryAfter.Round(time.Millisecond))
}

// Unwrap makes errors.Is(err, ErrOverloaded) hold.
func (e *OverloadedError) Unwrap() error { return ErrOverloaded }

// RetryAfterHint implements the hint [RetryAfter] reads.
func (e *OverloadedError) RetryAfterHint() time.Duration { return e.RetryAfter }

// The bounds of an overload's retry hint. Below a second, a saturated
// service is hammered by its own retries; beyond half a minute an
// estimate from average run times says little.
const (
	minOverloadRetry = time.Second
	maxOverloadRetry = 30 * time.Second
)

// parallelRunner is implemented by a runner that bounds how many
// invocations it runs at once; 0 means no bound.
type parallelRunner interface{ parallelism() int }

// capacity holds a service's in-flight slots and its queue.
type capacity struct {
	maxInflight int
	maxQueue    int
	now         func() time.Time

	mu       sync.Mutex
	inflight int
	// queue is first come first served. A non-empty queue means
	// every slot is taken: a freed slot passes to its head.
	queue []*capTicket
	// avgHold is a moving average of how long a run holds a slot,
	// zero until the first run ends.
	avgHold time.Duration
}

// newCapacity builds the gate for cfg over a runner running at most
// parallel invocations at once (0: no bound).
func newCapacity(cfg Concurrency, parallel int, now func() time.Time) *capacity {
	inflight := max(cfg.MaxInflight, 1)
	if parallel > 0 {
		inflight = min(inflight, parallel)
	}
	return &capacity{maxInflight: inflight, maxQueue: max(cfg.MaxQueue, 0), now: now}
}

// capTicket is one call's place at the gate: a slot, or a place in
// the queue until one passes to it. Its fields are guarded by the
// gate's mutex.
type capTicket struct {
	c *capacity
	// ready is closed when a queued ticket is granted a slot; nil for
	// a ticket granted one at once.
	ready   chan struct{}
	granted bool
	done    bool
	since   time.Time // when the slot was granted
}

// take returns a ticket holding a slot or a place in the queue, or
// nil and a retry hint when both are full.
func (c *capacity) take() (*capTicket, time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inflight < c.maxInflight && len(c.queue) == 0 {
		c.inflight++
		return &capTicket{c: c, granted: true, since: c.now()}, 0
	}
	if len(c.queue) < c.maxQueue {
		t := &capTicket{c: c, ready: make(chan struct{})}
		c.queue = append(c.queue, t)
		return t, 0
	}
	return nil, c.retryAfter()
}

// retryAfter estimates how long a full queue takes to drain: one
// average run per wave of maxInflight calls, the refused call's own
// wave included, within the retry bounds.
func (c *capacity) retryAfter() time.Duration {
	waves := time.Duration(c.maxQueue/c.maxInflight + 1)
	return min(max(c.avgHold*waves, minOverloadRetry), maxOverloadRetry)
}

// queued reports whether t started in the queue.
func (t *capTicket) queued() bool { return t.ready != nil }

// wait blocks until t holds a slot, or ctx ends, when it gives its
// place up and returns ctx's error.
func (t *capTicket) wait(ctx context.Context) error {
	if t.ready == nil {
		return nil
	}
	select {
	case <-t.ready:
		return nil
	case <-ctx.Done():
		t.drop(false)
		return ctx.Err()
	}
}

// release frees the slot t holds after a run, passing it to the head
// of the queue. It is idempotent.
func (t *capTicket) release() { t.drop(true) }

// drop gives t up: withdrawn from the queue while still waiting, else
// its slot freed. ran reports that the slot was used for a run, whose
// hold time then feeds the retry estimate.
func (t *capTicket) drop(ran bool) {
	c := t.c
	c.mu.Lock()
	defer c.mu.Unlock()
	if t.done {
		return
	}
	t.done = true
	if !t.granted {
		c.queue = slices.DeleteFunc(c.queue, func(x *capTicket) bool { return x == t })
		return
	}
	now := c.now()
	if ran {
		d := now.Sub(t.since)
		if c.avgHold == 0 {
			c.avgHold = d
		} else {
			c.avgHold += (d - c.avgHold) / 8
		}
	}
	if len(c.queue) == 0 {
		c.inflight--
		return
	}
	next := c.queue[0]
	c.queue[0] = nil
	c.queue = c.queue[1:]
	next.granted = true
	next.since = now
	close(next.ready)
}

// capacityGated reports whether the admitted call passes the capacity
// gate: the bridge has one, the surface is remote, and the call is not
// answered from the result cache.
func (a *Admission) capacityGated() bool {
	return a.b.capacity != nil && a.inv.Meta.Surface.remote() &&
		(a.cache == nil || a.cache.hit == nil)
}

// Reserve takes the admitted call's place at the capacity gate — a
// slot, or a place in the queue — without waiting for it, and returns
// the gate's refusal, an [*OverloadedError], when both are full. It
// is the part of slot 11 a streaming transport takes before it
// commits its response to a stream, so an overload is answered with
// the transport's status (503 and Retry-After) rather than as an
// error inside a stream already opened. The wait for a queued place
// happens in [Admission.Stream] or [Admission.Run], under the
// per-command deadline.
//
// A refusal is audited here, as the gates of [Bridge.Admit] audit
// theirs, and ends the Admission: Run and Stream return it again
// without running. An Admission that reserved must be run, or its
// place is held until it is. Reserve is a no-op without a gate, on a
// local surface, for a result cache hit, and when called again.
func (a *Admission) Reserve(ctx context.Context) error {
	if a.refused != nil || a.ticket != nil || !a.capacityGated() {
		return a.refused
	}
	if err := a.takePlace(ctx); err != nil {
		a.refused = a.b.refuse(a.b.auditContext(ctx, a.leaf), a.inv, err)
		return err
	}
	return nil
}

// takePlace takes a ticket, counting it into the queue when it waits.
func (a *Admission) takePlace(ctx context.Context) error {
	t, wait := a.b.capacity.take()
	if t == nil {
		return &OverloadedError{
			Path:        a.leaf.PathKey(),
			Surface:     a.inv.Meta.Surface,
			MaxInflight: a.b.capacity.maxInflight,
			MaxQueue:    a.b.capacity.maxQueue,
			RetryAfter:  wait,
		}
	}
	a.ticket = t
	if t.queued() {
		a.inQueue = true
		a.b.observeQueue(ctx, a.inv, 1)
	}
	return nil
}

// leaveQueue counts the call out of the queue, once.
func (a *Admission) leaveQueue(ctx context.Context) {
	if a.inQueue {
		a.inQueue = false
		a.b.observeQueue(context.WithoutCancel(ctx), a.inv, -1)
	}
}

// acquire holds a slot for the run under ctx, the context the
// per-command deadline bounds: it takes the call's place unless
// [Admission.Reserve] already did, and waits while queued. It returns
// the function freeing the slot once the run ends.
func (a *Admission) acquire(ctx context.Context) (release func(), err error) {
	if !a.capacityGated() {
		return func() {}, nil
	}
	if a.ticket == nil {
		if err := a.takePlace(ctx); err != nil {
			return nil, err
		}
	}
	err = a.ticket.wait(ctx)
	a.leaveQueue(ctx)
	if err != nil {
		return nil, err
	}
	return a.ticket.release, nil
}

// dropPlace gives up a place the call took and never ran on — a
// reserved call answered without running — so no slot or queue place
// outlives its Admission.
func (a *Admission) dropPlace(ctx context.Context) {
	if a.ticket != nil {
		a.ticket.drop(false)
		a.leaveQueue(ctx)
	}
}

// observeQueue tells every [QueueObserver] of a change of delta.
func (b *Bridge) observeQueue(ctx context.Context, inv Invocation, delta int) {
	for _, fn := range b.cfg.queueObservers {
		fn(ctx, inv, delta)
	}
}

// queueDeadlineError reports a call whose wait for a slot ended
// because ctx's deadline passed as [ErrDeadlineExceeded]: the call
// never ran. Any other error — a refusal, a caller gone — is returned
// as is.
func queueDeadlineError(ctx context.Context, err error, leaf *Leaf, bound time.Duration) error {
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return err
	}
	name := "the command"
	if leaf != nil {
		name = leaf.PathKey()
	}
	if bound > 0 {
		return fmt.Errorf("%w: %s waited past its %s deadline for a slot", ErrDeadlineExceeded, name, bound)
	}
	return fmt.Errorf("%w: %s waited past the caller's deadline for a slot", ErrDeadlineExceeded, name)
}
