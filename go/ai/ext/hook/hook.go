// Package hook provides a thread-safe lifecycle hook system.
//
// Extensions subscribe handlers to named hooks; the bus dispatches
// them in priority order (lower values run first).
//
// Internally delegates to kit/bus for pub/sub transport while
// maintaining priority ordering and the DispatchAll contract.
package hook

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"hop.top/kit/go/runtime/bus"
)

// Hook is a named lifecycle event.
type Hook string

// Common lifecycle hooks.
const (
	BeforeInit  Hook = "before_init"
	AfterInit   Hook = "after_init"
	BeforeClose Hook = "before_close"
	AfterClose  Hook = "after_close"
	BeforeRun   Hook = "before_run"
	AfterRun    Hook = "after_run"
)

// DefaultTopicPrefix namespaces hook events on the bus using the kit
// 4-segment convention: kit.ext.hook.<action>. The trailing dot is part
// of the stored value so concatenation with the action segment is direct.
const DefaultTopicPrefix = "kit.ext.hook."

// Handler is a function invoked when a hook fires.
type Handler func(ctx context.Context, payload any) error

// entry pairs a handler with its priority.
type entry struct {
	handler  Handler
	priority int
}

// Option configures a Bus at construction time.
type Option func(*Bus)

// WithHookTopicPrefix overrides the topic prefix for hook lifecycle
// events. The prefix MUST be 3 dot-separated lowercase segments
// (source.category.object). Trailing dot is normalized: pass either
// "myapp.ext.hook" or "myapp.ext.hook." — internally the helper appends
// "." before concatenating the hook action.
//
// Default: "kit.ext.hook." → "kit.ext.hook.<action>".
//
// Construction-time validation enforces 3 lowercase segments and panics
// on misconfiguration so adopter wiring fails loudly. Per-action
// validation via bus.ValidateTopic happens best-effort when the hook
// actually fires; hook actions are open-ended adopter-defined strings,
// so a non-past-tense action will skip the publish (logged via the
// inner bus error path) rather than fail construction.
func WithHookTopicPrefix(prefix string) Option {
	normalized := normalizeHookPrefix(prefix)
	return func(b *Bus) {
		b.topicPrefix = normalized
	}
}

// normalizeHookPrefix strips a trailing dot, validates the 3-segment
// lowercase shape, and returns the prefix WITH a trailing dot ready for
// concatenation with the action segment. Panics on invalid input.
func normalizeHookPrefix(prefix string) string {
	trimmed := strings.TrimSuffix(prefix, ".")
	if trimmed == "" {
		panic(fmt.Sprintf("hook: WithHookTopicPrefix(%q): prefix is empty", prefix))
	}
	parts := strings.Split(trimmed, ".")
	if len(parts) != 3 {
		panic(fmt.Sprintf(
			"hook: WithHookTopicPrefix(%q): prefix has %d segments; expected 3 (source.category.object)",
			prefix, len(parts),
		))
	}
	for i, seg := range parts {
		if seg == "" {
			panic(fmt.Sprintf(
				"hook: WithHookTopicPrefix(%q): empty segment at position %d",
				prefix, i,
			))
		}
		for _, r := range seg {
			switch {
			case r >= 'a' && r <= 'z':
			case r >= '0' && r <= '9':
			case r == '_':
			default:
				panic(fmt.Sprintf(
					"hook: WithHookTopicPrefix(%q): segment %q must be lowercase letters, digits, or underscores",
					prefix, seg,
				))
			}
		}
	}
	return trimmed + "."
}

// Bus is a thread-safe hook dispatcher backed by kit/bus.
type Bus struct {
	mu          sync.RWMutex
	handlers    map[Hook][]entry
	inner       bus.Bus
	topicPrefix string
}

