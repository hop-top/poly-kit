package consent

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hop.top/kit/go/runtime/bus"
)

const legacyGranted = "other: keep\nkit:\n  telemetry:\n    consent:\n      state: granted\n      decided_at: \"2025-01-01T00:00:00Z\"\n      prompt_version: 1\n      decision_source: prompt\n"

func readFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestStateStore_PathsArePerToolUnderState(t *testing.T) {
	_, _ = newTestStore(t)
	state := os.Getenv("XDG_STATE_HOME")
	s, err := NewFileStoreFor("mytool")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(state, "mytool", "consent", "grants.yaml"); s.GrantsPath() != want {
		t.Fatalf("grants path %s, want %s", s.GrantsPath(), want)
	}
	a, _ := NewFileAuditorFor("mytool")
	if want := filepath.Join(state, "mytool", "consent", "audit.jsonl"); a.Path() != want {
		t.Fatalf("audit path %s, want %s", a.Path(), want)
	}
	for _, bad := range []string{"", "..", "a/b", " x"} {
		if _, err := NewFileStoreFor(bad); err == nil {
			t.Errorf("tool %q accepted", bad)
		}
	}
	g := allowAlways("io.files.read", "")
	g.DecidedAt = t0
	if err := s.PutGrant(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]os.FileMode{s.GrantsPath(): 0o600, filepath.Dir(s.GrantsPath()): 0o700} {
		fi, err := os.Stat(p)
		if err != nil || fi.Mode().Perm() != want {
			t.Fatalf("%s perm %v err %v, want %o", p, fi.Mode().Perm(), err, want)
		}
	}
	if _, err := os.Stat(s.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a grant write created config.yaml: %v", err)
	}
}

func TestTelemetryCompat_FallbackRead(t *testing.T) {
	cfg := legacyGranted
	s := corpusStore(t, &cfg, nil, nil)
	d, err := s.Get(context.Background())
	if err != nil || d.State != StateGranted {
		t.Fatalf("fallback read %+v %v", d, err)
	}
	if _, err := os.Stat(s.GrantsPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a read created the grants file")
	}
}

