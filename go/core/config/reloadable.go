package config

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/runtime/domain"
)

// Reloadable is a generic wrapper around a typed config snapshot that
// supports atomic, in-process hot reload. Consumers hold the *Reloadable
// (not the inner *T) and read the current snapshot via Snapshot. Each
// successful Reload swaps the held pointer atomically; readers see either
// the old fully-formed snapshot or the new one — never a torn write.
//
// T is expected to be a struct shape (a pointer-to-struct in the
// wrapper). Field-level mutability is opt-in via the `reload:"true"`
// struct tag; immutable changes are vetoed at reload time. See Partition
// (added in a follow-up subtask) for the partitioning contract.
//
// A Reloadable holds the live Options used to re-run Load. Callers may
// pass a fresh Options to Reload — typically only ExtraConfigPaths varies
// across reloads — and the new Options replaces the held one on success.
//
// Zero-value Reloadable is not useful; construct via New.
type Reloadable[T any] struct {
	cur    atomic.Pointer[T]
	mu     sync.Mutex // serializes Reload; readers never block
	opts   Options
	pub    domain.EventPublisher
	topics ReloadTopics
	source string
}

// ReloadTopics holds the per-action topic strings emitted by Reloadable.
//
// Reloadable publishes one event per reload outcome:
//   - Reloaded     — Reload swapped the snapshot atomically
//   - ReloadFailed — Reload was vetoed (immutable diff or load error)
//
// Adopters override individual actions with WithReloadTopics or replace
// both with WithReloadTopicPrefix.
type ReloadTopics struct {
	Reloaded     bus.Topic
	ReloadFailed bus.Topic
}

// DefaultReloadTopics is the kit baseline used when no override is supplied.
// Each topic conforms to the kit 4-segment past-tense convention and
// passes bus.ValidateTopic.
var DefaultReloadTopics = ReloadTopics{
	Reloaded:     "kit.config.snapshot.reloaded",
	ReloadFailed: "kit.config.snapshot.reload_failed",
}

// reloadActions is the canonical action list passed to bus.PrefixTopics
// when expanding a 3-segment prefix.
var reloadActions = []string{"reloaded", "reload_failed"}

// defaultReloadSource is the Source field set on every published event.
const defaultReloadSource = "core.config"

// ReloadOption customizes a Reloadable at construction.
type ReloadOption func(*reloadConfig)

type reloadConfig struct {
	pub    domain.EventPublisher
	topics ReloadTopics
	source string
}

// WithReloadPublisher attaches an EventPublisher used to emit reload
// lifecycle events. Bus emission is opt-in: a nil publisher (the default)
// preserves the historical behavior — Reload swaps silently.
//
// Publishing is best-effort, fire-and-forget on a goroutine: it must not
// block or fail the reload flow. Errors from the publisher are dropped.
func WithReloadPublisher(p domain.EventPublisher) ReloadOption {
	return func(c *reloadConfig) { c.pub = p }
}

// WithReloadTopics replaces individual reload topics. Empty bus.Topic
// fields keep the corresponding DefaultReloadTopics value, so callers can
// override one action without restating the other.
func WithReloadTopics(t ReloadTopics) ReloadOption {
	return func(c *reloadConfig) {
		if t.Reloaded == "" {
			t.Reloaded = DefaultReloadTopics.Reloaded
		}
		if t.ReloadFailed == "" {
			t.ReloadFailed = DefaultReloadTopics.ReloadFailed
		}
		c.topics = t
	}
}

// WithReloadTopicPrefix sets both reload topics from a 3-segment prefix
// of the form "source.category.object". Composed topics are
// "<prefix>.reloaded" and "<prefix>.reload_failed".
//
// Panics if prefix fails bus.PrefixTopics validation. Constructors are
// wired at boot, so a misconfigured prefix is a programmer error.
func WithReloadTopicPrefix(prefix string) ReloadOption {
	tm, err := bus.PrefixTopics(prefix, reloadActions)
	if err != nil {
		panic(fmt.Sprintf("config.WithReloadTopicPrefix(%q): %v", prefix, err))
	}
	return func(c *reloadConfig) {
		c.topics = ReloadTopics{
			Reloaded:     tm["reloaded"],
			ReloadFailed: tm["reload_failed"],
		}
	}
}

// WithReloadSource overrides the Source string published with every event.
// Defaults to "core.config".
func WithReloadSource(src string) ReloadOption {
	return func(c *reloadConfig) { c.source = src }
}

// New constructs a Reloadable seeded with initial. opts is the live
// Options re-used by Reload when no fresh Options is supplied. Caller
// keeps ownership of initial after the call; Reloadable does not mutate
// it. Pass options to attach a publisher and customize topics.
//
// initial must be non-nil. Pass a freshly Load()-ed pointer.
func New[T any](initial *T, opts Options, options ...ReloadOption) *Reloadable[T] {
	if initial == nil {
		panic("config.New: initial *T is nil")
	}
	cfg := reloadConfig{
		topics: DefaultReloadTopics,
		source: defaultReloadSource,
	}
	for _, o := range options {
		o(&cfg)
	}
	r := &Reloadable[T]{
		opts:   opts,
		pub:    cfg.pub,
		topics: cfg.topics,
		source: cfg.source,
	}
	r.cur.Store(initial)
	return r
}

// Snapshot returns the current snapshot pointer. Readers must treat the
// returned value as immutable: future reloads atomically swap to a new
// pointer rather than mutating the old one. A typical reader caches the
// pointer for a single operation and re-calls Snapshot for the next.
func (r *Reloadable[T]) Snapshot() *T {
	return r.cur.Load()
}

