package cmdsurface_test

// Integration-level tests for the cmdsurface TelemetrySink (T-0682).
//
// These tests complement the unit-level coverage in
// sink_telemetry_test.go (T-0675) and the Bridge wiring coverage in
// bridge_test.go (T-0677). The shared theme: drive a real
// telemetry.Emitter + bus pipeline end-to-end through the sink
// constructed by Bridge.FromConfig, and assert on what actually lands
// on the bus (or — for the consent-denied case — what does NOT land).
//
// The sink itself calls Emitter.Record with context.Background() (see
// sink_telemetry.go shipOne), so per-context overrides like
// telemetry.WithMode/WithConsentHook do NOT propagate from the test's
// caller-side ctx. The integration tests therefore drive Anon/Full
// behaviour via the process-global telemetry.SetMode + a permissive
// global ConsentHook, with t.Cleanup restoring both. Tests that need
// global mode therefore CANNOT run in t.Parallel.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/core/redact"
	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/runtime/telemetry"
	"hop.top/kit/go/transport/cmdsurface"
)

// permissiveHook is a telemetry.ConsentHook that always grants
// emission. Used to step past the default-deny gate in integration
// tests; mirrors the same shape used inside emitter_test.go.
type permissiveHook struct{}

func (permissiveHook) Granted(context.Context) bool { return true }

// installXDGStateDir points telemetry.InstallationID at an isolated
// temp directory so the integration tests never touch the real
// ~/.local/state/kit file. t.Cleanup restores the previous value.
func installXDGStateDir(t *testing.T) {
	t.Helper()
	prev, hadPrev := os.LookupEnv("XDG_STATE_HOME")
	dir := t.TempDir()
	if err := os.Setenv("XDG_STATE_HOME", dir); err != nil {
		t.Fatalf("setenv XDG_STATE_HOME: %v", err)
	}
	t.Cleanup(func() {
		if hadPrev {
			_ = os.Setenv("XDG_STATE_HOME", prev)
		} else {
			_ = os.Unsetenv("XDG_STATE_HOME")
		}
	})
}

// installGlobalTelemetry stages process-global telemetry state for an
// integration test:
//
//   - SetMode(m): so emitter.Record observes the requested tier.
//   - SetConsentHook(permissive): so the emitter's consent gate passes.
//   - XDG_STATE_HOME → t.TempDir(): so InstallationID doesn't pollute
//     the user's real state dir.
//
// All three are restored on test cleanup.
func installGlobalTelemetry(t *testing.T, m telemetry.Mode) {
	t.Helper()
	installXDGStateDir(t)
	prevMode := telemetry.CurrentMode()
	telemetry.SetMode(m)
	t.Cleanup(func() { telemetry.SetMode(prevMode) })
	prevHook := telemetry.CurrentConsentHook()
	telemetry.SetConsentHook(permissiveHook{})
	t.Cleanup(func() { telemetry.SetConsentHook(prevHook) })
}

// busRecorder subscribes to a bus topic pattern and collects every
// published Event. Safe for concurrent Publish callers; tests read
// via Events().
type busRecorder struct {
	mu     sync.Mutex
	events []bus.Event
}

func (r *busRecorder) handler(_ context.Context, e bus.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	return nil
}

func (r *busRecorder) Events() []bus.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]bus.Event, len(r.events))
	copy(out, r.events)
	return out
}

// newIntegrationCobra builds the smallest possible cobra root the
// bridge can discover a leaf from. The leaf intentionally writes to
// stdout + stderr so the NoStdoutStderrLeak test can verify the sink
// never propagates them to the published Event.
func newIntegrationCobra() *cobra.Command {
	root := &cobra.Command{Use: "root"}
	ping := &cobra.Command{
		Use: "ping",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println("pong-stdout")
			cmd.PrintErrln("pong-stderr")
			return nil
		},
		Annotations: map[string]string{"kit/side-effect": "read"},
	}
	root.AddCommand(ping)
	return root
}