func TestTelemetryCompat_MigrateOnWrite(t *testing.T) {
	ctx := context.Background()
	cfg := legacyGranted
	s := corpusStore(t, &cfg, nil, nil)
	if err := s.Set(ctx, Decision{State: StateDenied, DecidedAt: t0, PromptVersion: 1, DecisionSource: SourceFlag}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(readFile(t, s.GrantsPath()), []byte("capability: telemetry")) {
		t.Fatal("telemetry write did not land in the grants file")
	}
	// The grants file is the source of truth from now on, even if an
	// older SDK later rewrites the legacy record.
	if err := os.WriteFile(s.Path(), []byte(legacyGranted), 0o600); err != nil {
		t.Fatal(err)
	}
	d, _ := s.Get(ctx)
	if d.State != StateDenied {
		t.Fatalf("state %s, want the grants file's denied", d.State)
	}
	if NewHook(s).Granted(ctx) {
		t.Fatal("hook disagrees with the grants file")
	}
}

func TestTelemetryCompat_MirrorKeepsLegacyReadersInStep(t *testing.T) {
	ctx := context.Background()
	cfg := legacyGranted
	s := corpusStore(t, &cfg, nil, nil)
	if err := s.Set(ctx, Decision{State: StateDenied, DecidedAt: t0, PromptVersion: 1, DecisionSource: SourceFlag}); err != nil {
		t.Fatal(err)
	}
	body := string(readFile(t, s.Path()))
	if !strings.Contains(body, "state: denied") || !strings.Contains(body, "other: keep") {
		t.Fatalf("legacy record not mirrored or sibling lost:\n%s", body)
	}
}

func TestTelemetryCompat_RevokeClearsGrantsFileAndLegacy(t *testing.T) {
	ctx := context.Background()
	cfg := legacyGranted
	old := "telemetry:\n  consent:\n    state: granted\n    decided_at: \"2025-01-01T00:00:00Z\"\n    prompt_version: 1\n    decision_source: prompt\n"
	s := corpusStore(t, &cfg, &old, nil)
	g := NewGrants(s, fixedClock(), WithEnv(MapEnv(nil)))
	if err := g.Decide(ctx, allowAlways(CapTelemetry, "")); err != nil {
		t.Fatal(err)
	}
	if removed, err := g.Revoke(ctx, CapTelemetry, "", SourceFlag); err != nil || !removed {
		t.Fatalf("revoke %v %v", removed, err)
	}
	for _, p := range []string{s.GrantsPath(), s.Path(), s.legacyPath} {
		if bytes.Contains(readFile(t, p), []byte("granted")) || bytes.Contains(readFile(t, p), []byte("capability: telemetry")) {
			t.Fatalf("%s still holds telemetry consent:\n%s", p, readFile(t, p))
		}
	}
	if d, _ := s.Get(ctx); d.State != StateUnknown {
		t.Fatalf("after revoke %s", d.State)
	}
}

func TestTelemetryCompat_CorruptGrantsFileFailsClosedConfigUntouched(t *testing.T) {
	ctx := context.Background()
	cfg := legacyGranted
	bad := "version: 1\ngrants: {oops\n"
	s := corpusStore(t, &cfg, nil, &bad)
	g := NewGrants(s, fixedClock(), WithEnv(MapEnv(nil)))
	if NewHook(s).Granted(ctx) {
		t.Fatal("hook granted over a corrupt grants file")
	}
	if _, err := g.Use(ctx, CapTelemetry, ""); KindOf(err) != KindMalformedStore {
		t.Fatalf("use: %v", err)
	}
	_ = g.Decide(ctx, allowAlways(CapTelemetry, ""))
	_ = g.Decide(ctx, allowAlways("io.files.read", ""))
	_, _ = g.Revoke(ctx, CapTelemetry, "", SourceFlag)
	_ = s.Clear(ctx)
	if string(readFile(t, s.Path())) != legacyGranted {
		t.Fatalf("config.yaml touched:\n%s", readFile(t, s.Path()))
	}
	if string(readFile(t, s.GrantsPath())) != bad {
		t.Fatal("corrupt grants file was rewritten")
	}
}

func TestConfigTypoDoesNotRefuseOtherCapabilities(t *testing.T) {
	ctx := context.Background()
	cfg := "kit: [unclosed\n"
	s := corpusStore(t, &cfg, nil, nil)
	g := NewGrants(s, fixedClock())
	if err := g.Decide(ctx, allowAlways("io.files.read", "")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Use(ctx, "io.files.read", "app:dek"); err != nil {
		t.Fatalf("a config typo refused a grant: %v", err)
	}
	if string(readFile(t, s.Path())) != cfg {
		t.Fatal("config.yaml touched")
	}
}

// refusedOnly records refused events reaching the bus.
func refusedRecorder(b bus.Bus) *[]AuditEvent {
	var got []AuditEvent
	b.Subscribe("kit.core.consent.refused", func(_ context.Context, e bus.Event) error {
		got = append(got, e.Payload.(AuditEvent))
		return errors.New("veto is ignored")
	})
	return &got
}

func TestRefused_AuditFailedGoesToTheBusOnly(t *testing.T) {
	ctx := context.Background()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	b := bus.New()
	defer b.Close(ctx)
	got := refusedRecorder(b)
	a, _ := NewFileAuditorFor("mytool")
	failing := AuditorFunc(func(ctx context.Context, e AuditEvent) error {
		if e.Event == AuditUsed {
			return errors.New("disk full")
		}
		return a.Record(ctx, e)
	})
	g := NewGrants(&MemoryGrantStore{}, fixedClock(), WithAuditor(failing), WithRelay(NewRelay(a, b)))
	_ = g.Decide(ctx, allowAlways("io.screen.capture", "app:dek"))
	if _, err := g.Use(ctx, "io.screen.capture", "app:dek"); err == nil {
		t.Fatal("use allowed although the audit failed")
	}
	if len(*got) != 1 || (*got)[0].Cause != CauseAuditFailed || (*got)[0].Key() != "" {
		t.Fatalf("bus refused events %+v", *got)
	}
	recs, _ := a.ReadAfter(0)
	for _, r := range recs {
		if r.Event == AuditRefused {
			t.Fatal("audit-failed refusal was written to the audit log")
		}
	}
}

func TestRefused_VetoAndAuditErrorsNeverAllow(t *testing.T) {
	ctx := context.Background()
	b := bus.New()
	defer b.Close(ctx)
	got := refusedRecorder(b)
	g := NewGrants(&MemoryGrantStore{}, fixedClock(), WithAuditor(BusAuditor(busPublisher{b}, DefaultTopics)))
	for range 3 {
		if _, err := g.Use(ctx, "io.screen.capture", "app:dek"); !errors.Is(err, ErrRefused) {
			t.Fatalf("use: %v", err)
		}
	}
	if len(*got) != 1 {
		t.Fatalf("refused emitted %d times, want 1 (debounce)", len(*got))
	}
}

// busPublisher adapts bus.Bus to domain.EventPublisher.
type busPublisher struct{ b bus.Bus }

func (p busPublisher) Publish(ctx context.Context, topic, source string, payload any) error {
	return p.b.Publish(ctx, bus.NewEvent(bus.Topic(topic), source, payload))
}

func TestOpenGrants_WithDeliveryConfirmsThroughTheRelay(t *testing.T) {
	ctx := context.Background()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	b := bus.New()
	defer b.Close(ctx)
	var ids []string
	b.Subscribe("kit.core.consent.#", func(_ context.Context, e bus.Event) error {
		ids = append(ids, e.ID)
		return nil
	})
	g, err := OpenGrants(WithTool("mytool"), WithDelivery(b), fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	_ = g.Decide(ctx, allowAlways("io.files.read", ""))
	_, _ = g.Use(ctx, "io.files.read", "app:dek")
	res, err := g.Relay().Flush(ctx)
	if err != nil || len(res.Delivered) != 2 || res.Pending != 0 {
		t.Fatalf("flush %+v %v", res, err)
	}
	if len(ids) != 2 || !strings.HasSuffix(ids[1], ":2") || !strings.HasPrefix(ids[0], "clog_") {
		t.Fatalf("event ids %v", ids)
	}
}

func TestRelay_RunDeliversOnNotify(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	b := bus.New()
	defer b.Close(context.Background())
	seen := make(chan struct{}, 4)
	b.Subscribe("kit.core.consent.#", func(context.Context, bus.Event) error {
		seen <- struct{}{}
		return nil
	})
	a, _ := NewFileAuditorFor("mytool")
	r := NewRelay(a, b)
	go func() { _ = r.Run(ctx) }()
	g := NewGrants(&MemoryGrantStore{}, fixedClock(), WithAuditor(a), WithRelay(r))
	_ = g.Decide(ctx, allowAlways("io.files.read", ""))
	select {
	case <-seen:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not deliver after Notify")
	}
}
