// Package cmdsurface — TelemetrySink fans cmdsurface invocation
// outcomes into the kit-telemetry pipeline.
//
// This file implements T-0675 of the cmdsurf-telemetry track. See
// `.tlc/tracks/cmdsurf-telemetry/design-note.md` for the contracts this
// implementation pins (canonical contracts owned by ADR-0035).
//
// Seam properties:
//
//   - Non-blocking: Emit returns within ~1ms regardless of downstream
//     pressure. Backpressure surfaces as dropped events (see Stats).
//   - Mode-aware: Anon ships only bounded canonical fields. Full
//     additionally ships post-redact Args/Flags and synthesises the
//     surface name as Flags["_surface"] (telemetry.Event has no
//     Surface column today; see design-note §3).
//   - Consent-deaf: cmdsurface never consults consent. The emitter
//     no-ops when consent is denied; the channel may briefly fill in
//     the denied window (cosmetic DroppedFull bump, see design-note §6).
//   - Size-capped: post-translation payloads exceeding MaxBytes are
//     dropped, never truncated (truncation would defeat the redactor).
package cmdsurface

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"hop.top/kit/go/runtime/telemetry"
)

// Defaults for TelemetrySink construction.
const (
	defaultTelemetryChannelCap = 256
	defaultTelemetryMaxBytes   = 8192
)

// InvocationEvent is the cmdsurface-flavoured intermediate value the
// sink hands to its drain goroutine. It carries every canonical
// telemetry.Event field plus a surface-only Surface stamp; see the
// cmdsurf-telemetry design note §2 for full provenance.
//
// The struct is part of the cmdsurface public surface so adopters
// writing custom sinks (or audit subscribers) can read the same shape
// the kit-telemetry sink consumes. Wire fields use snake_case to match
// the canonical telemetry.Event wire shape.
type InvocationEvent struct {
	// CommandPath is cobra's path from root to leaf, e.g.
	// ["widget","add"]. Renamed from "Path" post-reconciliation so it
	// round-trips through telemetry.Event without translation.
	CommandPath []string `json:"command_path"`
	// ExitCode is the process-style exit status.
	ExitCode int `json:"exit_code"`
	// DurationMS is the elapsed time from Meta.RequestedAt to event
	// completion. -1 when RequestedAt was not stamped by the surface
	// (sentinel per design-note §2).
	DurationMS int64 `json:"duration_ms"`
	// OccurredAt is the wall-clock time the cmdsurface materialised the
	// event. RFC 3339 with nanos (ADR-0035 §7).
	OccurredAt time.Time `json:"occurred_at"`
	// TraceID propagates inv.Meta.TraceID; omitempty so empty IDs
	// disappear from the wire.
	TraceID string `json:"trace_id,omitempty"`

	// Surface is the originating cmdsurface surface (cli, rest, mcp,
	// …). Carried in-memory; the telemetry.Event has no Surface column
	// so the sink translates it (drop in Anon, Flags["_surface"] in
	// Full) before calling the emitter. See design-note §3.
	Surface string `json:"surface"`

	// Args and Flags are populated only in Full mode. Args is the
	// positional tail; Flags carries typed flag values pre-string-
	// conversion. Both go through telemetry.MustLoadRedactor() in the
	// emitter before publish.
	Args  []string       `json:"args,omitempty"`
	Flags map[string]any `json:"flags,omitempty"`
}

// emitterIface is the minimum surface TelemetrySink consumes from
// telemetry.Emitter. Defined here so tests can drop in a stub without
// instantiating a full bus + redactor + emitter. Production wiring
// passes the real *telemetry.Emitter (WithEmitter), which satisfies
// this interface by virtue of its Record method.
type emitterIface interface {
	Record(ctx context.Context, ev telemetry.Event) error
}

