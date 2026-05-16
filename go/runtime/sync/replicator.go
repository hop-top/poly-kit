package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	gosync "sync"
	"time"

	"charm.land/log/v2"
	"github.com/spf13/viper"

	kitlog "hop.top/kit/go/console/log"
	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/runtime/domain"
)

// DefaultSubscriptionPrefix is the kit baseline subscription prefix used
// when no [WithSubscriptionPrefix] option is supplied. It matches the
// default topic prefix emitted by [domain.Service] (see
// [domain.DefaultTopics]) so a vanilla wiring "just works" without
// adopters touching topic configuration.
const DefaultSubscriptionPrefix = "kit.runtime.entity"

// Replicator orchestrates multi-remote entity replication.
type Replicator[T domain.Entity] struct {
	repo      domain.Repository[T]
	bus       bus.Bus
	remotes   *RemoteSet
	clock     *Clock
	merge     MergeFunc[T]
	interval  time.Duration
	subPrefix string // 3-segment topic prefix to subscribe under
	logger    *log.Logger
	done      chan struct{}
	wg        gosync.WaitGroup // tracks syncLoop goroutines

	mu       gosync.RWMutex
	queue    []Diff
	cursors  map[string]int // per-remote push cursor
	statuses map[string]*remoteState
	pulls    map[string]Timestamp // per-remote last-pull watermark
	unsub    bus.Unsubscribe
	ctx      context.Context // lifecycle context from Start
}

type remoteState struct {
	connected bool
	lastSync  time.Time
	lastErr   error
}

// ReplicatorOption configures a Replicator.
type ReplicatorOption[T domain.Entity] func(*Replicator[T])

// WithRemote adds a remote to the replicator.
func WithRemote[T domain.Entity](r Remote) ReplicatorOption[T] {
	return func(rep *Replicator[T]) { _ = rep.remotes.Add(r) }
}

// WithInterval sets the sync loop interval.
func WithInterval[T domain.Entity](d time.Duration) ReplicatorOption[T] {
	return func(rep *Replicator[T]) { rep.interval = d }
}

// WithMergeFunc sets the conflict resolution function.
func WithMergeFunc[T domain.Entity](fn MergeFunc[T]) ReplicatorOption[T] {
	return func(rep *Replicator[T]) { rep.merge = fn }
}

// WithBus sets the event bus for subscribing to entity changes.
func WithBus[T domain.Entity](b bus.Bus) ReplicatorOption[T] {
	return func(rep *Replicator[T]) { rep.bus = b }
}

// WithClock sets the hybrid logical clock.
func WithClock[T domain.Entity](c *Clock) ReplicatorOption[T] {
	return func(rep *Replicator[T]) { rep.clock = c }
}

// WithLogger sets the logger used for apply-path error reporting. When
// unset, the replicator constructs a logger via [kitlog.New] keyed off
// the global viper instance, so adopters' --quiet/--no-color settings
// flow through automatically.
func WithLogger[T domain.Entity](l *log.Logger) ReplicatorOption[T] {
	return func(rep *Replicator[T]) { rep.logger = l }
}

// WithSubscriptionPrefix configures the 3-segment topic prefix the
// replicator subscribes under. The replicator subscribes to
// "<prefix>.*" and dispatches via suffix match on the action segment
// (created/updated/deleted), so the action vocabulary is stable
// across any prefix.
//
// Defaults to [DefaultSubscriptionPrefix] ("kit.runtime.entity"),
// which matches [domain.DefaultTopics]. Adopters using
// [domain.WithTopicPrefix] on Service[T] should pass the same
// prefix here so the replicator captures their entity events.
//
// Example:
//
//	svc := domain.NewService(repo,
//	    domain.WithTopicPrefix[Workspace]("wsm.runtime.workspace"),
//	)
//	rep := sync.NewReplicator[Workspace](repo,
//	    sync.WithBus[Workspace](b),
//	    sync.WithSubscriptionPrefix[Workspace]("wsm.runtime.workspace"),
//	)
//
// Panics if prefix is not exactly 3 dot-separated segments — a
// programmer error caught at boot rather than as silent missed
// events at runtime.
func WithSubscriptionPrefix[T domain.Entity](prefix string) ReplicatorOption[T] {
	if err := validateSubPrefix(prefix); err != nil {
		panic(fmt.Sprintf("sync.WithSubscriptionPrefix(%q): %v", prefix, err))
	}
	return func(rep *Replicator[T]) { rep.subPrefix = prefix }
}

