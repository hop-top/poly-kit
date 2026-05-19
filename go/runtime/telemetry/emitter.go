package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"hop.top/kit/go/core/redact"
	"hop.top/kit/go/runtime/bus"
)

// defaultTopicPrefix is the 3-segment kit-owned prefix prepended to the
// past-tense action ".recorded" to form the wire topic. Adopters
// override via WithTopicPrefix("<app>.telemetry.event").
const defaultTopicPrefix = "kit.telemetry.event"

// recordedAction is the past-tense action suffix appended to the topic
// prefix. Centralised so a future rename can't drift between Record and
// tests.
const recordedAction = "recorded"

// busSource is the Source field stamped on the bus envelope. Identifies
// the emitter package to subscribers without scraping the payload.
const busSource = "kit.runtime.telemetry"

// installIDFunc is the lazy installation_id resolver. Defaults to the
// real InstallationID(); overridable per-emitter for tests via
// withInstallIDFunc (unexported — internal test seam only).
type installIDFunc func() (string, error)

// Errors returned by New for configuration failures. Construction-time
// only — Record's error returns are reserved for runtime failures
// (currently just bus publish errors).
var (
	// ErrEmitterMissingBus is returned by New when WithBus was not
	// supplied. The emitter has no fallback bus; an unbound emitter
	// would silently drop events.
	ErrEmitterMissingBus = errors.New("telemetry: New requires WithBus")

	// ErrEmitterMissingRedactor is returned by New when the
	// package-global mode resolves to Full but no WithRedactor was
	// supplied. Emitting Full-tier events without a redactor would
	// leak unredacted Args/Flags onto the bus.
	ErrEmitterMissingRedactor = errors.New("telemetry: New requires WithRedactor when Mode=Full")
)

// Emitter publishes telemetry events to a bus topic, gated by mode,
// consent, and redact. Construct via [New]. Safe for concurrent use.
type Emitter struct {
	bus         bus.Bus
	redactor    *redact.Redactor
	topicPrefix string
	kitVersion  string
	sdkVersion  string

	// installID resolves the persisted installation_id lazily and
	// caches the result. The resolver is overridable per-emitter for
	// tests (internal seam); production code uses InstallationID.
	installIDFn installIDFunc

	idMu      sync.Mutex
	idCached  string
	idCacheOK bool

	// logger receives soft-refusal diagnostics (validation failure,
	// installation_id lookup error). Unconfigured → slog.Default().
	logger *slog.Logger
}

// emitterConfig is the option target for [Option]. Kept private so the
// stable surface is Option + the With* helpers.
type emitterConfig struct {
	bus         bus.Bus
	redactor    *redact.Redactor
	topicPrefix string
	kitVersion  string
	sdkVersion  string
	installID   installIDFunc
	logger      *slog.Logger
}

// Option configures an [Emitter]. Apply via [New].
type Option func(*emitterConfig)

// WithBus injects the bus the emitter publishes on. Required.
// Construction without WithBus returns [ErrEmitterMissingBus].
func WithBus(b bus.Bus) Option {
	return func(c *emitterConfig) { c.bus = b }
}

// WithRedactor injects the redactor used to scrub Args/Flags in
// ModeFull. Required when the package-global mode resolves to
// [ModeFull] at New time; otherwise optional (Anon/Off paths never
// touch the redactor).
func WithRedactor(r *redact.Redactor) Option {
	return func(c *emitterConfig) { c.redactor = r }
}

// WithTopicPrefix overrides the default "kit.telemetry.event" 3-segment
// prefix. Adopters pass "<app>.telemetry.event" so their events land on
// "<app>.telemetry.event.recorded" rather than the kit-owned topic.
// Empty input restores the default.
func WithTopicPrefix(prefix string) Option {
	return func(c *emitterConfig) {
		if prefix != "" {
			c.topicPrefix = prefix
		}
	}
}

// WithKitVersion sets the kit_version field stamped on every emitted
// Event. Build systems inject this via -ldflags so events identify the
// shipped kit binary.
func WithKitVersion(v string) Option {
	return func(c *emitterConfig) { c.kitVersion = v }
}

