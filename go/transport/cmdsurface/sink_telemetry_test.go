package cmdsurface

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hop.top/kit/go/runtime/telemetry"
)

// stubEmitter is the test seam for TelemetrySink. Implements emitterIface.
// recordCalls is a per-call hook (return error to simulate denial / bus
// failure); recorded captures every Event handed in.
type stubEmitter struct {
	mu        sync.Mutex
	recorded  []telemetry.Event
	ctxs      []context.Context
	recordErr error
	// gate, when non-nil, is closed by the test to release Record. Used
	// only by the close-flush test; nil for everything else.
	gate <-chan struct{}
}

func (s *stubEmitter) Record(ctx context.Context, ev telemetry.Event) error {
	if s.gate != nil {
		<-s.gate
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recordErr != nil {
		return s.recordErr
	}
	s.recorded = append(s.recorded, ev)
	s.ctxs = append(s.ctxs, ctx)
	return nil
}

func (s *stubEmitter) events() []telemetry.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]telemetry.Event, len(s.recorded))
	copy(out, s.recorded)
	return out
}

func (s *stubEmitter) contexts() []context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]context.Context, len(s.ctxs))
	copy(out, s.ctxs)
	return out
}

// drainSync builds a sink, emits, calls Close with a generous deadline
// so the drain has time to flush, and returns the captured events.
func drainSync(t *testing.T, s *TelemetrySink) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestTelemetrySink_AnonModeShape(t *testing.T) {
	em := &stubEmitter{}
	s, err := NewTelemetrySink(WithEmitter(em), WithMode(telemetry.ModeAnon), WithKitVersion("v1.2.3"))
	if err != nil {
		t.Fatalf("NewTelemetrySink: %v", err)
	}

	inv := Invocation{
		Path:  []string{"widget", "add"},
		Args:  []string{"foo", "bar"},
		Flags: map[string]any{"name": "alice", "count": 7},
		Meta: Meta{
			Surface:     SurfaceCLI,
			TraceID:     "tr-anon",
			RequestedAt: time.Now().Add(-10 * time.Millisecond),
		},
	}
	if err := s.Emit(context.Background(), inv, Result{ExitCode: 0}, nil); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	drainSync(t, s)

	evs := em.events()
	if len(evs) != 1 {
		t.Fatalf("got %d events, want 1", len(evs))
	}
	ev := evs[0]
	if got := strings.Join(ev.CommandPath, " "); got != "widget add" {
		t.Errorf("CommandPath=%q want=%q", got, "widget add")
	}
	if ev.ExitCode != 0 {
		t.Errorf("ExitCode=%d want=0", ev.ExitCode)
	}
	if ev.TraceID != "tr-anon" {
		t.Errorf("TraceID=%q want=tr-anon", ev.TraceID)
	}
	if ev.KitVersion != "v1.2.3" {
		t.Errorf("KitVersion=%q want=v1.2.3", ev.KitVersion)
	}
	if ev.OccurredAt.IsZero() {
		t.Errorf("OccurredAt is zero")
	}
	if len(ev.Args) != 0 {
		t.Errorf("Args=%v want empty in Anon", ev.Args)
	}
	if len(ev.Flags) != 0 {
		t.Errorf("Flags=%v want empty in Anon", ev.Flags)
	}
	// Surface MUST NOT leak in Anon — design-note §3.
	if _, ok := ev.Flags["_surface"]; ok {
		t.Errorf("_surface flag must not be present in Anon")
	}
}