// validateSubPrefix mirrors the 3-segment shape check from
// bus.PrefixTopics without requiring a synthetic action to compose
// a full topic for validation.
func validateSubPrefix(prefix string) error {
	if prefix == "" {
		return errors.New("prefix is empty")
	}
	if strings.HasSuffix(prefix, ".") {
		return fmt.Errorf("prefix %q must not end with '.'", prefix)
	}
	parts := strings.Split(prefix, ".")
	if len(parts) != 3 {
		return fmt.Errorf("prefix %q has %d segments; expected 3 (source.category.object)", prefix, len(parts))
	}
	for i, seg := range parts {
		if seg == "" {
			return fmt.Errorf("prefix %q has empty segment at position %d", prefix, i)
		}
	}
	return nil
}

// NewReplicator creates a Replicator for the given repository.
func NewReplicator[T domain.Entity](repo domain.Repository[T], opts ...ReplicatorOption[T]) *Replicator[T] {
	r := &Replicator[T]{
		repo:      repo,
		remotes:   NewRemoteSet(),
		clock:     NewClock("local"),
		interval:  5 * time.Second, // default; override with WithInterval
		subPrefix: DefaultSubscriptionPrefix,
		done:      make(chan struct{}),
		cursors:   make(map[string]int),
		statuses:  make(map[string]*remoteState),
		pulls:     make(map[string]Timestamp),
	}
	for _, o := range opts {
		o(r)
	}
	if r.logger == nil {
		r.logger = kitlog.New(viper.GetViper())
	}
	return r
}

// Start begins sync loops for all registered remotes and subscribes
// to bus events for local change capture.
func (r *Replicator[T]) Start(ctx context.Context) error {
	r.ctx = ctx

	if r.bus != nil {
		pattern := r.subPrefix + ".*"
		r.unsub = r.bus.Subscribe(pattern, func(_ context.Context, e bus.Event) error {
			r.handleEvent(e)
			return nil
		})
	}

	for _, rem := range r.remotes.List() {
		r.mu.Lock()
		r.statuses[rem.Name] = &remoteState{}
		r.mu.Unlock()
		r.wg.Add(1)
		go r.syncLoop(ctx, rem)
	}
	return nil
}

// Stop signals all sync loops to exit and blocks until they have returned.
// After Stop returns, no more writes to the underlying repository will occur
// from this Replicator's goroutines.
func (r *Replicator[T]) Stop() error {
	close(r.done)
	r.wg.Wait()
	if r.unsub != nil {
		r.unsub()
	}
	return nil
}

// AddRemote registers a new remote and starts its sync loop.
// Start must be called before AddRemote.
func (r *Replicator[T]) AddRemote(rem Remote) error {
	if r.ctx == nil {
		return errors.New("sync: replicator not started")
	}
	if err := r.remotes.Add(rem); err != nil {
		return err
	}
	r.mu.Lock()
	r.statuses[rem.Name] = &remoteState{}
	r.mu.Unlock()
	r.wg.Add(1)
	go r.syncLoop(r.ctx, rem)
	return nil
}

// RemoveRemote unregisters a remote by name.
func (r *Replicator[T]) RemoveRemote(name string) error {
	return r.remotes.Remove(name)
}

const maxQueueSize = 10000

// Enqueue adds a diff to the outbound queue (for testing or manual push).
// If the queue is full, the oldest entry is dropped.
func (r *Replicator[T]) Enqueue(d Diff) {
	r.mu.Lock()
	if len(r.queue) >= maxQueueSize {
		r.queue = r.queue[1:]
	}
	r.queue = append(r.queue, d)
	r.mu.Unlock()
}

func (r *Replicator[T]) syncLoop(ctx context.Context, rem Remote) {
	defer r.wg.Done()
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-r.done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.syncOnce(ctx, rem)
		}
	}
}

func (r *Replicator[T]) syncOnce(ctx context.Context, rem Remote) {
	// Ping
	if err := rem.Transport.Ping(ctx); err != nil {
		r.setStatus(rem.Name, false, err)
		r.publishEvent(ctx, "sync.remote.error", rem.Name)
		return
	}
	r.setStatus(rem.Name, true, nil)
	r.publishEvent(ctx, "sync.remote.connected", rem.Name)

	if rem.Mode != PullOnly {
		r.push(ctx, rem)
	}
	if rem.Mode != PushOnly {
		r.pull(ctx, rem)
	}

	r.trimQueue()

	r.mu.Lock()
	if st, ok := r.statuses[rem.Name]; ok {
		st.lastSync = time.Now()
	}
	r.mu.Unlock()
	r.publishEvent(ctx, "sync.remote.synced", rem.Name)
}

