package cmdsurface

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"hop.top/kit/go/transport/api"
)

// Bus surface header keys. Bus messages carry safety/auth context via
// the BusMessage.Headers map; the surface inspects only the presence
// of these keys (validation is a later wave).
const (
	// BusHeaderAuthorization is the credential the bridge inspects to
	// satisfy SafetyClass.AuthRequired. Any non-empty value passes;
	// callers MAY also set Invocation.Meta.Caller as a substitute.
	BusHeaderAuthorization = "authorization"
	// BusHeaderConfirmToken is the confirmation token header the
	// bridge inspects to satisfy SafetyClass.RequiresConfirmation.
	// Any non-empty value passes.
	BusHeaderConfirmToken = "x-confirm-token"
	// BusHeaderGroupID is the consumer-group identifier the adopter
	// reads from BusBinding.GroupID and forwards on each delivered
	// message.
	BusHeaderGroupID = "group_id"
)

// busSource is the canonical "source" string this surface passes to
// EventPublisher.Publish. Adopters use it to distinguish cmdsurface
// bus responses from other publishers when wiring multiple producers
// to the same backend.
const busSource = "cmdsurface.bus"

// Subscriber abstracts a pub/sub backend for the Bus surface.
// Adopters implement Subscribe against Kafka, NATS, Redis Streams,
// in-process channels, or any other queue. Subscribe registers
// handler for every message on topic; the returned cancel func
// removes the subscription. ctx applies to the subscription's
// lifetime — implementations SHOULD stop dispatching when ctx is
// done even if cancel was not called.
type Subscriber interface {
	Subscribe(ctx context.Context, topic string, handler func(msg BusMessage) error) (cancel func(), err error)
}

// BusMessage is one message received on a subscribed topic. Headers
// carries the adopter's per-message metadata (auth, confirmation,
// trace ids); Payload is the raw request body the surface decodes as
// JSON into an Invocation envelope.
type BusMessage struct {
	Topic   string
	Payload []byte
	Headers map[string]string
}

// BusBinding configures how one leaf maps onto bus topics. Path is
// the leaf's cobra path; RequestTopic is the topic the surface
// subscribes to on behalf of the leaf; ResponseTopic, when non-empty,
// is where the surface publishes each Result. GroupID is opaque to
// the surface — it is forwarded to the adopter via the per-handler
// invocation (placed in the Subscriber's own internal state at
// Subscribe time is the adopter's concern; the surface does not
// transmit it on Subscribe).
type BusBinding struct {
	// Path is the leaf path the binding addresses. Required.
	Path []string
	// RequestTopic is the topic the surface subscribes to. Required.
	RequestTopic string
	// ResponseTopic is the topic the surface publishes Results to.
	// Empty value means fire-and-forget: the surface invokes the
	// bridge and discards the Result.
	ResponseTopic string
	// GroupID is the consumer-group label the adopter uses to share
	// load across multiple subscribers. The surface does not consume
	// this field directly; adopters MAY read it from the binding
	// when registering with the broker.
	GroupID string
}

// busRequest is the JSON envelope the surface decodes from each
// request message. Path is intentionally absent — the topic
// determines the leaf.
type busRequest struct {
	Args  []string       `json:"args,omitempty"`
	Flags map[string]any `json:"flags,omitempty"`
	Meta  Meta           `json:"meta,omitempty"`
}

// busErrorEnvelope wraps a bridge / decode error in a JSON shape the
// surface publishes to ResponseTopic. It mirrors the {"error":{...}}
// convention adopted by REST + RPC mappings.
type busErrorEnvelope struct {
	Error busError `json:"error"`
}

// busError carries the canonical error code and human message.
type busError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// busConfig is the internal options bag for MountBus.
type busConfig struct {
	ctx    context.Context
	logger func(format string, args ...any)
}

// BusOption configures MountBus.
type BusOption func(*busConfig)

// WithBusContext sets the parent context for every subscription.
// MountBus derives a per-message context from this ctx so timeouts
// and cancellation propagate uniformly. Default: context.Background.
func WithBusContext(ctx context.Context) BusOption {
	return func(c *busConfig) {
		if ctx != nil {
			c.ctx = ctx
		}
	}
}

// WithBusLogger installs a printf-style logger the surface uses for
// non-fatal decode / publish errors. Default: no-op.
func WithBusLogger(fn func(format string, args ...any)) BusOption {
	return func(c *busConfig) {
		if fn != nil {
			c.logger = fn
		}
	}
}

