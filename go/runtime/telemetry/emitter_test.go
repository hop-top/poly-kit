package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hop.top/kit/go/core/redact"
	"hop.top/kit/go/runtime/bus"
)

// permissiveHook is a ConsentHook that always grants emission. Used in
// tests that need to reach steps past the consent gate without touching
// the global default-deny hook.
type permissiveHook struct{}

func (permissiveHook) Granted(context.Context) bool { return true }

// recordingBus is a minimal bus.Bus implementation that captures every
// Publish call. It does NOT enforce topic validation or run handlers;
// the emitter contract only requires that Publish be called with the
// right Topic/Source/payload.
type recordingBus struct {
	mu     sync.Mutex
	events []bus.Event
	calls  int
	err    error // optional injected error
}

func (r *recordingBus) Publish(ctx context.Context, e bus.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.events = append(r.events, e)
	return r.err
}

func (r *recordingBus) Subscribe(string, bus.Handler) bus.Unsubscribe {
	return func() {}
}

func (r *recordingBus) SubscribeAsync(string, bus.AsyncHandler) bus.Unsubscribe {
	return func() {}
}

func (r *recordingBus) Close(context.Context) error { return nil }

func (r *recordingBus) Calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func (r *recordingBus) Last() (bus.Event, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.events) == 0 {
		return bus.Event{}, false
	}
	return r.events[len(r.events)-1], true
}

// stableInstallID returns a known-good 64-char hex digest so emitter
// tests don't depend on the on-disk XDG state file.
func stableInstallID() installIDFunc {
	return func() (string, error) {
		return validInstallIDHex, nil
	}
}

// failingInstallID simulates an install_id resolver failure.
func failingInstallID(err error) installIDFunc {
	return func() (string, error) {
		return "", err
	}
}

// withMode is a test helper that swaps the package-global Mode for the
// duration of a sub-test and restores it via t.Cleanup. We avoid
// touching the per-context override because some tests need to assert
// the global path explicitly.
func withGlobalMode(t *testing.T, m Mode) {
	t.Helper()
	prev := Mode(globalMode.Load())
	prevEnvApplied := envModeApplied.Load()
	SetMode(m)
	t.Cleanup(func() {
		globalMode.Store(int32(prev))
		envModeApplied.Store(prevEnvApplied)
	})
}

// silentLogger discards all log output. Used in tests that assert
// soft-refusal paths without polluting `go test -v` output.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// capturingLogger returns a logger plus a buffer holding its output, so
// soft-refusal tests can assert "the warning fired".
func capturingLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

// stubRedactor builds an empty redactor with one rule that matches the
// literal "SECRET" so tests can assert Full-tier redaction happens.
// redact.Default() panics on zero rules; this avoids hitting the
// production gitleaks corpus from tests.
func stubRedactor(t *testing.T) *redact.Redactor {
	t.Helper()
	r, err := redact.New().AddRule("test-secret", "SECRET", "<REDACTED>")
	if err != nil {
		t.Fatalf("build stub redactor: %v", err)
	}
	return r
}

func TestNew_RequiresBus(t *testing.T) {
	withGlobalMode(t, ModeOff)
	if _, err := New(); !errors.Is(err, ErrEmitterMissingBus) {
		t.Fatalf("New() err = %v, want ErrEmitterMissingBus", err)
	}
}

func TestNew_FullModeNeedsRedactor(t *testing.T) {
	withGlobalMode(t, ModeFull)
	b := &recordingBus{}
	if _, err := New(WithBus(b)); !errors.Is(err, ErrEmitterMissingRedactor) {
		t.Fatalf("New() err = %v, want ErrEmitterMissingRedactor", err)
	}
	// And succeeds once redactor is provided.
	if _, err := New(WithBus(b), WithRedactor(stubRedactor(t))); err != nil {
		t.Fatalf("New(WithBus, WithRedactor) err = %v, want nil", err)
	}
}