func (r *Replicator[T]) trimQueue() {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Consider all registered remotes; untracked cursors are implicitly 0
	minCursor := len(r.queue)
	remotes := r.remotes.List()
	for _, rem := range remotes {
		c := r.cursors[rem.Name] // 0 if not yet tracked
		if c < minCursor {
			minCursor = c
		}
	}
	if minCursor > 0 {
		r.queue = r.queue[minCursor:]
		for name := range r.cursors {
			r.cursors[name] -= minCursor
		}
	}
}

func (r *Replicator[T]) push(ctx context.Context, rem Remote) {
	r.mu.Lock()
	cursor := r.cursors[rem.Name]
	pending := r.queue[cursor:]
	r.mu.Unlock()

	if len(pending) == 0 {
		return
	}

	// Apply filter
	var filtered []Diff
	for _, d := range pending {
		if rem.Filter == nil || rem.Filter(d) {
			filtered = append(filtered, d)
		}
	}

	if len(filtered) > 0 {
		if err := rem.Transport.Push(ctx, filtered); err != nil {
			r.setStatus(rem.Name, true, err)
			return
		}
	}

	r.mu.Lock()
	r.cursors[rem.Name] = cursor + len(pending)
	r.mu.Unlock()
}

func (r *Replicator[T]) pull(ctx context.Context, rem Remote) {
	r.mu.RLock()
	since := r.pulls[rem.Name]
	r.mu.RUnlock()

	diffs, err := rem.Transport.Pull(ctx, since)
	if err != nil {
		r.setStatus(rem.Name, true, err)
		return
	}

	var maxTS Timestamp
	for _, d := range diffs {
		r.clock.Update(d.Timestamp)
		r.applyDiff(ctx, d)
		if maxTS.Before(d.Timestamp) {
			maxTS = d.Timestamp
		}
	}

	if len(diffs) > 0 {
		r.mu.Lock()
		r.pulls[rem.Name] = maxTS
		r.mu.Unlock()
	}
}

func (r *Replicator[T]) applyDiff(ctx context.Context, d Diff) {
	if d.After == nil {
		if err := r.repo.Delete(ctx, d.EntityID); err != nil {
			r.logger.Error("sync: apply delete failed", "entity_id", d.EntityID, "err", err)
			r.publishEvent(ctx, "sync.apply.error", d.EntityID)
		}
		return
	}

	var entity T
	if err := json.Unmarshal(d.After, &entity); err != nil {
		r.logger.Error("sync: unmarshal diff failed", "entity_id", d.EntityID, "err", err)
		r.publishEvent(ctx, "sync.apply.error", d.EntityID)
		return
	}

	// Try update; if not found, create
	if err := r.repo.Update(ctx, &entity); err != nil {
		if err := r.repo.Create(ctx, &entity); err != nil {
			r.logger.Error("sync: apply create failed", "entity_id", d.EntityID, "err", err)
			r.publishEvent(ctx, "sync.apply.error", d.EntityID)
		}
	}
}

func (r *Replicator[T]) handleEvent(e bus.Event) {
	payload, ok := e.Payload.(map[string]string)
	if !ok {
		return
	}
	entityID := payload["id"]
	if entityID == "" {
		return
	}

	ts := r.clock.Now()
	d := Diff{
		EntityID:  entityID,
		Timestamp: ts,
		NodeID:    ts.NodeID,
	}

	// Suffix match on the action segment so dispatch survives any
	// configured prefix (default kit.runtime.entity, or any
	// 3-segment override passed via WithSubscriptionPrefix).
	topic := string(e.Topic)
	idx := strings.LastIndex(topic, ".")
	if idx < 0 {
		return
	}
	action := topic[idx+1:]
	switch action {
	case "created":
		d.Operation = OpCreate
	case "updated":
		d.Operation = OpUpdate
	case "deleted":
		d.Operation = OpDelete
	default:
		return
	}

	// Snapshot current state for create/update
	if d.Operation != OpDelete {
		ctx := r.ctx
		entity, err := r.repo.Get(ctx, entityID)
		if err == nil {
			d.After, _ = json.Marshal(entity)
		}
	}

	r.Enqueue(d)
}

func (r *Replicator[T]) setStatus(name string, connected bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.statuses[name]
	if !ok {
		st = &remoteState{}
		r.statuses[name] = st
	}
	st.connected = connected
	st.lastErr = err
}

func (r *Replicator[T]) publishEvent(ctx context.Context, topic, remoteName string) {
	if r.bus == nil {
		return
	}
	_ = r.bus.Publish(ctx, bus.Event{
		Topic:   bus.Topic(topic),
		Source:  fmt.Sprintf("sync.replicator.%s", remoteName),
		Payload: map[string]string{"remote": remoteName},
	})
}
