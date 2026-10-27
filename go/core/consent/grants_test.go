package consent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hop.top/kit/go/console/output"
	"hop.top/kit/go/runtime/bus"
)

var t0 = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

func fixedClock() Option { return WithClock(func() time.Time { return t0 }) }

func allowAlways(c Capability, subject string) Grant {
	return Grant{Capability: c, Subject: subject, Decision: Allow, Duration: Always, DecisionSource: SourcePrompt, PromptVersion: 1}
}

func TestNewGrants_NilStorePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewGrants(nil) did not panic")
		}
	}()
	NewGrants(nil)
}

func TestUse_AuditFailureRefusesAndKeepsOnceGrant(t *testing.T) {
	ctx := context.Background()
	fail := true
	g := NewGrants(&MemoryGrantStore{}, fixedClock(), WithAuditor(AuditorFunc(func(_ context.Context, e AuditEvent) error {
		if e.Event == AuditUsed && fail {
			return errors.New("disk full")
		}
		return nil
	})))
	gr := allowAlways("io.clipboard.read", "app:dek")
	gr.Duration = Once
	if err := g.Decide(ctx, gr); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Use(ctx, "io.clipboard.read", "app:dek"); err == nil {
		t.Fatal("Use succeeded although the audit failed")
	}
	fail = false
	if _, err := g.Use(ctx, "io.clipboard.read", "app:dek"); err != nil {
		t.Fatalf("once grant was consumed by a failed use: %v", err)
	}
}