func TestNew_AnonModeNoRedactorOK(t *testing.T) {
	withGlobalMode(t, ModeAnon)
	b := &recordingBus{}
	if _, err := New(WithBus(b)); err != nil {
		t.Fatalf("New() err = %v, want nil (Anon does not need a redactor)", err)
	}
}

func TestRecord_OffModeNoop(t *testing.T) {
	withGlobalMode(t, ModeOff)
	b := &recordingBus{}
	e, err := New(WithBus(b), withInstallIDFunc(stableInstallID()), withLogger(silentLogger()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := WithConsentHook(context.Background(), permissiveHook{})
	if err := e.Record(ctx, baseEvent()); err != nil {
		t.Fatalf("Record err = %v, want nil", err)
	}
	if got := b.Calls(); got != 0 {
		t.Fatalf("bus.Publish calls = %d, want 0 (Mode=Off must not publish)", got)
	}
}

func TestRecord_ConsentDeniedNoop(t *testing.T) {
	withGlobalMode(t, ModeAnon)
	b := &recordingBus{}
	e, err := New(WithBus(b), withInstallIDFunc(stableInstallID()), withLogger(silentLogger()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Default global hook denies; do not install a permissive override.
	if err := e.Record(context.Background(), baseEvent()); err != nil {
		t.Fatalf("Record err = %v, want nil", err)
	}
	if got := b.Calls(); got != 0 {
		t.Fatalf("bus.Publish calls = %d, want 0 (consent denied)", got)
	}
}

func TestRecord_AnonStripsArgsAndFlags(t *testing.T) {
	withGlobalMode(t, ModeAnon)
	b := &recordingBus{}
	e, err := New(WithBus(b), withInstallIDFunc(stableInstallID()), withLogger(silentLogger()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := WithConsentHook(context.Background(), permissiveHook{})
	in := baseEvent()
	in.Args = []string{"SECRET", "foo"}
	in.Flags = map[string]string{"--token": "SECRET"}
	if err := e.Record(ctx, in); err != nil {
		t.Fatalf("Record: %v", err)
	}
	ev := lastEventPayload(t, b)
	if len(ev.Args) != 0 {
		t.Errorf("Anon emit: Args = %v, want empty", ev.Args)
	}
	if len(ev.Flags) != 0 {
		t.Errorf("Anon emit: Flags = %v, want empty", ev.Flags)
	}
	if ev.Mode != "anon" {
		t.Errorf("Anon emit: Mode = %q, want \"anon\"", ev.Mode)
	}
}

func TestRecord_FullKeepsArgsAndFlagsRedacted(t *testing.T) {
	withGlobalMode(t, ModeFull)
	b := &recordingBus{}
	e, err := New(
		WithBus(b),
		WithRedactor(stubRedactor(t)),
		withInstallIDFunc(stableInstallID()),
		withLogger(silentLogger()),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := WithConsentHook(context.Background(), permissiveHook{})
	in := baseEvent()
	in.Args = []string{"SECRET", "plain"}
	in.Flags = map[string]string{"--token": "value=SECRET"}
	if err := e.Record(ctx, in); err != nil {
		t.Fatalf("Record: %v", err)
	}
	ev := lastEventPayload(t, b)
	if ev.Mode != "full" {
		t.Errorf("Full emit: Mode = %q, want \"full\"", ev.Mode)
	}
	if len(ev.Args) != 2 {
		t.Fatalf("Full emit: Args len = %d, want 2 (%v)", len(ev.Args), ev.Args)
	}
	if strings.Contains(ev.Args[0], "SECRET") {
		t.Errorf("Full emit: Args[0] = %q still contains SECRET (redactor not applied)", ev.Args[0])
	}
	if ev.Args[1] != "plain" {
		t.Errorf("Full emit: Args[1] = %q, want \"plain\"", ev.Args[1])
	}
	if v := ev.Flags["--token"]; strings.Contains(v, "SECRET") {
		t.Errorf("Full emit: Flags[--token] = %q still contains SECRET", v)
	}
}

func TestRecord_StampsCanonicalFields(t *testing.T) {
	withGlobalMode(t, ModeAnon)
	b := &recordingBus{}
	e, err := New(
		WithBus(b),
		withInstallIDFunc(stableInstallID()),
		WithKitVersion("9.9.9-test"),
		WithSDKVersion("sdk-9.9.9"),
		withLogger(silentLogger()),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := WithConsentHook(context.Background(), permissiveHook{})
	// Pass an empty OccurredAt to assert the emitter stamps it.
	in := baseEvent()
	in.OccurredAt = time.Time{}
	if err := e.Record(ctx, in); err != nil {
		t.Fatalf("Record: %v", err)
	}
	ev := lastEventPayload(t, b)
	if ev.SchemaVersion != SchemaVersion {
		t.Errorf("SchemaVersion = %q, want %q", ev.SchemaVersion, SchemaVersion)
	}
	if ev.SDKLang != SDKLang {
		t.Errorf("SDKLang = %q, want %q", ev.SDKLang, SDKLang)
	}
	if ev.SDKVersion != "sdk-9.9.9" {
		t.Errorf("SDKVersion = %q, want \"sdk-9.9.9\"", ev.SDKVersion)
	}
	if ev.KitVersion != "9.9.9-test" {
		t.Errorf("KitVersion = %q, want \"9.9.9-test\"", ev.KitVersion)
	}
	if !validInstallID(ev.InstallationID) {
		t.Errorf("InstallationID = %q, fails validInstallID", ev.InstallationID)
	}
	if ev.OccurredAt.IsZero() {
		t.Errorf("OccurredAt is zero; emitter should have stamped it")
	}
	if ev.Mode != "anon" && ev.Mode != "full" {
		t.Errorf("Mode = %q, want \"anon\" or \"full\"", ev.Mode)
	}
}

func TestRecord_HonoursPerCtxMode(t *testing.T) {
	// Global mode says Anon, but per-context override forces Off → no publish.
	withGlobalMode(t, ModeAnon)
	b := &recordingBus{}
	e, err := New(WithBus(b), withInstallIDFunc(stableInstallID()), withLogger(silentLogger()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := WithMode(WithConsentHook(context.Background(), permissiveHook{}), ModeOff)
	if err := e.Record(ctx, baseEvent()); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if got := b.Calls(); got != 0 {
		t.Fatalf("publish calls = %d, want 0 (per-ctx mode=Off)", got)
	}
}

func TestRecord_HonoursPerCtxConsentHook(t *testing.T) {
	// Global default-deny; per-ctx permissive → publish happens.
	withGlobalMode(t, ModeAnon)
	b := &recordingBus{}
	e, err := New(WithBus(b), withInstallIDFunc(stableInstallID()), withLogger(silentLogger()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := WithConsentHook(context.Background(), permissiveHook{})
	if err := e.Record(ctx, baseEvent()); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if got := b.Calls(); got != 1 {
		t.Fatalf("publish calls = %d, want 1 (per-ctx permissive)", got)
	}
}

func TestRecord_TopicPrefixDefault(t *testing.T) {
	withGlobalMode(t, ModeAnon)
	b := &recordingBus{}
	e, err := New(WithBus(b), withInstallIDFunc(stableInstallID()), withLogger(silentLogger()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := WithConsentHook(context.Background(), permissiveHook{})
	if err := e.Record(ctx, baseEvent()); err != nil {
		t.Fatalf("Record: %v", err)
	}
	ev, ok := b.Last()
	if !ok {
		t.Fatal("no published event")
	}
	wantTopic := bus.Topic("kit.telemetry.event.recorded")
	if ev.Topic != wantTopic {
		t.Fatalf("Topic = %q, want %q", ev.Topic, wantTopic)
	}
	if err := bus.ValidateTopic(ev.Topic); err != nil {
		t.Fatalf("default topic %q fails ValidateTopic: %v", ev.Topic, err)
	}
}

func TestRecord_TopicPrefixAdopter(t *testing.T) {
	withGlobalMode(t, ModeAnon)
	b := &recordingBus{}
	e, err := New(
		WithBus(b),
		WithTopicPrefix("spaced.telemetry.event"),
		withInstallIDFunc(stableInstallID()),
		withLogger(silentLogger()),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := WithConsentHook(context.Background(), permissiveHook{})
	if err := e.Record(ctx, baseEvent()); err != nil {
		t.Fatalf("Record: %v", err)
	}
	ev, _ := b.Last()
	wantTopic := bus.Topic("spaced.telemetry.event.recorded")
	if ev.Topic != wantTopic {
		t.Fatalf("Topic = %q, want %q", ev.Topic, wantTopic)
	}
	if err := bus.ValidateTopic(ev.Topic); err != nil {
		t.Fatalf("adopter topic %q fails ValidateTopic: %v", ev.Topic, err)
	}
}

func TestRecord_BadEventLoggedSoftRefuse(t *testing.T) {
	withGlobalMode(t, ModeAnon)
	b := &recordingBus{}
	logger, buf := capturingLogger()
	e, err := New(WithBus(b), withInstallIDFunc(stableInstallID()), withLogger(logger))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := WithConsentHook(context.Background(), permissiveHook{})
	bad := baseEvent()
	bad.CommandPath = nil // triggers ErrCommandPath in Validate
	if err := e.Record(ctx, bad); err != nil {
		t.Fatalf("Record err = %v, want nil (soft refuse on validate failure)", err)
	}
	if got := b.Calls(); got != 0 {
		t.Fatalf("publish calls = %d, want 0 (validation failed)", got)
	}
	if !strings.Contains(buf.String(), "failed validation") {
		t.Errorf("expected validation warning in log; got: %s", buf.String())
	}
}

func TestRecord_InstallIDFailureSoftRefuse(t *testing.T) {
	withGlobalMode(t, ModeAnon)
	b := &recordingBus{}
	logger, buf := capturingLogger()
	e, err := New(
		WithBus(b),
		withInstallIDFunc(failingInstallID(errors.New("disk full"))),
		withLogger(logger),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := WithConsentHook(context.Background(), permissiveHook{})
	if err := e.Record(ctx, baseEvent()); err != nil {
		t.Fatalf("Record err = %v, want nil (soft refuse on install_id error)", err)
	}
	if got := b.Calls(); got != 0 {
		t.Fatalf("publish calls = %d, want 0 (install_id lookup failed)", got)
	}
	if !strings.Contains(buf.String(), "installation_id") {
		t.Errorf("expected installation_id warning in log; got: %s", buf.String())
	}
}

// TestRecord_FullModeWithNilRedactor_SoftRefuses covers the runtime
// defence-in-depth check: New() guards Full-at-construction, but a
// caller who flips mode per-context via WithMode(ctx, ModeFull) can
// otherwise reach the redactor branch with a nil redactor. The emitter
// must soft-refuse (warn + return nil + zero publishes) rather than
// nil-deref.
func TestRecord_FullModeWithNilRedactor_SoftRefuses(t *testing.T) {
	// Global mode stays at Anon — New's construct-time check sees the
	// global and passes (no WithRedactor required for Anon).
	withGlobalMode(t, ModeAnon)
	b := &recordingBus{}
	logger, buf := capturingLogger()
	e, err := New(WithBus(b), withInstallIDFunc(stableInstallID()), withLogger(logger))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Per-ctx override flips to Full at Record time.
	ctx := WithMode(WithConsentHook(context.Background(), permissiveHook{}), ModeFull)
	if err := e.Record(ctx, baseEvent()); err != nil {
		t.Fatalf("Record err = %v, want nil (soft-refuse on missing redactor)", err)
	}
	if got := b.Calls(); got != 0 {
		t.Fatalf("publish calls = %d, want 0 (Full+nil redactor must refuse)", got)
	}
	if !strings.Contains(buf.String(), "no redactor") {
		t.Errorf("expected redactor warning in log; got: %s", buf.String())
	}
}

func TestRecord_BusErrorBubblesUp(t *testing.T) {
	withGlobalMode(t, ModeAnon)
	want := errors.New("bus is closed")
	b := &recordingBus{err: want}
	e, err := New(WithBus(b), withInstallIDFunc(stableInstallID()), withLogger(silentLogger()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := WithConsentHook(context.Background(), permissiveHook{})
	if got := e.Record(ctx, baseEvent()); !errors.Is(got, want) {
		t.Fatalf("Record err = %v, want %v", got, want)
	}
}

func TestRecord_BusSource(t *testing.T) {
	withGlobalMode(t, ModeAnon)
	b := &recordingBus{}
	e, err := New(WithBus(b), withInstallIDFunc(stableInstallID()), withLogger(silentLogger()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := WithConsentHook(context.Background(), permissiveHook{})
	if err := e.Record(ctx, baseEvent()); err != nil {
		t.Fatalf("Record: %v", err)
	}
	ev, _ := b.Last()
	if ev.Source != busSource {
		t.Fatalf("envelope Source = %q, want %q", ev.Source, busSource)
	}
}

// BenchmarkRecord_OffMode asserts the zero-cost short-circuit promise:
// when CurrentModeFromContext resolves to ModeOff the emitter must avoid
// allocations entirely and stay below ~200ns/op on commodity hardware.
// ADR-0035 #2: every kit binary instantiates this emitter, so any
// per-call cost falls on every adopter.
func BenchmarkRecord_OffMode(b *testing.B) {
	// We need a fresh emitter, but we don't want to flip the package
	// global Mode (other parallel tests rely on it). Instead we force
	// Mode=Off via the per-context override.
	prev := Mode(globalMode.Load())
	prevEnvApplied := envModeApplied.Load()
	SetMode(ModeOff)
	b.Cleanup(func() {
		globalMode.Store(int32(prev))
		envModeApplied.Store(prevEnvApplied)
	})

	bb := &recordingBus{}
	em, err := New(WithBus(bb), withInstallIDFunc(stableInstallID()), withLogger(silentLogger()))
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	ctx := WithMode(context.Background(), ModeOff)
	ev := baseEvent()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = em.Record(ctx, ev)
	}
	// Sanity: the bus must never have been touched.
	if calls := bb.Calls(); calls != 0 {
		b.Fatalf("bench published %d events; Off-mode must publish zero", calls)
	}
}

// baseEvent returns a minimal Event with the caller-owned fields
// populated. The emitter stamps the rest (SchemaVersion, SDKLang,
// SDKVersion, InstallationID, Mode, KitVersion, OccurredAt-if-zero).
func baseEvent() Event {
	return Event{
		CommandPath: []string{"kit", "hop", "list"},
		ExitCode:    0,
		DurationMS:  42,
		OccurredAt:  time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC),
	}
}

// lastEventPayload extracts the Event from the most recent bus.Publish
// call. bus.NewEvent wraps the payload as `any`; in-process subscribers
// receive the original Go value, so a direct type assertion suffices.
func lastEventPayload(t *testing.T, b *recordingBus) Event {
	t.Helper()
	env, ok := b.Last()
	if !ok {
		t.Fatal("no published event")
	}
	ev, ok := env.Payload.(Event)
	if !ok {
		t.Fatalf("payload type = %T, want telemetry.Event", env.Payload)
	}
	return ev
}

// _ keeps the json/atomic imports honest if a future test stops needing
// them. Cheap insurance — atomic is used by withGlobalMode helpers and
// json by future cross-language wire diff tests.
var (
	_ atomic.Int32
	_ = json.Marshal
)