// TelemetryStats reports counters from a TelemetrySink. Sampled
// atomically by kit-telemetry inspect tooling.
//
// All fields are monotonic counters; differences across two reads give
// the per-window deltas.
type TelemetryStats struct {
	// Emitted is the number of events the drain successfully handed
	// to the emitter (no error returned).
	Emitted int64
	// DroppedFull counts events refused at Emit time because the
	// channel was saturated.
	DroppedFull int64
	// DroppedOversize counts events the drain refused because the
	// translated telemetry.Event JSON-marshalled larger than MaxBytes.
	DroppedOversize int64
	// DroppedDenied counts events the emitter rejected with a non-nil
	// error. The emitter soft-refuses (returns nil) for mode/consent
	// gates, so this counter advances only on hard failures (bus
	// publish error, validation failure that escapes the soft-refuse
	// path).
	DroppedDenied int64
	// RequestedAtMissing counts events whose surface failed to stamp
	// Meta.RequestedAt. The event still ships with DurationMS=-1
	// (design-note §2); the counter surfaces the cosmetic issue to
	// operators.
	RequestedAtMissing int64
}

// TelemetryOption configures a TelemetrySink at construction.
type TelemetryOption func(*telemetryConfig)

// telemetryConfig is the unexported option bag. Kept private so the
// stable surface is TelemetryOption + the With* helpers.
type telemetryConfig struct {
	emitter    emitterIface
	channelCap int
	maxBytes   int
	mode       telemetry.Mode
	kitVersion string
}

// WithChannelCap sets the buffered channel capacity. Defaults to 256;
// see design-note §5 for the rationale on why this is tunable but does
// not auto-grow.
func WithChannelCap(n int) TelemetryOption {
	return func(c *telemetryConfig) {
		if n > 0 {
			c.channelCap = n
		}
	}
}

// WithMaxBytes sets the per-event byte cap applied after translation.
// Defaults to 8192; see design-note §4 for sizing rationale.
func WithMaxBytes(n int) TelemetryOption {
	return func(c *telemetryConfig) {
		if n > 0 {
			c.maxBytes = n
		}
	}
}

// WithMode sets the sink's view of the telemetry mode at construction
// time. Anon strips Args/Flags/Surface; Full ships them post-redact.
// Defaults to telemetry.ModeAnon. The emitter performs its own mode
// check at Record time — this option only controls what the sink
// places into the InvocationEvent before queuing.
func WithMode(m telemetry.Mode) TelemetryOption {
	return func(c *telemetryConfig) { c.mode = m }
}

// WithEmitter wires the telemetry emitter. Required. Pass the real
// *telemetry.Emitter from telemetry.New() or any value satisfying the
// emitterIface contract (Record(ctx, telemetry.Event) error).
func WithEmitter(e emitterIface) TelemetryOption {
	return func(c *telemetryConfig) { c.emitter = e }
}

// WithKitVersion stamps every emitted telemetry.Event with v. Forwarded
// verbatim to telemetry.Event.KitVersion.
func WithKitVersion(v string) TelemetryOption {
	return func(c *telemetryConfig) { c.kitVersion = v }
}

// ErrTelemetrySinkNoEmitter is returned by NewTelemetrySink when
// WithEmitter was not supplied. The sink has no fallback emitter; an
// unbound sink would silently buffer events forever.
var ErrTelemetrySinkNoEmitter = errors.New("cmdsurface: TelemetrySink requires WithEmitter")

// invocationCarrier pairs an InvocationEvent with the originating
// caller's context. The carrier flows through the buffered channel so
// the drain goroutine forwards the caller's ctx (and any per-ctx
// telemetry.WithMode / telemetry.WithConsentHook overrides) into
// emitter.Record. Dropping the ctx at the channel boundary would lose
// these overrides — the global default would win, silently — which is
// T-0682's reported failure mode.
//
// The ctx may have been canceled by the time the drain processes the
// carrier. That is intentional: telemetry is fire-and-forget, the
// emitter's Record is non-blocking, and ctx-cancellation does not
// invalidate the Value lookups the emitter performs (WithMode /
// WithConsentHook stamp values, not deadlines).
type invocationCarrier struct {
	ctx context.Context
	ev  InvocationEvent
}