func TestUse_OnceGrantIsConsumedExactlyOnceUnderRace(t *testing.T) {
	ctx := context.Background()
	g := NewGrants(&MemoryGrantStore{}, fixedClock())
	gr := allowAlways("io.screen.capture", "app:dek")
	gr.Duration = Once
	if err := g.Decide(ctx, gr); err != nil {
		t.Fatal(err)
	}
	var ok atomic.Int32
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := g.Use(ctx, "io.screen.capture", "app:dek"); err == nil {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 1 {
		t.Fatalf("once grant used %d times", ok.Load())
	}
}

func TestRequest_PrompterMayCheckWithoutDeadlock(t *testing.T) {
	ctx := context.Background()
	g := NewGrants(&MemoryGrantStore{}, fixedClock())
	p := PrompterFunc(func(ctx context.Context, pr Prompt) (Answer, error) {
		if _, err := g.Check(ctx, pr.Capability, pr.Subject); err != nil {
			return Answer{}, err
		}
		if pr.Reason != ReasonUnset || pr.PromptVersion != 1 {
			t.Errorf("prompt = %+v", pr)
		}
		return Answer{Decision: Allow, Duration: Session}, nil
	})
	done := make(chan error, 1)
	go func() { _, err := g.Request(ctx, "io.files.read", "app:dek", p); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Request deadlocked while prompting")
	}
}

func TestRefusedError_RendersOnExitTaxonomy(t *testing.T) {
	cases := []struct {
		reason Reason
		code   string
		exit   int
		trans  string
	}{
		{ReasonDenied, output.CodeUnauthorized, 5, "permanent"},
		{ReasonUnset, output.CodeConsentRefused, output.ExitConsentRefused, "transient"},
		{ReasonExpired, output.CodeConsentRefused, output.ExitConsentRefused, "transient"},
		{ReasonDeclined, output.CodeConsentRefused, output.ExitConsentRefused, "transient"},
	}
	for _, c := range cases {
		var err error = &RefusedError{Capability: "io.screen.capture", Subject: "app:dek", Reason: c.reason}
		wrapped := errors.Join(errors.New("ctx"), err)
		if !errors.Is(wrapped, ErrRefused) {
			t.Errorf("%s: errors.Is(ErrRefused) false", c.reason)
		}
		var cli interface{ AsCLIError() *output.Error }
		if !errors.As(wrapped, &cli) {
			t.Fatalf("%s: no AsCLIError", c.reason)
		}
		e := cli.AsCLIError()
		if e.Code != c.code || e.ExitCode != c.exit || e.Transience != c.trans || e.SuggestedFix == "" {
			t.Errorf("%s: got %s/%d/%s fix=%q", c.reason, e.Code, e.ExitCode, e.Transience, e.SuggestedFix)
		}
	}
}

func TestGrantError_IsMatchesKindSentinel(t *testing.T) {
	err := ValidateCapability("screen")
	if !errors.Is(err, ErrInvalidCapability) || errors.Is(err, ErrInvalidSubject) {
		t.Fatalf("sentinel matching wrong for %v", err)
	}
	if KindOf(errors.New("x")) != "" {
		t.Fatal("KindOf on a plain error is not empty")
	}
}

func TestFileAuditor_AppendsJSONLinesOwnerOnly(t *testing.T) {
	ctx := context.Background()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	a, err := NewFileAuditor()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(a.Path()) != "audit.jsonl" || filepath.Base(filepath.Dir(a.Path())) != "consent" {
		t.Fatalf("path %s", a.Path())
	}
	g := NewGrants(&MemoryGrantStore{}, fixedClock(), WithAuditor(a))
	if err := g.Decide(ctx, allowAlways("io.files.read", "app:dek")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Use(ctx, "io.files.read", "app:dek"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(a.Path())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm %o", fi.Mode().Perm())
	}
	f, _ := os.Open(a.Path())
	defer f.Close()
	var events []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		events = append(events, m["event"].(string))
	}
	if len(events) != 2 || events[0] != "granted" || events[1] != "used" {
		t.Fatalf("events %v", events)
	}
}

type recPub struct {
	topics []string
	veto   error
}

func (p *recPub) Publish(_ context.Context, topic, source string, payload any) error {
	if _, ok := payload.(AuditEvent); !ok || source != "core.consent" {
		return errors.New("bad payload or source")
	}
	p.topics = append(p.topics, topic)
	return p.veto
}

func TestBusAuditor_PublishesTopicsAndVetoRefusesUse(t *testing.T) {
	ctx := context.Background()
	for _, tp := range []bus.Topic{DefaultTopics.Granted, DefaultTopics.Denied, DefaultTopics.Revoked, DefaultTopics.Used, DefaultTopics.Refused} {
		if err := bus.ValidateTopic(tp); err != nil {
			t.Fatalf("topic %s: %v", tp, err)
		}
	}
	pub := &recPub{}
	g := NewGrants(&MemoryGrantStore{}, fixedClock(), WithAuditor(BusAuditor(pub, DefaultTopics)))
	_ = g.Decide(ctx, allowAlways("io.files.read", ""))
	_, _ = g.Use(ctx, "io.files.read", "app:dek")
	_, _ = g.Revoke(ctx, "io.files.read", "", SourceFlag)
	d := allowAlways("io.files.read", "")
	d.Decision = Deny
	_ = g.Decide(ctx, d)
	want := []string{"kit.core.consent.granted", "kit.core.consent.used", "kit.core.consent.revoked", "kit.core.consent.denied"}
	if len(pub.topics) != len(want) {
		t.Fatalf("topics %v", pub.topics)
	}
	for i := range want {
		if pub.topics[i] != want[i] {
			t.Fatalf("topics %v, want %v", pub.topics, want)
		}
	}
	pub.veto = errors.New("vetoed")
	_ = g.Decide(ctx, allowAlways("io.files.read", "app:dek"))
	if _, err := g.Use(ctx, "io.files.read", "app:dek"); err == nil {
		t.Fatal("a vetoed use went through")
	}
}

func TestMultiAuditor_StopsAtFirstError(t *testing.T) {
	var second bool
	a := MultiAuditor(
		AuditorFunc(func(context.Context, AuditEvent) error { return errors.New("x") }),
		AuditorFunc(func(context.Context, AuditEvent) error { second = true; return nil }),
	)
	if err := a.Record(context.Background(), AuditEvent{}); err == nil || second {
		t.Fatalf("err=%v second=%v", err, second)
	}
}

func TestOpenGrants_FileBackedRoundTrip(t *testing.T) {
	ctx := context.Background()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	g, err := OpenGrants(fixedClock(), WithEnv(MapEnv(nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Decide(ctx, allowAlways(CapTelemetry, "")); err != nil {
		t.Fatal(err)
	}
	// The telemetry hook still reads the same record.
	s, _ := NewFileStore()
	if !NewHook(s).Granted(ctx) {
		t.Fatal("telemetry hook does not see the telemetry grant")
	}
	if _, err := g.Revoke(ctx, CapTelemetry, "", SourceFlag); err != nil {
		t.Fatal(err)
	}
	if NewHook(s).Granted(ctx) {
		t.Fatal("telemetry hook still granted after revoke")
	}
}

func TestMemoryGrantStore_RejectsInMemoryDurations(t *testing.T) {
	gr := allowAlways("io.files.read", "")
	gr.DecidedAt = t0
	gr.Duration = Session
	if err := (&MemoryGrantStore{}).PutGrant(context.Background(), gr); !errors.Is(err, ErrUnsupportedScope) {
		t.Fatalf("err %v", err)
	}
}

func TestFileStore_ClearStripsLegacyRecord(t *testing.T) {
	ctx := context.Background()
	legacy := "telemetry:\n  consent:\n    state: granted\n    decided_at: \"2025-01-01T00:00:00Z\"\n    prompt_version: 1\n    decision_source: prompt\nother: keep\n"
	s := corpusStore(t, nil, &legacy, nil)
	if err := s.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	d, err := s.Get(ctx)
	if err != nil || d.State != StateUnknown {
		t.Fatalf("after Clear: %+v %v", d, err)
	}
	b, _ := os.ReadFile(s.legacyPath)
	if !strings.Contains(string(b), "other: keep") {
		t.Fatalf("legacy sibling key lost:\n%s", b)
	}
}