// MountBus binds bus topics to bridge leaves. For each binding the
// surface:
//
//  1. Resolves Path against the bridge. Unknown leaves and leaves
//     without SurfaceBus enabled cause MountBus to return an error
//     before any subscription is registered.
//  2. Subscribes to RequestTopic. The handler decodes each delivered
//     BusMessage payload as a JSON busRequest, builds an Invocation
//     with Meta.Surface forced to SurfaceBus, calls Bridge.Invoke,
//     and (when ResponseTopic is non-empty) publishes the Result via
//     pub.Publish.
//  3. Aggregates the per-binding cancel funcs. The returned cleanup
//     invokes them in reverse subscription order so the most-recent
//     subscription is the first to unwind.
//
// Per-leaf safety gates:
//
//   - Class.AuthRequired: handler inspects msg.Headers["authorization"];
//     missing → response with {error:{code:"unauthenticated"}}. An
//     Invocation.Meta.Caller carried in the request envelope is an
//     acceptable substitute (matches RPC behavior).
//   - Class.RequiresConfirmation: handler inspects
//     msg.Headers["x-confirm-token"]; missing → response with
//     {error:{code:"confirmation_required"}}.
//   - Class.Destructive: gated by Policy.AllowDestructiveOn the same
//     way as every other remote surface; failure → response with
//     {error:{code:"destructive_blocked"}}.
//
// Errors during JSON decode, missing-leaf, surface-not-enabled, and
// destructive-blocked all produce a response envelope on
// ResponseTopic instead of crashing the handler. The handler returns
// nil to the subscriber in every case — application-level failures
// are conveyed in the response payload, not via subscriber redelivery.
func MountBus(
	b *Bridge,
	sub Subscriber,
	pub api.EventPublisher,
	bindings []BusBinding,
	opts ...BusOption,
) (cleanup func(), err error) {
	if b == nil {
		return nil, errors.New("cmdsurface: MountBus: nil Bridge")
	}
	if sub == nil {
		return nil, errors.New("cmdsurface: MountBus: nil Subscriber")
	}
	// pub MAY be nil only when every binding is fire-and-forget. We
	// check per-binding below.

	cfg := busConfig{
		ctx:    context.Background(),
		logger: func(string, ...any) {},
	}
	for _, o := range opts {
		o(&cfg)
	}

	// Snapshot bindings → resolved leaves. Validation happens up
	// front so a bad binding aborts the entire mount.
	type resolved struct {
		binding BusBinding
		leaf    *Leaf
	}
	plan := make([]resolved, 0, len(bindings))
	for i := range bindings {
		bd := bindings[i]
		if bd.RequestTopic == "" {
			return nil, fmt.Errorf("cmdsurface: MountBus: binding[%d] empty RequestTopic", i)
		}
		if len(bd.Path) == 0 {
			return nil, fmt.Errorf("cmdsurface: MountBus: binding[%d] empty Path (topic=%s)",
				i, bd.RequestTopic)
		}
		leaf, lerr := b.resolveLeaf(bd.Path)
		if lerr != nil {
			return nil, fmt.Errorf("cmdsurface: MountBus: binding[%d]: %w", i, lerr)
		}
		if !leaf.Enabled[SurfaceBus] {
			return nil, fmt.Errorf("cmdsurface: MountBus: binding[%d]: %w: %s",
				i, ErrSurfaceNotEnabled, leaf.PathKey())
		}
		if bd.ResponseTopic != "" && pub == nil {
			return nil, fmt.Errorf(
				"cmdsurface: MountBus: binding[%d]: ResponseTopic=%q requires a non-nil EventPublisher",
				i, bd.ResponseTopic)
		}
		plan = append(plan, resolved{binding: bd, leaf: leaf})
	}

	cancels := make([]func(), 0, len(plan))
	var cancelMu sync.Mutex

	for _, r := range plan {
		bd := r.binding
		leaf := r.leaf
		handler := newBusHandler(b, leaf, bd, pub, cfg)
		cancel, serr := sub.Subscribe(cfg.ctx, bd.RequestTopic, handler)
		if serr != nil {
			// Roll back already-registered subscriptions before
			// returning so the caller doesn't have to manage partial
			// state.
			runCancels(cancels)
			return nil, fmt.Errorf(
				"cmdsurface: MountBus: subscribe %q: %w", bd.RequestTopic, serr)
		}
		cancelMu.Lock()
		cancels = append(cancels, cancel)
		cancelMu.Unlock()
	}

	cleanup = func() {
		cancelMu.Lock()
		toCancel := cancels
		cancels = nil
		cancelMu.Unlock()
		runCancels(toCancel)
	}
	return cleanup, nil
}

