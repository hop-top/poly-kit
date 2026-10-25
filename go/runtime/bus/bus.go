package bus

import (
	"context"
	"reflect"
	"sync"
	"sync/atomic"
)

// Handler is a synchronous event handler. Returning an error vetoes the
// publish — no further handlers (sync or async) run for that event.
type Handler func(ctx context.Context, e Event) error

// AsyncHandler is an asynchronous event handler. It runs in its own
// goroutine and never blocks the publisher.
type AsyncHandler func(ctx context.Context, e Event)

// Unsubscribe removes a subscription when called.
type Unsubscribe func()

// ErrFunc receives errors from non-critical operations (e.g. sink failures).
type ErrFunc func(error)

// Bus is the pub/sub interface.
type Bus interface {
	Publish(ctx context.Context, e Event) error
	Subscribe(pattern string, h Handler) Unsubscribe
	SubscribeAsync(pattern string, h AsyncHandler) Unsubscribe
	Close(ctx context.Context) error
}

type subscription struct {
	id      uint64
	pattern string
	sync    Handler
	async   AsyncHandler
}

const defaultMaxAsync = 256

type memBus struct {
	mu        sync.RWMutex
	subs      []subscription
	nextID    uint64
	closed    atomic.Bool
	wg        sync.WaitGroup
	sem       chan struct{} // bounded goroutine pool semaphore
	enforce   Mode
	onInvalid ErrFunc
	observers []observerEntry
}

// Option configures Bus construction.
type Option func(*busOpts)

type busOpts struct {
	adapter      Adapter
	networkAddrs []string
	networkOpts  []NetworkOption
	maxAsync     int
	enforce      Mode
	enforceSet   bool
	onInvalid    ErrFunc
}

// WithMaxAsync sets the maximum concurrent async handler goroutines.
// Zero or negative values fall back to defaultMaxAsync (256).
//
// Note: this option only applies when using the default in-memory bus
// (no adapter). NewMemoryAdapter always uses defaultMaxAsync.
func WithMaxAsync(n int) Option {
	return func(o *busOpts) { o.maxAsync = n }
}

// WithEnforce sets the topic-naming enforcement mode for Publish.
// The default is [ModeWarn] (when no enforcement option is supplied).
//
// Precedence: explicit WithEnforce > config getter > env > default(Warn).
func WithEnforce(m Mode) Option {
	return func(o *busOpts) {
		o.enforce = m
		o.enforceSet = true
	}
}

// WithInvalidTopicReporter installs a callback invoked when Publish
// encounters a topic that fails [Validate]. In [ModeWarn] the
// reporter receives the validation error (an *[InvalidTopicError])
// and the event is still delivered. In [ModeStrict] Publish returns
// the same error to the caller; the reporter is also invoked so
// observers can record the violation.
//
// If fn is nil the reporter is reset to a no-op. Defaults to no-op.
func WithInvalidTopicReporter(fn ErrFunc) Option {
	return func(o *busOpts) { o.onInvalid = fn }
}

// WithAdapter sets the transport adapter for the bus.
// When omitted, New uses an in-memory adapter.
// A typed-nil adapter (e.g. (*SQLiteAdapter)(nil)) is treated as unset.
func WithAdapter(a Adapter) Option {
	return func(o *busOpts) {
		if !isNilAdapter(a) {
			o.adapter = a
		}
	}
}

// isNilAdapter returns true if a is nil or a typed-nil interface value.
func isNilAdapter(a Adapter) bool {
	if a == nil {
		return true
	}
	v := reflect.ValueOf(a)
	return v.Kind() == reflect.Ptr && v.IsNil()
}