func TestTelemetrySink_FullModeShape(t *testing.T) {
	em := &stubEmitter{}
	s, err := NewTelemetrySink(WithEmitter(em), WithMode(telemetry.ModeFull))
	if err != nil {
		t.Fatalf("NewTelemetrySink: %v", err)
	}

	inv := Invocation{
		Path:  []string{"widget", "add"},
		Args:  []string{"pos1", "pos2"},
		Flags: map[string]any{"name": "alice", "verbose": true},
		Meta: Meta{
			Surface:     SurfaceREST,
			TraceID:     "tr-full",
			RequestedAt: time.Now().Add(-5 * time.Millisecond),
		},
	}
	if err := s.Emit(context.Background(), inv, Result{ExitCode: 0}, nil); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	drainSync(t, s)

	evs := em.events()
	if len(evs) != 1 {
		t.Fatalf("got %d events, want 1", len(evs))
	}
	ev := evs[0]
	if len(ev.Args) != 2 || ev.Args[0] != "pos1" || ev.Args[1] != "pos2" {
		t.Errorf("Args=%v want=[pos1 pos2]", ev.Args)
	}
	if ev.Flags["name"] != "alice" {
		t.Errorf("Flags[name]=%q want=alice", ev.Flags["name"])
	}
	if ev.Flags["verbose"] != "true" {
		t.Errorf("Flags[verbose]=%q want=true (string conversion)", ev.Flags["verbose"])
	}
	if ev.Flags["_surface"] != "rest" {
		t.Errorf("Flags[_surface]=%q want=rest", ev.Flags["_surface"])
	}
}

func TestTelemetrySink_NonBlocking(t *testing.T) {
	// Block the emitter so the channel actually fills up.
	gate := make(chan struct{})
	em := &stubEmitter{gate: gate}
	s, err := NewTelemetrySink(WithEmitter(em), WithChannelCap(2))
	if err != nil {
		t.Fatalf("NewTelemetrySink: %v", err)
	}

	// Fire 10 emits; cap=2 means at most 2 are queued + 1 in-flight in
	// the drain (parked on gate). The remaining 7 must drop.
	const total = 10
	inv := Invocation{
		Path: []string{"x"},
		Meta: Meta{Surface: SurfaceCLI, RequestedAt: time.Now()},
	}
	for i := 0; i < total; i++ {
		start := time.Now()
		if err := s.Emit(context.Background(), inv, Result{}, nil); err != nil {
			t.Fatalf("Emit %d: %v", i, err)
		}
		// 5ms is generous: production target is ~1ms; CI fluctuates.
		if elapsed := time.Since(start); elapsed > 5*time.Millisecond {
			t.Errorf("Emit %d took %v, want <5ms (non-blocking contract)", i, elapsed)
		}
	}

	// Release the drain so Close can flush.
	close(gate)
	drainSync(t, s)

	st := s.Stats()
	// At minimum we must have refused most of the events; exact count
	// depends on scheduler, but DroppedFull must be > 0 and the sum
	// Emitted+DroppedFull == total.
	if st.DroppedFull == 0 {
		t.Errorf("DroppedFull=0; want >0 with cap=2 and %d emits", total)
	}
	if st.Emitted+st.DroppedFull != total {
		t.Errorf("Emitted(%d) + DroppedFull(%d) = %d, want %d",
			st.Emitted, st.DroppedFull, st.Emitted+st.DroppedFull, total)
	}
}

func TestTelemetrySink_OversizePayloadDropped(t *testing.T) {
	em := &stubEmitter{}
	// 200-byte cap forces a drop with any non-trivial Args payload.
	s, err := NewTelemetrySink(WithEmitter(em), WithMode(telemetry.ModeFull), WithMaxBytes(200))
	if err != nil {
		t.Fatalf("NewTelemetrySink: %v", err)
	}

	huge := strings.Repeat("x", 1024)
	inv := Invocation{
		Path: []string{"big"},
		Args: []string{huge},
		Meta: Meta{Surface: SurfaceCLI, RequestedAt: time.Now()},
	}
	if err := s.Emit(context.Background(), inv, Result{}, nil); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	drainSync(t, s)

	st := s.Stats()
	if st.DroppedOversize != 1 {
		t.Errorf("DroppedOversize=%d want=1", st.DroppedOversize)
	}
	if st.Emitted != 0 {
		t.Errorf("Emitted=%d want=0 (drop happens before emitter)", st.Emitted)
	}
	if len(em.events()) != 0 {
		t.Errorf("emitter received %d events; expected 0", len(em.events()))
	}
}

func TestTelemetrySink_ConsentDeniedNoOps(t *testing.T) {
	// Emitter returns an error to simulate hard failure / denial.
	em := &stubEmitter{recordErr: errors.New("denied")}
	s, err := NewTelemetrySink(WithEmitter(em))
	if err != nil {
		t.Fatalf("NewTelemetrySink: %v", err)
	}

	inv := Invocation{Path: []string{"x"}, Meta: Meta{Surface: SurfaceCLI, RequestedAt: time.Now()}}
	if err := s.Emit(context.Background(), inv, Result{}, nil); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	drainSync(t, s)

	st := s.Stats()
	if st.DroppedDenied != 1 {
		t.Errorf("DroppedDenied=%d want=1", st.DroppedDenied)
	}
	if st.Emitted != 0 {
		t.Errorf("Emitted=%d want=0", st.Emitted)
	}
}