// Options returns a copy of the live Options used by the next Reload
// when invoked without an explicit Options argument. The returned value
// is safe to mutate (e.g. to add an ExtraConfigPath) before passing back
// to Reload.
func (r *Reloadable[T]) Options() Options {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.opts
}

// ErrImmutableChanged is returned by Reload when one or more fields not
// tagged `reload:"true"` differ between the old and new snapshots. The
// snapshot held by the Reloadable is unchanged.
//
// The Paths slice lists the dotted field paths whose values differ. The
// failure event published to the bus carries the same list.
type ErrImmutableChanged struct {
	Paths []string
}

func (e *ErrImmutableChanged) Error() string {
	return fmt.Sprintf(
		"config reload vetoed: immutable field(s) changed: %s",
		strings.Join(e.Paths, ", "),
	)
}

// Is reports whether target is an *ErrImmutableChanged so callers can use
// errors.Is for sentinel-style matching.
func (e *ErrImmutableChanged) Is(target error) bool {
	_, ok := target.(*ErrImmutableChanged)
	return ok
}

// Reload re-runs Load against newOpts, partitions the result, and:
//
//   - returns ErrImmutableChanged (without swapping) if any non-tagged
//     field differs from the held snapshot — emitting a reload_failed
//     event with the offending paths
//   - returns the wrapped error from Load if loading fails — emitting a
//     reload_failed event with the error string
//   - otherwise atomically swaps the held snapshot to the new pointer,
//     replaces the held Options with newOpts, and emits a reloaded event
//     with the mutable diff
//
// Reload serializes concurrent calls via an internal mutex. Readers using
// Snapshot() are not blocked by an in-flight reload; they observe either
// the pre-reload pointer or the post-reload pointer, never a partial state.
func (r *Reloadable[T]) Reload(newOpts Options) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	old := r.cur.Load()
	var fresh T
	if err := Load(&fresh, newOpts); err != nil {
		r.publishLoadFailed(newOpts, err)
		return fmt.Errorf("config reload: %w", err)
	}

	mutablePaths, immutablePaths, err := Partition(&fresh)
	if err != nil {
		r.publishLoadFailed(newOpts, err)
		return fmt.Errorf("config reload: partition new snapshot: %w", err)
	}

	immDiff, err := diffPaths(old, &fresh, immutablePaths)
	if err != nil {
		r.publishLoadFailed(newOpts, err)
		return fmt.Errorf("config reload: diff immutable: %w", err)
	}
	if len(immDiff) > 0 {
		offending := make([]string, 0, len(immDiff))
		for path := range immDiff {
			offending = append(offending, path)
		}
		sortStrings(offending)
		errOut := &ErrImmutableChanged{Paths: offending}
		r.publishImmutableVeto(newOpts, offending, immDiff, errOut)
		return errOut
	}

	mutDiff, err := diffPaths(old, &fresh, mutablePaths)
	if err != nil {
		r.publishLoadFailed(newOpts, err)
		return fmt.Errorf("config reload: diff mutable: %w", err)
	}

	r.cur.Store(&fresh)
	r.opts = newOpts
	r.publishReloaded(newOpts, mutDiff)
	return nil
}

// sortStrings sorts in ascending order in place. Diff path slices are
// tiny, so a small insertion-sort keeps imports lean and runtime O(n^2)
// is irrelevant.
func sortStrings(in []string) {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && in[j-1] > in[j]; j-- {
			in[j-1], in[j] = in[j], in[j-1]
		}
	}
}

// reloadableErrIs hooks into the errors package so static-analysis tools
// see the package-level use.
var _ = errors.Is

// publish performs a best-effort, non-blocking publish. nil publisher
// is a no-op so adopters who never opt in pay nothing.
func (r *Reloadable[T]) publish(topic bus.Topic, payload any) {
	if r.pub == nil || topic == "" {
		return
	}
	go func() {
		_ = r.pub.Publish(context.Background(), string(topic), r.source, payload)
	}()
}

// publishReloaded fires the success topic with the supplied diff,
// using the typed ReloadedPayload struct.
func (r *Reloadable[T]) publishReloaded(opts Options, diff map[string]FieldDiff) {
	r.publish(r.topics.Reloaded, ReloadedPayload{
		MutableDiff: diff,
		SourcePaths: collectSourcePaths(opts),
	})
}

// publishImmutableVeto fires the failure topic for an immutable-field
// change. The would-have-been mutable diff is included so subscribers
// can see the full delta, not just the offending paths.
func (r *Reloadable[T]) publishImmutableVeto(
	opts Options, offending []string, mutDiff map[string]FieldDiff, err error,
) {
	r.publish(r.topics.ReloadFailed, ReloadFailedPayload{
		Reason:      ReloadFailReasonImmutableChanged,
		Offending:   offending,
		Error:       err.Error(),
		MutableDiff: mutDiff,
		SourcePaths: collectSourcePaths(opts),
	})
}

// publishLoadFailed fires the failure topic for a Load error.
func (r *Reloadable[T]) publishLoadFailed(opts Options, err error) {
	r.publish(r.topics.ReloadFailed, ReloadFailedPayload{
		Reason:      ReloadFailReasonLoadError,
		Error:       err.Error(),
		SourcePaths: collectSourcePaths(opts),
	})
}

// collectSourcePaths returns the ordered set of file paths Load
// considered for opts. Empty slots are preserved so subscribers can tell
// which layer was unset.
func collectSourcePaths(opts Options) []string {
	out := []string{
		opts.SystemConfigPath,
		opts.UserConfigPath,
		opts.ProjectConfigPath,
	}
	out = append(out, opts.ExtraConfigPaths...)
	return out
}