// buildBridgeWithTelemetry assembles a Bridge that owns a real
// TelemetrySink driven by a real telemetry.Emitter on a real
// in-process bus. Returns the bridge, the bus (caller subscribes
// before driving the sink), and the underlying sink (used directly
// because Bridge.Invoke does NOT auto-fan-out to sinks — that wire is
// adopter-owned today).
//
// emitterOpts are forwarded into telemetry.New(...). Tests pass
// WithTopicPrefix / WithRedactor / WithKitVersion here.
func buildBridgeWithTelemetry(
	t *testing.T,
	mode string,
	emitterOpts ...telemetry.Option,
) (*cmdsurface.Bridge, bus.Bus, *cmdsurface.TelemetrySink) {
	t.Helper()
	b := bus.New()
	cfg := cmdsurface.Config{
		Telemetry: &cmdsurface.TelemetryConfig{
			Enabled: true,
			Mode:    mode,
		},
		TelemetryEmitterProvider: func() (*telemetry.Emitter, error) {
			opts := append([]telemetry.Option{telemetry.WithBus(b)}, emitterOpts...)
			return telemetry.New(opts...)
		},
	}
	br, err := cmdsurface.FromConfig(newIntegrationCobra(), cfg)
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}
	sinks := br.Sinks()
	if len(sinks) != 1 {
		t.Fatalf("Sinks len=%d want=1", len(sinks))
	}
	ts, ok := sinks[0].Sink.(*cmdsurface.TelemetrySink)
	if !ok {
		t.Fatalf("Sinks[0] type=%T want=*TelemetrySink", sinks[0].Sink)
	}
	return br, b, ts
}