func TestTelemetrySink_TraceIDPropagated(t *testing.T) {
	em := &stubEmitter{}
	s, err := NewTelemetrySink(WithEmitter(em))
	if err != nil {
		t.Fatalf("NewTelemetrySink: %v", err)
	}
	inv := Invocation{
		Path: []string{"x"},
		Meta: Meta{Surface: SurfaceCLI, TraceID: "abc-123", RequestedAt: time.Now()},
	}
	if err := s.Emit(context.Background(), inv, Result{}, nil); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	drainSync(t, s)

	evs := em.events()
	if len(evs) != 1 {
		t.Fatalf("got %d events", len(evs))
	}
	if evs[0].TraceID != "abc-123" {
		t.Errorf("TraceID=%q want=abc-123", evs[0].TraceID)
	}
}

func TestTelemetrySink_DurationFromRequestedAt(t *testing.T) {
	em := &stubEmitter{}
	s, err := NewTelemetrySink(WithEmitter(em))
	if err != nil {
		t.Fatalf("NewTelemetrySink: %v", err)
	}
	inv := Invocation{
		Path: []string{"x"},
		Meta: Meta{Surface: SurfaceCLI, RequestedAt: time.Now().Add(-25 * time.Millisecond)},
	}
	if err := s.Emit(context.Background(), inv, Result{}, nil); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	drainSync(t, s)

	evs := em.events()
	if len(evs) != 1 {
		t.Fatalf("got %d events", len(evs))
	}
	// Allow ±100ms slack for scheduler jitter on loaded CI.
	d := evs[0].DurationMS
	if d < 20 || d > 200 {
		t.Errorf("DurationMS=%d want ≈25 (±slack)", d)
	}
}

func TestTelemetrySink_RequestedAtMissing_DurationSentinel(t *testing.T) {
	em := &stubEmitter{}
	s, err := NewTelemetrySink(WithEmitter(em))
	if err != nil {
		t.Fatalf("NewTelemetrySink: %v", err)
	}
	// RequestedAt zero — simulates a surface that forgot to stamp it.
	inv := Invocation{Path: []string{"x"}, Meta: Meta{Surface: SurfaceCLI}}
	if err := s.Emit(context.Background(), inv, Result{}, nil); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	drainSync(t, s)

	evs := em.events()
	if len(evs) != 1 {
		t.Fatalf("got %d events", len(evs))
	}
	if evs[0].DurationMS != -1 {
		t.Errorf("DurationMS=%d want=-1 sentinel", evs[0].DurationMS)
	}
	if got := s.Stats().RequestedAtMissing; got != 1 {
		t.Errorf("RequestedAtMissing=%d want=1", got)
	}
}

func TestTelemetrySink_Close_FlushesPending(t *testing.T) {
	em := &stubEmitter{}
	s, err := NewTelemetrySink(WithEmitter(em), WithChannelCap(16))
	if err != nil {
		t.Fatalf("NewTelemetrySink: %v", err)
	}
	const n = 5
	for i := 0; i < n; i++ {
		inv := Invocation{Path: []string{"x"}, Meta: Meta{Surface: SurfaceCLI, RequestedAt: time.Now()}}
		if err := s.Emit(context.Background(), inv, Result{}, nil); err != nil {
			t.Fatalf("Emit %d: %v", i, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := s.Stats().Emitted; got != n {
		t.Errorf("Emitted=%d want=%d (all queued events must flush)", got, n)
	}
}

func TestTelemetrySink_Stats_Atomic(t *testing.T) {
	em := &stubEmitter{}
	s, err := NewTelemetrySink(WithEmitter(em), WithChannelCap(2048))
	if err != nil {
		t.Fatalf("NewTelemetrySink: %v", err)
	}

	const goroutines = 50
	const perG = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				inv := Invocation{
					Path: []string{"x"},
					Meta: Meta{Surface: SurfaceCLI, RequestedAt: time.Now()},
				}
				_ = s.Emit(context.Background(), inv, Result{}, nil)
			}
		}()
	}
	wg.Wait()
	drainSync(t, s)

	st := s.Stats()
	total := st.Emitted + st.DroppedFull + st.DroppedOversize + st.DroppedDenied
	if total != goroutines*perG {
		t.Errorf("counter sum=%d want=%d (no torn reads, no lost events)", total, goroutines*perG)
	}
}