// TelemetrySink fans cmdsurface invocation outcomes into the
// kit-telemetry pipeline. Implements the cmdsurface Sink interface.
//
// Construct via NewTelemetrySink. Safe for concurrent Emit calls; the
// drain goroutine is single-threaded by design (the emitter handles
// concurrency on its side).
type TelemetrySink struct {
	ch         chan invocationCarrier
	emitter    emitterIface
	maxBytes   int
	mode       telemetry.Mode
	kitVersion string

	emitted            atomic.Int64
	droppedFull        atomic.Int64
	droppedOversize    atomic.Int64
	droppedDenied      atomic.Int64
	requestedAtMissing atomic.Int64

	wg        sync.WaitGroup
	closeOnce sync.Once
}

// NewTelemetrySink constructs a sink and starts its drain goroutine.
// Returns ErrTelemetrySinkNoEmitter when WithEmitter is missing.
//
// The drain goroutine exits when Close is called; callers MUST Close
// the sink during process shutdown to avoid leaking the goroutine and
// to flush queued events within the Close context deadline.
func NewTelemetrySink(opts ...TelemetryOption) (*TelemetrySink, error) {
	cfg := telemetryConfig{
		channelCap: defaultTelemetryChannelCap,
		maxBytes:   defaultTelemetryMaxBytes,
		mode:       telemetry.ModeAnon,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.emitter == nil {
		return nil, ErrTelemetrySinkNoEmitter
	}
	s := &TelemetrySink{
		ch:         make(chan invocationCarrier, cfg.channelCap),
		emitter:    cfg.emitter,
		maxBytes:   cfg.maxBytes,
		mode:       cfg.mode,
		kitVersion: cfg.kitVersion,
	}
	s.wg.Add(1)
	go s.drain()
	return s, nil
}

// Emit is the cmdsurface Sink interface method. Never blocks; on a
// saturated channel it increments DroppedFull and returns nil (soft
// refusal — the caller's hot path must not see telemetry pressure).
//
// The err argument is intentionally ignored: telemetry events are
// invocation summaries (ExitCode + duration), not error reports. A
// non-zero ExitCode in res captures the failure signal. Adopters who
// need richer error reporting wire LogSink or BusSink in parallel.
//
// ctx is captured onto the carrier (see invocationCarrier) so per-ctx
// overrides — telemetry.WithMode(ctx, …), telemetry.WithConsentHook(
// ctx, …) — survive the channel boundary and reach emitter.Record
// when the drain processes the event. Without this, the original
// caller's ctx would be replaced with context.Background() at ship
// time and the global default would silently win (T-0682).
func (s *TelemetrySink) Emit(ctx context.Context, inv Invocation, res Result, _ error) error {
	ev := InvocationEvent{
		CommandPath: append([]string(nil), inv.Path...),
		ExitCode:    res.ExitCode,
		Surface:     string(inv.Meta.Surface),
		OccurredAt:  time.Now().UTC(),
		TraceID:     inv.Meta.TraceID,
	}
	if inv.Meta.RequestedAt.IsZero() {
		ev.DurationMS = -1
		s.requestedAtMissing.Add(1)
	} else {
		ev.DurationMS = time.Since(inv.Meta.RequestedAt).Milliseconds()
	}

	// Full-mode fields are placed on the InvocationEvent at queue time
	// so the drain doesn't have to re-derive mode. The emitter
	// independently strips Anon-tier events; this is the producer-side
	// half of the two-layer strip (design-note §3).
	if s.mode == telemetry.ModeFull {
		if len(inv.Args) > 0 {
			ev.Args = append([]string(nil), inv.Args...)
		}
		if len(inv.Flags) > 0 {
			ev.Flags = make(map[string]any, len(inv.Flags))
			for k, v := range inv.Flags {
				ev.Flags[k] = v
			}
		}
	}

	// Non-blocking enqueue. The select-default pattern is the only
	// shape that guarantees Emit returns in O(1) when the channel is
	// saturated; a timeout-based send would still block up to the
	// timeout under contention. The carrier pairs ev with ctx so
	// per-ctx telemetry overrides survive the channel boundary.
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case s.ch <- invocationCarrier{ctx: ctx, ev: ev}:
		return nil
	default:
		s.droppedFull.Add(1)
		return nil
	}
}