// WithSDKVersion sets the sdk_version field stamped on every emitted
// Event. Defaults to the kit module version discovered from runtime
// build info; pass explicitly when the build embeds a custom string.
func WithSDKVersion(v string) Option {
	return func(c *emitterConfig) { c.sdkVersion = v }
}

// withInstallIDFunc replaces the InstallationID resolver. UNEXPORTED on
// purpose: external callers should never bypass the real on-disk file.
// Internal tests use it to drive the success and failure branches of
// Record without touching the XDG state dir.
func withInstallIDFunc(fn installIDFunc) Option {
	return func(c *emitterConfig) { c.installID = fn }
}

// withLogger overrides the slog logger. Unexported — production paths
// use slog.Default(); tests use this to capture soft-refusal warnings.
func withLogger(l *slog.Logger) Option {
	return func(c *emitterConfig) { c.logger = l }
}

// New constructs an [Emitter]. Validates the configuration and returns
// an error if the bus is missing or if the global mode is [ModeFull]
// without a redactor. Returns nil-error on the common Off/Anon paths
// even when no redactor is supplied.
func New(opts ...Option) (*Emitter, error) {
	cfg := emitterConfig{
		topicPrefix: defaultTopicPrefix,
		installID:   InstallationID,
		logger:      slog.Default(),
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.bus == nil {
		return nil, ErrEmitterMissingBus
	}
	if cfg.redactor == nil && CurrentMode() == ModeFull {
		return nil, ErrEmitterMissingRedactor
	}
	if cfg.sdkVersion == "" {
		cfg.sdkVersion = discoverSDKVersion()
	}
	return &Emitter{
		bus:         cfg.bus,
		redactor:    cfg.redactor,
		topicPrefix: cfg.topicPrefix,
		kitVersion:  cfg.kitVersion,
		sdkVersion:  cfg.sdkVersion,
		installIDFn: cfg.installID,
		logger:      cfg.logger,
	}, nil
}

// Record publishes ev on the configured bus topic. Soft-refuses (returns
// nil with no publish) when:
//
//   - Mode resolves to [ModeOff] (zero-cost short-circuit).
//   - The active [ConsentHook] denies emission.
//   - The installation_id cannot be persisted/read.
//   - The event fails [Event.Validate] after stamping.
//
// Returns a non-nil error only when the underlying bus publish itself
// fails. Callers therefore CANNOT distinguish "telemetry off" from
// "telemetry succeeded" by the return value — that ambiguity is
// intentional (ADR-0035 #2: a privacy-respecting emitter exposes no
// channel by which a consumer can detect mode).
func (e *Emitter) Record(ctx context.Context, ev Event) error {
	// Step 1: zero-cost short-circuit on Mode=Off. CurrentModeFromContext
	// reads an atomic.Int32 (after the one-shot env read). No allocations
	// occur on this path: the bench guards <= 0 B/op.
	mode := CurrentModeFromContext(ctx)
	if mode == ModeOff {
		return nil
	}

	// Step 2: consent gate. Default-deny + per-context override; soft
	// refusal returns nil so the caller cannot distinguish "denied" from
	// "off".
	if !CurrentConsentHookFromContext(ctx).Granted(ctx) {
		return nil
	}

	// Step 3: stamp authoritative envelope fields. Callers fill the
	// behaviour-bearing fields (CommandPath, ExitCode, DurationMS,
	// Args/Flags, TraceID); everything that identifies the schema,
	// install, or build is stamped here so producers cannot drift.
	ev.SchemaVersion = SchemaVersion
	ev.SDKLang = SDKLang
	ev.SDKVersion = e.sdkVersion
	ev.KitVersion = e.kitVersion
	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = time.Now().UTC()
	}
	ev.Mode = modeString(mode)

	// installation_id is fetched on first use and cached on the emitter.
	// A read/write failure soft-refuses: emitting with a synthetic or
	// missing id would corrupt the cross-language identifier contract.
	id, err := e.installID()
	if err != nil {
		e.logger.WarnContext(ctx, "telemetry: installation_id lookup failed; dropping event",
			slog.String("err", err.Error()))
		return nil
	}
	ev.InstallationID = id

	// Step 4: Anon mode strips Args/Flags defensively. ADR-0035 #6:
	// even a well-meaning caller can populate them; we MUST refuse to
	// emit anything beyond the anon payload at this tier.
	if mode == ModeAnon {
		ev.Args = nil
		ev.Flags = nil
	}

	// Step 5: Full mode runs redact. redactEvent is the in-package
	// helper (see redactor.go); it consults CurrentRedactObserver for
	// audit-side observation and returns a value-copy with rewritten
	// Args/Flags.
	//
	// Runtime defence-in-depth: New() rejects Full-without-redactor at
	// construction, but an adopter who flips mode per-context via
	// WithMode(ctx, ModeFull) can reach this branch with e.redactor==nil
	// (the New check saw Off/Anon at startup). Soft-refuse rather than
	// nil-deref — calling redactEvent with a nil redactor would panic
	// and crash the host program, violating the non-blocking contract.
	if mode == ModeFull {
		if e.redactor == nil {
			e.logger.WarnContext(ctx, "telemetry: Full mode requested but no redactor configured; refusing",
				slog.String("command_path", joinCommandPath(ev.CommandPath)))
			return nil
		}
		ev = redactEvent(e.redactor, ev)
	}

	// Step 6: validate. Catches producer bugs (zero CommandPath, etc).
	// Soft-refuse: a malformed event must not crash the host program.
	if err := ev.Validate(); err != nil {
		e.logger.WarnContext(ctx, "telemetry: event failed validation; dropping",
			slog.String("err", err.Error()),
			slog.String("command_path", joinCommandPath(ev.CommandPath)))
		return nil
	}

	// Step 7: publish. Bus error bubbles up — it represents bus state
	// (closed, network adapter down) that the caller should see.
	return e.publish(ctx, ev)
}