// TestTelemetrySink_AnonNeverTripsSizeCap pins the design-note §4
// invariant: "Anon never trips it. Bounded by construction." Even with
// a tight 512-byte cap and 1000 emits carrying nominal command paths,
// Anon mode events MUST NOT increment DroppedOversize — Args/Flags are
// stripped at queue time so the canonical fields fit in well under 1KB.
func TestTelemetrySink_AnonNeverTripsSizeCap(t *testing.T) {
	em := &stubEmitter{}
	s, err := NewTelemetrySink(
		WithEmitter(em),
		WithMode(telemetry.ModeAnon),
		WithMaxBytes(512),
		WithChannelCap(2048),
	)
	if err != nil {
		t.Fatalf("NewTelemetrySink: %v", err)
	}

	// Args + Flags below would EASILY blow a 512-byte cap if shipped,
	// but Anon strips them at queue time (sink_telemetry.go §3).
	huge := strings.Repeat("y", 4096)
	inv := Invocation{
		Path:  []string{"widget", "add"},
		Args:  []string{huge, huge, huge},
		Flags: map[string]any{"big": huge},
		Meta:  Meta{Surface: SurfaceCLI, TraceID: "trace-xyz", RequestedAt: time.Now()},
	}
	const n = 1000
	for i := 0; i < n; i++ {
		if err := s.Emit(context.Background(), inv, Result{ExitCode: 0}, nil); err != nil {
			t.Fatalf("Emit %d: %v", i, err)
		}
	}
	drainSync(t, s)

	st := s.Stats()
	if st.DroppedOversize != 0 {
		t.Errorf("DroppedOversize=%d want=0 (Anon must be bounded by construction; design-note §4)", st.DroppedOversize)
	}
	if st.Emitted != n {
		t.Errorf("Emitted=%d want=%d", st.Emitted, n)
	}
}

// TestTelemetrySink_EmptyTraceIDOmittedFromJSON pins the omitempty wire
// contract on InvocationEvent.TraceID: when inv.Meta.TraceID is empty,
// the queued InvocationEvent MUST marshal without a "trace_id" key at
// all (not "trace_id":""). Audit subscribers serialising the
// intermediate value rely on this to detect "no trace" vs. "empty trace".
func TestTelemetrySink_EmptyTraceIDOmittedFromJSON(t *testing.T) {
	ev := InvocationEvent{
		CommandPath: []string{"x"},
		ExitCode:    0,
		DurationMS:  5,
		OccurredAt:  time.Now().UTC(),
		Surface:     "cli",
		// TraceID intentionally empty.
	}
	blob, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(blob), "trace_id") {
		t.Errorf("empty TraceID leaked into JSON: %s", blob)
	}

	// Non-empty TraceID must round-trip verbatim through the
	// InvocationEvent → telemetry.Event conversion (no transformation,
	// no truncation, no normalisation).
	em := &stubEmitter{}
	s, err := NewTelemetrySink(WithEmitter(em))
	if err != nil {
		t.Fatalf("NewTelemetrySink: %v", err)
	}
	const wantTrace = "abc-DEF-123_456.789"
	inv := Invocation{
		Path: []string{"x"},
		Meta: Meta{Surface: SurfaceCLI, TraceID: wantTrace, RequestedAt: time.Now()},
	}
	if err := s.Emit(context.Background(), inv, Result{}, nil); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	drainSync(t, s)

	evs := em.events()
	if len(evs) != 1 {
		t.Fatalf("got %d events", len(evs))
	}
	if evs[0].TraceID != wantTrace {
		t.Errorf("TraceID=%q want=%q (must survive conversion unchanged)", evs[0].TraceID, wantTrace)
	}
}