// NewBus returns a ready-to-use Bus.
func NewBus(opts ...Option) *Bus {
	b := &Bus{
		handlers:    make(map[Hook][]entry),
		inner:       bus.New(),
		topicPrefix: DefaultTopicPrefix,
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Subscribe registers a handler for the given hook.
// Lower priority values run first.
func (b *Bus) Subscribe(hook Hook, handler Handler, priority int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.handlers[hook] = append(b.handlers[hook], entry{
		handler:  handler,
		priority: priority,
	})
	sort.SliceStable(b.handlers[hook], func(i, j int) bool {
		return b.handlers[hook][i].priority < b.handlers[hook][j].priority
	})
}

// Dispatch runs all handlers for hook in priority order.
// It stops and returns the first non-nil error.
// If ctx is already canceled, Dispatch returns ctx.Err() immediately.
func (b *Bus) Dispatch(ctx context.Context, hook Hook, payload any) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	b.mu.RLock()
	entries := make([]entry, len(b.handlers[hook]))
	copy(entries, b.handlers[hook])
	b.mu.RUnlock()

	// Notify cross-cutting observers asynchronously so they cannot
	// block or veto hook handler execution.
	b.notify(ctx, hook, payload)

	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := e.handler(ctx, payload); err != nil {
			return err
		}
	}
	return nil
}

// DispatchAll runs all handlers for hook in priority order,
// collecting every error instead of stopping on the first.
// Context cancellation is checked before each handler.
func (b *Bus) DispatchAll(ctx context.Context, hook Hook, payload any) []error {
	if err := ctx.Err(); err != nil {
		return []error{err}
	}

	b.mu.RLock()
	entries := make([]entry, len(b.handlers[hook]))
	copy(entries, b.handlers[hook])
	b.mu.RUnlock()

	b.notify(ctx, hook, payload)

	var errs []error
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			return errs
		}
		if err := e.handler(ctx, payload); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// Handlers returns the number of handlers registered for hook.
func (b *Bus) Handlers(hook Hook) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.handlers[hook])
}

// notify publishes a hook event to the inner bus asynchronously.
// Observer errors and latency never affect hook handler execution.
//
// The composed topic is "<topicPrefix><hook>"; topicPrefix always ends
// in "." (see normalizeHookPrefix). bus.ValidateTopic is invoked
// best-effort: per-action validation cannot block publish because hook
// actions are open-ended adopter-defined strings (e.g. "before_init",
// "after_run") that don't always satisfy the past-tense convention.
// Construction-time validation on the prefix is the strict gate; the
// per-action call here exists as a hook for future telemetry/lint.
func (b *Bus) notify(ctx context.Context, hook Hook, payload any) {
	prefix := b.topicPrefix
	source := strings.TrimSuffix(prefix, ".")
	topic := bus.Topic(prefix + string(hook))
	_ = bus.ValidateTopic(topic) // best-effort; never gates publish
	go func() {
		_ = b.inner.Publish(ctx, bus.NewEvent(topic, source, payload))
	}()
}

// Inner returns the underlying kit/bus instance for cross-cutting
// observers that want to subscribe to hook events via topic patterns
// (e.g. "kit.ext.hook.#" for all lifecycle events). Observers should
// use SubscribeAsync or ensure sync handlers are non-blocking and
// never return errors.
func (b *Bus) Inner() bus.Bus {
	return b.inner
}

// ---- package-level default bus ----

var defaultBus = NewBus()

// Default returns the package-level Bus.
func Default() *Bus { return defaultBus }

// Subscribe registers a handler on the default bus.
func Subscribe(hook Hook, handler Handler, priority int) {
	defaultBus.Subscribe(hook, handler, priority)
}

// Dispatch fires a hook on the default bus.
func Dispatch(ctx context.Context, hook Hook, payload any) error {
	return defaultBus.Dispatch(ctx, hook, payload)
}

// DispatchAll fires a hook on the default bus, collecting all errors.
func DispatchAll(ctx context.Context, hook Hook, payload any) []error {
	return defaultBus.DispatchAll(ctx, hook, payload)
}

// Handlers returns the handler count on the default bus.
func Handlers(hook Hook) int {
	return defaultBus.Handlers(hook)
}