// publish composes the bus envelope and forwards to the wired bus. Topic
// is "<prefix>.recorded"; Source identifies kit-telemetry as the
// publisher. The Event payload is passed by value — bus.NewEvent stamps
// the timestamp on the envelope (separate from Event.OccurredAt, which
// the emitter stamped above).
func (e *Emitter) publish(ctx context.Context, ev Event) error {
	topic := bus.Topic(e.topicPrefix + "." + recordedAction)
	return e.bus.Publish(ctx, bus.NewEvent(topic, busSource, ev))
}

// installID returns the cached installation_id, resolving it on first
// use. Cache is per-emitter so each emitter records the id observed at
// its first emit; production wiring uses one emitter per process so the
// distinction is academic.
func (e *Emitter) installID() (string, error) {
	e.idMu.Lock()
	defer e.idMu.Unlock()
	if e.idCacheOK {
		return e.idCached, nil
	}
	id, err := e.installIDFn()
	if err != nil {
		return "", err
	}
	e.idCached = id
	e.idCacheOK = true
	return id, nil
}

// modeString renders a [Mode] as the wire-format token used in
// Event.Mode. Only "anon" and "full" are emittable; ModeOff returns "off"
// for completeness but Record short-circuits before it ever reaches
// stamping, so the wire never sees that value.
func modeString(m Mode) string {
	switch m {
	case ModeAnon:
		return "anon"
	case ModeFull:
		return "full"
	default:
		return "off"
	}
}

// joinCommandPath renders the command path for log messages without an
// allocation when empty. Used only in the soft-refusal log line, so the
// micro-cost is amortised against the slog call itself.
func joinCommandPath(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out = out + " " + p
	}
	return out
}

// discoverSDKVersion attempts to read the kit module version from the
// runtime build info. Empty result when running outside a module build
// (e.g. `go test` in some configurations) — callers that need a stable
// version string set WithSDKVersion explicitly at build time.
func discoverSDKVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, m := range info.Deps {
		if m == nil {
			continue
		}
		if m.Path == "hop.top/kit" {
			return m.Version
		}
	}
	// The main module is the kit when tests run in-tree.
	if info.Main.Path == "hop.top/kit" {
		return info.Main.Version
	}
	return ""
}

// Compile-time assertion that emitterConfig stays usable as the Option
// target. Cheap insurance against an accidental future refactor that
// changes the Option signature without touching the helpers above.
var _ = func() Option { return func(*emitterConfig) {} }