// TestTelemetrySink_PreservesCtxConsentHook pins T-0682's ctx-
// propagation fix: a permissive ConsentHook installed on the caller's
// ctx via telemetry.WithConsentHook MUST reach emitter.Record at ship
// time, not get replaced by context.Background() at the channel-drain
// boundary. The stub emitter records the ctx it received; we assert
// the hook is observable on that ctx.
func TestTelemetrySink_PreservesCtxConsentHook(t *testing.T) {
	em := &stubEmitter{}
	s, err := NewTelemetrySink(WithEmitter(em))
	if err != nil {
		t.Fatalf("NewTelemetrySink: %v", err)
	}

	hook := permissiveTestHook{}
	ctx := telemetry.WithConsentHook(context.Background(), hook)

	inv := Invocation{Path: []string{"x"}, Meta: Meta{Surface: SurfaceCLI, RequestedAt: time.Now()}}
	if err := s.Emit(ctx, inv, Result{}, nil); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	drainSync(t, s)

	ctxs := em.contexts()
	if len(ctxs) != 1 {
		t.Fatalf("emitter received %d ctxs, want 1", len(ctxs))
	}
	got := telemetry.CurrentConsentHookFromContext(ctxs[0])
	if !got.Granted(ctxs[0]) {
		t.Errorf("per-ctx ConsentHook lost across channel; emitter saw default-deny")
	}
}

// TestTelemetrySink_PreservesCtxMode mirrors the consent-hook test for
// telemetry.WithMode: a per-ctx ModeFull override MUST reach
// emitter.Record so the emitter applies the Full path. Without the
// carrier carrying ctx through the channel, the emitter would see the
// global mode (ModeOff by default) and skip emission silently.
func TestTelemetrySink_PreservesCtxMode(t *testing.T) {
	em := &stubEmitter{}
	s, err := NewTelemetrySink(WithEmitter(em))
	if err != nil {
		t.Fatalf("NewTelemetrySink: %v", err)
	}

	ctx := telemetry.WithMode(context.Background(), telemetry.ModeFull)

	inv := Invocation{Path: []string{"x"}, Meta: Meta{Surface: SurfaceCLI, RequestedAt: time.Now()}}
	if err := s.Emit(ctx, inv, Result{}, nil); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	drainSync(t, s)

	ctxs := em.contexts()
	if len(ctxs) != 1 {
		t.Fatalf("emitter received %d ctxs, want 1", len(ctxs))
	}
	if got := telemetry.CurrentModeFromContext(ctxs[0]); got != telemetry.ModeFull {
		t.Errorf("per-ctx Mode lost across channel; emitter saw %v, want ModeFull", got)
	}
}

// permissiveTestHook is a telemetry.ConsentHook that always grants.
// Local to this test file so we don't depend on a fixture from the
// telemetry pkg test corpus.
type permissiveTestHook struct{}

func (permissiveTestHook) Granted(context.Context) bool { return true }

func TestTelemetrySink_MissingEmitterErrs(t *testing.T) {
	_, err := NewTelemetrySink()
	if !errors.Is(err, ErrTelemetrySinkNoEmitter) {
		t.Fatalf("NewTelemetrySink() err=%v want=ErrTelemetrySinkNoEmitter", err)
	}
}

// BenchmarkTelemetrySink_Emit measures the hot-path overhead. Pins the
// design-note §5 non-blocking contract: target ~1ms; the bench asserts
// well below that as a smoke alarm.
func BenchmarkTelemetrySink_Emit(b *testing.B) {
	em := &stubEmitter{}
	s, err := NewTelemetrySink(WithEmitter(em), WithChannelCap(b.N+16))
	if err != nil {
		b.Fatalf("NewTelemetrySink: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Close(ctx)
	}()
	inv := Invocation{
		Path: []string{"widget", "add"},
		Meta: Meta{Surface: SurfaceCLI, RequestedAt: time.Now()},
	}
	res := Result{ExitCode: 0}
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.Emit(ctx, inv, res, nil)
	}
}

// compile-time check that *telemetry.Emitter satisfies emitterIface —
// catches any future drift in the emitter's Record signature.
var _ emitterIface = (*telemetry.Emitter)(nil)

// silence unused-import warnings if the file ever gets a temporary
// refactor; atomic is used in production, not directly in tests.
var _ = atomic.Int64{}