// drain reads from the channel until it is closed, shipping each event
// to the emitter. Exits via Close().
func (s *TelemetrySink) drain() {
	defer s.wg.Done()
	for c := range s.ch {
		s.shipOne(c.ctx, c.ev)
	}
}

// shipOne translates an InvocationEvent into a canonical
// telemetry.Event and forwards to the emitter. Applies the size cap
// AFTER translation (so the cap reflects what would actually go on the
// wire). Increments per-outcome counters.
//
// ctx is the caller's original context (captured at Emit time) so any
// per-ctx overrides (telemetry.WithMode, telemetry.WithConsentHook)
// reach the emitter. The ctx may be canceled by now; that is fine —
// the emitter does not check ctx.Err() before its Value lookups, and
// per-ctx overrides survive cancellation.
func (s *TelemetrySink) shipOne(ctx context.Context, ev InvocationEvent) {
	teleEv := telemetry.Event{
		CommandPath: ev.CommandPath,
		ExitCode:    ev.ExitCode,
		DurationMS:  ev.DurationMS,
		OccurredAt:  ev.OccurredAt,
		KitVersion:  s.kitVersion,
		TraceID:     ev.TraceID,
	}

	// Surface workaround (design-note §3): Anon drops Surface entirely
	// because telemetry.Event has no Surface column and Anon's promise
	// is "no flags". Full synthesises Flags["_surface"] so adopters can
	// route on surface without scraping out-of-band fields.
	if s.mode == telemetry.ModeFull {
		if len(ev.Args) > 0 {
			teleEv.Args = ev.Args
		}
		teleEv.Flags = make(map[string]string, len(ev.Flags)+1)
		for k, v := range ev.Flags {
			teleEv.Flags[k] = fmt.Sprintf("%v", v)
		}
		if ev.Surface != "" {
			teleEv.Flags["_surface"] = ev.Surface
		}
		if len(teleEv.Flags) == 0 {
			teleEv.Flags = nil
		}
	}

	// Size cap. Marshal once: this is the same JSON the bus codec
	// will produce, so the byte count is faithful. We deliberately do
	// NOT truncate — a redacted token straddling the cut point would
	// leak its prefix (design-note §4).
	blob, err := json.Marshal(teleEv)
	if err != nil {
		// json.Marshal of a struct with only standard scalar fields
		// effectively cannot fail; treat as a soft drop with no
		// counter (the failure mode is "programming error", not "size
		// pressure" or "consent denial").
		return
	}
	if len(blob) > s.maxBytes {
		s.droppedOversize.Add(1)
		return
	}

	// Hand to the emitter using the caller's original ctx (carried
	// through the channel). emitter.Record performs mode + consent +
	// stamp + strip + redact + validate + publish (telemetry/emitter.go).
	// Per-ctx overrides (WithMode, WithConsentHook) flow here without
	// fallback to package globals — that's the whole point of using ctx.
	// Any error here is a hard failure (bus publish or producer bug);
	// soft refusals (Off, consent denied) return nil and we count them
	// as Emitted.
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.emitter.Record(ctx, teleEv); err != nil {
		s.droppedDenied.Add(1)
		return
	}
	s.emitted.Add(1)
}

// Stats returns a snapshot of the sink's counters. The reads are
// atomic but not transactionally consistent across all fields — for
// operator inspection this is fine; the counters are monotonic.
func (s *TelemetrySink) Stats() TelemetryStats {
	return TelemetryStats{
		Emitted:            s.emitted.Load(),
		DroppedFull:        s.droppedFull.Load(),
		DroppedOversize:    s.droppedOversize.Load(),
		DroppedDenied:      s.droppedDenied.Load(),
		RequestedAtMissing: s.requestedAtMissing.Load(),
	}
}

// Close stops the drain goroutine and waits up to ctx's deadline for
// pending events to ship. Idempotent; subsequent calls return nil.
//
// Concurrent Emit calls after Close are racy by definition — the
// channel may be closed mid-send. Callers MUST stop emitting before
// calling Close. The two-phase shutdown pattern (stop producers, then
// drain) is standard for fire-and-forget pipelines.
func (s *TelemetrySink) Close(ctx context.Context) error {
	s.closeOnce.Do(func() { close(s.ch) })
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