// runCancels invokes each cancel in reverse order. nil entries are
// skipped — Subscribers are permitted to return nil cancel funcs when
// the subscription is implicitly unwound by ctx cancellation.
func runCancels(cancels []func()) {
	for i := len(cancels) - 1; i >= 0; i-- {
		if cancels[i] != nil {
			cancels[i]()
		}
	}
}

// newBusHandler returns the per-message handler closure the
// Subscriber invokes on every delivery. Each call:
//
//  1. Decodes the JSON request envelope (or publishes a bad_request
//     error envelope on parse failure).
//  2. Checks auth and confirmation headers against the leaf class.
//  3. Forces inv.Path = leaf.Path and inv.Meta.Surface = SurfaceBus.
//  4. Calls Bridge.Invoke; publishes Result or error envelope on
//     ResponseTopic (when non-empty).
//
// The handler returns nil to the subscriber regardless of application
// outcome — the bus protocol does not signal app errors via redelivery.
func newBusHandler(
	b *Bridge,
	leaf *Leaf,
	bd BusBinding,
	pub api.EventPublisher,
	cfg busConfig,
) func(BusMessage) error {
	return func(msg BusMessage) error {
		ctx := cfg.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		var req busRequest
		if len(msg.Payload) > 0 {
			if derr := json.Unmarshal(msg.Payload, &req); derr != nil {
				publishError(ctx, pub, bd.ResponseTopic, cfg.logger,
					"bad_request", derr.Error())
				return nil
			}
		}
		// Auth gate (header presence OR Meta.Caller).
		if leaf.Class.AuthRequired {
			if headerLookup(msg.Headers, BusHeaderAuthorization) == "" && req.Meta.Caller == "" {
				publishError(ctx, pub, bd.ResponseTopic, cfg.logger,
					"unauthenticated",
					fmt.Sprintf("auth required: %s", leaf.PathKey()))
				return nil
			}
		}
		// Confirmation gate.
		if leaf.Class.RequiresConfirmation {
			if headerLookup(msg.Headers, BusHeaderConfirmToken) == "" {
				publishError(ctx, pub, bd.ResponseTopic, cfg.logger,
					"confirmation_required",
					fmt.Sprintf("x-confirm-token header required: %s", leaf.PathKey()))
				return nil
			}
		}

		inv := Invocation{
			Path:  append([]string(nil), leaf.Path...),
			Args:  req.Args,
			Flags: req.Flags,
			Meta:  req.Meta,
		}
		inv.Meta.Surface = SurfaceBus
		inv.Meta.RequestedAt = time.Now()

		res, ierr := b.Invoke(ctx, inv)
		if ierr != nil {
			publishError(ctx, pub, bd.ResponseTopic, cfg.logger,
				bridgeErrorCode(ierr), ierr.Error())
			return nil
		}
		if bd.ResponseTopic == "" {
			return nil
		}
		if perr := pub.Publish(ctx, bd.ResponseTopic, busSource, res); perr != nil {
			cfg.logger("cmdsurface.bus: publish %q: %v", bd.ResponseTopic, perr)
		}
		return nil
	}
}

// headerLookup is a case-insensitive lookup against msg.Headers. Bus
// adopters disagree on header casing (Kafka headers are byte slices
// with arbitrary casing; NATS uses canonical MIME-style); the
// surface normalises to lower-case keys for portability.
func headerLookup(headers map[string]string, key string) string {
	if v, ok := headers[key]; ok && v != "" {
		return v
	}
	lower := strings.ToLower(key)
	for k, v := range headers {
		if strings.ToLower(k) == lower {
			return v
		}
	}
	return ""
}

// bridgeErrorCode maps a Bridge sentinel error onto the bus response
// envelope's code field. Non-sentinel errors fall back to "internal".
func bridgeErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrUnknownCommand):
		return "unknown_command"
	case errors.Is(err, ErrSurfaceNotEnabled):
		return "not_enabled"
	case errors.Is(err, ErrDestructiveBlocked):
		return "destructive_blocked"
	default:
		return "internal"
	}
}

// publishError emits a {error:{code,message}} envelope on topic. When
// pub or topic is nil/empty the function is a no-op (the binding is
// fire-and-forget). Publish failures are logged but otherwise
// swallowed — the surface cannot recover from a broken response path.
func publishError(
	ctx context.Context,
	pub api.EventPublisher,
	topic string,
	logger func(string, ...any),
	code, message string,
) {
	if pub == nil || topic == "" {
		return
	}
	env := busErrorEnvelope{Error: busError{Code: code, Message: message}}
	if err := pub.Publish(ctx, topic, busSource, env); err != nil {
		logger("cmdsurface.bus: publish error envelope %q: %v", topic, err)
	}
}