// New returns a new Bus. Without options it uses MemoryAdapter.
// If WithNetwork is provided, a NetworkAdapter is attached and
// auto-connects to the specified addresses.
func New(opts ...Option) Bus {
	var o busOpts
	for _, fn := range opts {
		fn(&o)
	}

	maxAsync := o.maxAsync
	if maxAsync <= 0 {
		maxAsync = defaultMaxAsync
	}

	enforce := ModeWarn
	if o.enforceSet {
		enforce = o.enforce
	}
	reporter := o.onInvalid
	if reporter == nil {
		reporter = func(error) {}
	}

	var b Bus
	if o.adapter != nil {
		b = &adapterBus{adapter: o.adapter, enforce: enforce, onInvalid: reporter}
	} else {
		b = &memBus{
			sem:       make(chan struct{}, maxAsync),
			enforce:   enforce,
			onInvalid: reporter,
		}
	}

	if len(o.networkAddrs) > 0 {
		na := NewNetworkAdapter(b, o.networkOpts...)
		for _, addr := range o.networkAddrs {
			// Best-effort connect; a failed first dial keeps retrying.
			na.dialConfigured(addr)
		}
		b = &networkedBus{Bus: b, network: na}
	}

	// Env-driven sink switch (KIT_BUS_SINK / KIT_BUS_SINK_PATH). Applied
	// last so the TeeBus wraps every other layer (memory/adapter,
	// network) — sink Drains see the same events Publish accepted. Zero
	// allocation in the common case: envSinksFromEnv returns nil when the
	// env var is unset, so the TeeBus wrap is skipped entirely.
	if sinks := envSinksFromEnv(); len(sinks) > 0 {
		b = NewTeeBus(b, sinks, reporter)
	}

	return b
}

// networkedBus wraps a Bus with a NetworkAdapter for cleanup.
type networkedBus struct {
	Bus
	network *NetworkAdapter
}

func (nb *networkedBus) Close(ctx context.Context) error {
	_ = nb.network.Close()
	return nb.Bus.Close(ctx)
}

// adapterBus delegates all operations to an Adapter.
type adapterBus struct {
	adapter   Adapter
	enforce   Mode
	onInvalid ErrFunc
}

func (a *adapterBus) Publish(ctx context.Context, e Event) error {
	if err := checkTopic(a.enforce, a.onInvalid, e.Topic); err != nil {
		return err
	}
	return a.adapter.Publish(ctx, e)
}

func (a *adapterBus) Subscribe(pattern string, h Handler) Unsubscribe {
	return a.adapter.Subscribe(pattern, h)
}

func (a *adapterBus) SubscribeAsync(pattern string, h AsyncHandler) Unsubscribe {
	return a.adapter.SubscribeAsync(pattern, h)
}

func (a *adapterBus) Close(ctx context.Context) error {
	return a.adapter.Close(ctx)
}

func (b *memBus) Subscribe(pattern string, h Handler) Unsubscribe {
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	b.subs = append(b.subs, subscription{id: id, pattern: pattern, sync: h})
	b.mu.Unlock()
	return b.unsub(id)
}

func (b *memBus) SubscribeAsync(pattern string, h AsyncHandler) Unsubscribe {
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	b.subs = append(b.subs, subscription{id: id, pattern: pattern, async: h})
	b.mu.Unlock()
	return b.unsub(id)
}

func (b *memBus) unsub(id uint64) Unsubscribe {
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		for i, s := range b.subs {
			if s.id == id {
				b.subs = append(b.subs[:i], b.subs[i+1:]...)
				return
			}
		}
	}
}

// ErrBusClosed is returned by Publish after Close has been called.
var ErrBusClosed = &busError{"bus: publish after close"}

type busError struct{ msg string }

func (e *busError) Error() string { return e.msg }