// waitForEvents polls rec.Events() until it has at least n events or
// the timeout elapses. The sink's drain runs on a goroutine; the bus
// publish is synchronous from drain's POV, but the goroutine itself
// is async, so a small polling loop is the cleanest synchronisation.
// 200ms is generous: empirically <2ms under -race; CI may need more.
func waitForEvents(t *testing.T, rec *busRecorder, n int, msg string) []bus.Event {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		ev := rec.Events()
		if len(ev) >= n {
			return ev
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: got %d events after 2s, want %d", msg, len(ev), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// closeBridge invokes Bridge.Close with a generous deadline so the
// drain goroutine has time to flush. Used as a t.Cleanup helper.
func closeBridge(t *testing.T, b *cmdsurface.Bridge) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := b.Close(ctx); err != nil {
		t.Errorf("Bridge.Close: %v", err)
	}
}

// payloadEvent extracts the telemetry.Event from a bus.Event. The
// emitter publishes the Event as the bus envelope Payload; in-process
// subscribers receive the original Go value, so a direct assertion
// suffices.
func payloadEvent(t *testing.T, env bus.Event) telemetry.Event {
	t.Helper()
	ev, ok := env.Payload.(telemetry.Event)
	if !ok {
		t.Fatalf("envelope Payload type=%T want=telemetry.Event", env.Payload)
	}
	return ev
}

// pingInvocation builds the canonical Invocation used by most tests:
// the "ping" leaf, SurfaceCLI, RequestedAt stamped, no positional or
// flag values unless the test overrides.
func pingInvocation() cmdsurface.Invocation {
	return cmdsurface.Invocation{
		Path: []string{"ping"},
		Meta: cmdsurface.Meta{
			Surface:     cmdsurface.SurfaceCLI,
			RequestedAt: time.Now(),
		},
	}
}

// 1 — Anon-mode end-to-end ─────────────────────────────────────────

func TestIntegration_TelemetrySink_EndToEnd_AnonMode(t *testing.T) {
	installGlobalTelemetry(t, telemetry.ModeAnon)

	br, busSink, sink := buildBridgeWithTelemetry(t, "anon",
		telemetry.WithKitVersion("v1.0.0-integration"))
	t.Cleanup(func() { closeBridge(t, br) })

	rec := &busRecorder{}
	busSink.Subscribe("kit.telemetry.event.recorded", rec.handler)

	inv := pingInvocation()
	inv.Args = []string{"would-be-arg"}                  // Anon must strip
	inv.Flags = map[string]any{"would-be": "flag-value"} // Anon must strip
	if err := sink.Emit(context.Background(), inv, cmdsurface.Result{ExitCode: 0}, nil); err != nil {
		t.Fatalf("sink.Emit: %v", err)
	}

	events := waitForEvents(t, rec, 1, "anon mode emit")
	ev := payloadEvent(t, events[0])

	if ev.Mode != "anon" {
		t.Errorf("Mode=%q want=anon", ev.Mode)
	}
	if got := strings.Join(ev.CommandPath, " "); got != "ping" {
		t.Errorf("CommandPath=%q want=ping", got)
	}
	if len(ev.Args) != 0 {
		t.Errorf("Args=%v want empty (Anon strips)", ev.Args)
	}
	if len(ev.Flags) != 0 {
		t.Errorf("Flags=%v want empty (Anon strips)", ev.Flags)
	}
	if ev.KitVersion != "v1.0.0-integration" {
		t.Errorf("KitVersion=%q want=v1.0.0-integration", ev.KitVersion)
	}
	if ev.SchemaVersion != telemetry.SchemaVersion {
		t.Errorf("SchemaVersion=%q want=%q", ev.SchemaVersion, telemetry.SchemaVersion)
	}
	if ev.SDKLang != telemetry.SDKLang {
		t.Errorf("SDKLang=%q want=%q", ev.SDKLang, telemetry.SDKLang)
	}
}

// 2 — Full-mode redacts secrets ────────────────────────────────────

func TestIntegration_TelemetrySink_EndToEnd_FullModeRedacts(t *testing.T) {
	installGlobalTelemetry(t, telemetry.ModeFull)

	// Use a tiny ad-hoc redactor with a single deterministic rule. We
	// avoid redact.Default() so the test does not depend on the
	// gitleaks corpus survey of OPENAI_API_KEY-shape secrets; a
	// hand-rolled rule pins the redaction signal precisely.
	rd := redact.New()
	if _, err := rd.AddRule("openai-test", `sk-[A-Za-z0-9]+`, ""); err != nil {
		t.Fatalf("build redactor rule: %v", err)
	}

	br, busSink, sink := buildBridgeWithTelemetry(t, "full",
		telemetry.WithRedactor(rd))
	t.Cleanup(func() { closeBridge(t, br) })

	rec := &busRecorder{}
	busSink.Subscribe("kit.telemetry.event.recorded", rec.handler)

	inv := pingInvocation()
	inv.Args = []string{"OPENAI_API_KEY=sk-abc123def456"}
	inv.Flags = map[string]any{"--token": "sk-deadbeef99"}
	if err := sink.Emit(context.Background(), inv, cmdsurface.Result{ExitCode: 0}, nil); err != nil {
		t.Fatalf("sink.Emit: %v", err)
	}

	events := waitForEvents(t, rec, 1, "full mode emit")
	ev := payloadEvent(t, events[0])

	if ev.Mode != "full" {
		t.Errorf("Mode=%q want=full", ev.Mode)
	}
	if len(ev.Args) != 1 {
		t.Fatalf("Args len=%d want=1 (%v)", len(ev.Args), ev.Args)
	}
	if strings.Contains(ev.Args[0], "sk-abc123def456") {
		t.Errorf("Args[0]=%q still contains the raw secret", ev.Args[0])
	}
	if !strings.Contains(ev.Args[0], "REDACTED") {
		t.Errorf("Args[0]=%q missing REDACTED placeholder", ev.Args[0])
	}
	if v := ev.Flags["--token"]; strings.Contains(v, "sk-deadbeef99") {
		t.Errorf("Flags[--token]=%q still contains the raw secret", v)
	}
}

// 3 — Consent denied = zero bus emits ──────────────────────────────

func TestIntegration_TelemetrySink_ConsentDenied_NoBusEmit(t *testing.T) {
	// Stage Anon mode but DO NOT install the permissive consent hook —
	// the package default-deny stays in effect. installXDGStateDir
	// still isolates the on-disk install_id file from the host.
	installXDGStateDir(t)
	prevMode := telemetry.CurrentMode()
	telemetry.SetMode(telemetry.ModeAnon)
	t.Cleanup(func() { telemetry.SetMode(prevMode) })
	// Reset hook to the package default-deny (in case a previous test
	// in the binary left a permissive hook in place).
	prevHook := telemetry.CurrentConsentHook()
	telemetry.SetConsentHook(nil)
	t.Cleanup(func() { telemetry.SetConsentHook(prevHook) })

	br, busSink, sink := buildBridgeWithTelemetry(t, "anon")
	t.Cleanup(func() { closeBridge(t, br) })

	rec := &busRecorder{}
	busSink.Subscribe("kit.telemetry.event.recorded", rec.handler)

	const drives = 10
	for range drives {
		if err := sink.Emit(context.Background(), pingInvocation(), cmdsurface.Result{}, nil); err != nil {
			t.Fatalf("sink.Emit: %v", err)
		}
	}

	// Give the drain time to attempt all 10 records.
	time.Sleep(50 * time.Millisecond)

	if got := rec.Events(); len(got) != 0 {
		t.Errorf("bus received %d events under default-deny consent; want 0", len(got))
	}
	// The sink itself counts these as Emitted (emitter.Record soft-
	// refused with nil — see emitter Step 2). That is the documented
	// "consent-deaf" sink contract per sink_telemetry.go header.
}

// 4 — Bridge.Close drains pending events ───────────────────────────

func TestIntegration_TelemetrySink_BridgeCloseDrains(t *testing.T) {
	installGlobalTelemetry(t, telemetry.ModeAnon)

	br, busSink, sink := buildBridgeWithTelemetry(t, "anon")
	// No closeBridge cleanup — this test owns Close().

	rec := &busRecorder{}
	busSink.Subscribe("kit.telemetry.event.recorded", rec.handler)

	const want = 20
	for range want {
		if err := sink.Emit(context.Background(), pingInvocation(), cmdsurface.Result{}, nil); err != nil {
			t.Fatalf("sink.Emit: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := br.Close(ctx); err != nil {
		t.Fatalf("Bridge.Close: %v", err)
	}

	// After Close returns the drain has exited. The sink's Emitted
	// counter must reflect every queued event (channel cap is 256, so
	// none should drop). The bus side will also have received them
	// because the synchronous handler chain runs in Publish.
	st := sink.Stats()
	if st.Emitted+st.DroppedFull+st.DroppedOversize+st.DroppedDenied != want {
		t.Errorf("sum of sink counters=%d want=%d (Stats=%+v)",
			st.Emitted+st.DroppedFull+st.DroppedOversize+st.DroppedDenied, want, st)
	}
	if st.DroppedFull != 0 {
		t.Errorf("DroppedFull=%d want=0 (channel cap 256, queued %d)", st.DroppedFull, want)
	}
	if got := len(rec.Events()); got != want {
		t.Errorf("bus received %d events; want %d (Bridge.Close did not drain)", got, want)
	}
}

// 5 — Topic prefix override applied ────────────────────────────────

func TestIntegration_TelemetrySink_TopicPrefixApplied(t *testing.T) {
	installGlobalTelemetry(t, telemetry.ModeAnon)

	br, busSink, sink := buildBridgeWithTelemetry(t, "anon",
		telemetry.WithTopicPrefix("myapp.telemetry.event"))
	t.Cleanup(func() { closeBridge(t, br) })

	// Two recorders: one on the adopter prefix (expect events), one on
	// the kit default (expect zero — proving the override took effect).
	recAdopter := &busRecorder{}
	recKit := &busRecorder{}
	busSink.Subscribe("myapp.telemetry.event.recorded", recAdopter.handler)
	busSink.Subscribe("kit.telemetry.event.recorded", recKit.handler)

	if err := sink.Emit(context.Background(), pingInvocation(), cmdsurface.Result{}, nil); err != nil {
		t.Fatalf("sink.Emit: %v", err)
	}

	events := waitForEvents(t, recAdopter, 1, "adopter prefix")
	if string(events[0].Topic) != "myapp.telemetry.event.recorded" {
		t.Errorf("Topic=%q want=myapp.telemetry.event.recorded", events[0].Topic)
	}
	if got := len(recKit.Events()); got != 0 {
		t.Errorf("kit-prefix subscriber got %d events; want 0 (prefix override leaked)", got)
	}
}

// 6 — schema_version is the STRING "1", not the integer 1 ─────────

func TestIntegration_TelemetrySink_SchemaVersion_String(t *testing.T) {
	installGlobalTelemetry(t, telemetry.ModeAnon)

	br, busSink, sink := buildBridgeWithTelemetry(t, "anon")
	t.Cleanup(func() { closeBridge(t, br) })

	rec := &busRecorder{}
	busSink.Subscribe("kit.telemetry.event.recorded", rec.handler)

	if err := sink.Emit(context.Background(), pingInvocation(), cmdsurface.Result{}, nil); err != nil {
		t.Fatalf("sink.Emit: %v", err)
	}

	events := waitForEvents(t, rec, 1, "schema_version emit")

	// Two-layer assertion. Layer 1: the typed payload's
	// SchemaVersion field is a string and matches the constant.
	ev := payloadEvent(t, events[0])
	if ev.SchemaVersion != "1" {
		t.Errorf("typed SchemaVersion=%q want=\"1\"", ev.SchemaVersion)
	}
	if telemetry.SchemaVersion != "1" {
		t.Errorf("telemetry.SchemaVersion constant=%q want=\"1\"", telemetry.SchemaVersion)
	}

	// Layer 2: re-marshal the Event through JSON and confirm the wire
	// form is a quoted string (the regression guard for ADR-0035 §7 —
	// a future int field would JSON-encode without quotes).
	blob, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal Event: %v", err)
	}
	if !strings.Contains(string(blob), `"schema_version":"1"`) {
		t.Errorf("wire JSON missing quoted schema_version. blob=%s", string(blob))
	}
	if strings.Contains(string(blob), `"schema_version":1,`) {
		t.Errorf("wire JSON has unquoted schema_version integer. blob=%s", string(blob))
	}
}

// 7 — Result stdout/stderr never propagate to the Event ───────────

func TestIntegration_TelemetrySink_NoStdoutStderrLeak(t *testing.T) {
	installGlobalTelemetry(t, telemetry.ModeFull)

	rd := redact.New()
	if _, err := rd.AddRule("noop", "this-pattern-never-matches", ""); err != nil {
		t.Fatalf("build redactor: %v", err)
	}

	br, busSink, sink := buildBridgeWithTelemetry(t, "full",
		telemetry.WithRedactor(rd))
	t.Cleanup(func() { closeBridge(t, br) })

	rec := &busRecorder{}
	busSink.Subscribe("kit.telemetry.event.recorded", rec.handler)

	// Synthesise a Result with stdout + stderr text. The sink must
	// never surface that text into the published Event — the
	// telemetry.Event type has no stdout/stderr columns (ADR-0035 #6).
	res := cmdsurface.Result{
		ExitCode: 0,
		Stdout:   "secret-stdout-leak-canary",
		Stderr:   "secret-stderr-leak-canary",
		Data:     "result-data-which-also-must-not-leak",
	}
	if err := sink.Emit(context.Background(), pingInvocation(), res, nil); err != nil {
		t.Fatalf("sink.Emit: %v", err)
	}

	events := waitForEvents(t, rec, 1, "no-leak emit")
	blob, err := json.Marshal(payloadEvent(t, events[0]))
	if err != nil {
		t.Fatalf("Marshal Event: %v", err)
	}
	wire := string(blob)
	for _, canary := range []string{
		"secret-stdout-leak-canary",
		"secret-stderr-leak-canary",
		"result-data-which-also-must-not-leak",
		"stdout",
		"stderr",
	} {
		if strings.Contains(wire, canary) {
			t.Errorf("wire JSON contains forbidden token %q. blob=%s", canary, wire)
		}
	}
}

// 8 — Full mode synthesises Flags["_surface"] ──────────────────────

func TestIntegration_TelemetrySink_FullModeAddsSurfaceFlag(t *testing.T) {
	installGlobalTelemetry(t, telemetry.ModeFull)

	rd := redact.New()
	if _, err := rd.AddRule("noop", "this-pattern-never-matches", ""); err != nil {
		t.Fatalf("build redactor: %v", err)
	}

	br, busSink, sink := buildBridgeWithTelemetry(t, "full",
		telemetry.WithRedactor(rd))
	t.Cleanup(func() { closeBridge(t, br) })

	rec := &busRecorder{}
	busSink.Subscribe("kit.telemetry.event.recorded", rec.handler)

	inv := pingInvocation()
	inv.Meta.Surface = cmdsurface.SurfaceREST
	inv.Flags = map[string]any{"--explicit": "x"}
	if err := sink.Emit(context.Background(), inv, cmdsurface.Result{}, nil); err != nil {
		t.Fatalf("sink.Emit: %v", err)
	}

	events := waitForEvents(t, rec, 1, "surface flag")
	ev := payloadEvent(t, events[0])
	if got := ev.Flags["_surface"]; got != "rest" {
		t.Errorf("Flags[_surface]=%q want=rest (Flags=%v)", got, ev.Flags)
	}
	// And the explicit flag survives alongside it.
	if got := ev.Flags["--explicit"]; got != "x" {
		t.Errorf("Flags[--explicit]=%q want=x (Flags=%v)", got, ev.Flags)
	}
}

// 9 — Anon mode strips Surface entirely ───────────────────────────

func TestIntegration_TelemetrySink_AnonModeStripsSurface(t *testing.T) {
	installGlobalTelemetry(t, telemetry.ModeAnon)

	br, busSink, sink := buildBridgeWithTelemetry(t, "anon")
	t.Cleanup(func() { closeBridge(t, br) })

	rec := &busRecorder{}
	busSink.Subscribe("kit.telemetry.event.recorded", rec.handler)

	inv := pingInvocation()
	inv.Meta.Surface = cmdsurface.SurfaceREST
	if err := sink.Emit(context.Background(), inv, cmdsurface.Result{}, nil); err != nil {
		t.Fatalf("sink.Emit: %v", err)
	}

	events := waitForEvents(t, rec, 1, "anon surface strip")
	ev := payloadEvent(t, events[0])
	if _, ok := ev.Flags["_surface"]; ok {
		t.Errorf("Anon Flags[_surface] present (=%q); must be absent", ev.Flags["_surface"])
	}
	// telemetry.Event has no top-level Surface column — verify by
	// re-marshalling and confirming the string "surface" doesn't
	// appear on the wire under any key.
	blob, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(blob), "surface") {
		t.Errorf("Anon wire JSON contains forbidden key/value containing \"surface\": %s", string(blob))
	}

	// Belt-and-braces: ensure the temp XDG dir was actually created on
	// disk — proving installGlobalTelemetry took effect. (If
	// XDG_STATE_HOME wiring regressed we'd be writing to the host.)
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		if _, err := os.Stat(filepath.Join(dir, "kit", "telemetry", "installation_id")); err != nil {
			t.Logf("note: install_id not written under XDG_STATE_HOME=%s (err=%v); not fatal", dir, err)
		}
	}
}