// Publish delivers the event to all matching subscribers. Sync handlers
// run in order; the first error vetoes and is returned. Async handlers
// run in goroutines after all sync handlers succeed.
//
// The read lock is released before acquiring the semaphore to avoid
// priority inversion: Close() needs the write lock, so blocking on a
// full semaphore while holding the read lock would deadlock under
// saturation. wg.Add is called after the semaphore slot is acquired
// (and a fresh closed check passes) so Close's wg.Wait never stalls
// on a goroutine that hasn't launched yet.
func (b *memBus) Publish(ctx context.Context, e Event) error {
	if err := checkTopic(b.enforce, b.onInvalid, e.Topic); err != nil {
		return err
	}

	b.mu.RLock()

	if b.closed.Load() {
		b.mu.RUnlock()
		return ErrBusClosed
	}

	matching := make([]subscription, 0, len(b.subs))
	for _, s := range b.subs {
		if e.Topic.Match(s.pattern) {
			matching = append(matching, s)
		}
	}

	// Sync handlers first (under RLock).
	for _, s := range matching {
		if s.sync != nil {
			if err := s.sync(ctx, e); err != nil {
				b.mu.RUnlock()
				return err
			}
		}
	}

	// Observers see every accepted event, in the publishing goroutine,
	// after all sync handlers succeeded and before async handlers start.
	for _, o := range b.observers {
		o.fn(ctx, e)
	}

	// Collect async handlers under RLock (snapshot matching subs).
	var asyncHandlers []AsyncHandler
	for _, s := range matching {
		if s.async != nil {
			asyncHandlers = append(asyncHandlers, s.async)
		}
	}

	b.mu.RUnlock()

	// Async handlers run on their own goroutine, so they are no longer
	// inside a network link's delivery: clear its marker, which would
	// otherwise refuse requests that cannot deadlock from here.
	if ctx.Value(deliveryKey{}) != nil {
		ctx = context.WithValue(ctx, deliveryKey{}, nil)
	}

	// Dispatch async handlers outside the lock. wg.Add is deferred
	// until after the semaphore slot is acquired so Close's wg.Wait
	// cannot stall on a goroutine that hasn't launched yet.
	for _, h := range asyncHandlers {
		h := h // capture
		b.sem <- struct{}{}
		if b.closed.Load() {
			<-b.sem
			return nil
		}
		b.wg.Add(1)
		go func() {
			defer func() { <-b.sem }()
			defer b.wg.Done()
			h(ctx, e)
		}()
	}

	return nil
}

// Close stops the bus from accepting new publishes and waits for in-flight
// async handlers to complete, respecting the context deadline.
//
// Close acquires the write lock before setting the closed flag. Any
// Publish blocked on the semaphore will see closed=true after acquiring
// its slot and bail out without launching the goroutine.
func (b *memBus) Close(ctx context.Context) error {
	b.mu.Lock()
	b.closed.Store(true)
	b.subs = nil
	b.mu.Unlock()

	done := make(chan struct{})
	go func() {
		b.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Observer is called once for every event a bus accepted: after all
// sync handlers returned nil, in the publishing goroutine, before async
// handlers start. It runs under the bus's read lock, so it must not
// block and must not publish.
type Observer func(ctx context.Context, e Event)

// PublishObserver is implemented by buses that can report accepted
// events in publish order. The network adapter uses it to keep an
// ordered log; a bus without it gets unordered forwarding.
type PublishObserver interface {
	ObservePublished(o Observer) Unsubscribe
}

type observerEntry struct {
	id uint64
	fn Observer
}

// ObservePublished registers o; see [Observer].
func (b *memBus) ObservePublished(o Observer) Unsubscribe {
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	b.observers = append(b.observers, observerEntry{id: id, fn: o})
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		for i, ob := range b.observers {
			if ob.id == id {
				b.observers = append(b.observers[:i], b.observers[i+1:]...)
				return
			}
		}
	}
}

// publishObserverOf finds the publish hook of b: b itself, or the bus
// inside one of kit's wrappers. It returns nil when there is none.
func publishObserverOf(b Bus) PublishObserver {
	for b != nil {
		if po, ok := b.(PublishObserver); ok {
			return po
		}
		switch w := b.(type) {
		case *networkedBus:
			b = w.Bus
		case *TeeBus:
			b = w.bus
		case *adapterBus:
			if ma, ok := w.adapter.(*MemoryAdapter); ok {
				return ma.bus
			}
			if po, ok := w.adapter.(PublishObserver); ok {
				return po
			}
			return nil
		default:
			return nil
		}
	}
	return nil
}
