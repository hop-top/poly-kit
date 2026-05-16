package notify_test

// End-to-end test for the kit-notify wiring shape from
// docs/specs/notifications.md §9 + §10 (T-0375). Wires a single
// in-memory bus.Bus through a TeeBus that fans events to:
//
//   - a webhook sink wrapped in a FilterSink (pages-style:
//     critical+ severity floor + topic pattern),
//   - an email sink wrapped in a FilterSink wrapped in a RetrySink
//     (summaries-style: warn+ severity floor + billing topic),
//   - an audit JSONLSink (no filter; forensic capture).
//
// osnotify is intentionally excluded from the cross-platform e2e: its
// runner injection uses an unexported `withRunner` Option in the
// osnotifysink package, which the per-package osnotify_test.go
// already exercises. A build-tagged variant could spawn the real
// osascript / notify-send if needed in the future.
//
// Stability target: clean under `go test -race -count=100`. TeeBus
// fan-out is synchronous w.r.t. the publisher (see
// go/runtime/bus/sink.go TeeBus.Publish: each sink's Drain is called
// inline before Publish returns), so all assertions can run after
// Publish returns without polling.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"hop.top/kit/go/core/breaker"
	"hop.top/kit/go/core/redact"
	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/runtime/notify"
	emailsink "hop.top/kit/go/runtime/notify/sinks/email"
	webhooksink "hop.top/kit/go/runtime/notify/sinks/webhook"
)

// -----------------------------------------------------------------
// Fixtures
// -----------------------------------------------------------------

// recordingServer is an httptest.Server fixture that records every
// request body it receives. Body capture is mutex-protected so the
// race detector stays clean when the publisher and the assertion
// goroutines run on different OS threads.
type recordingServer struct {
	srv    *httptest.Server
	mu     sync.Mutex
	bodies [][]byte
}

func newRecordingServer(t *testing.T) *recordingServer {
	t.Helper()
	r := &recordingServer{}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.bodies = append(r.bodies, body)
		r.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *recordingServer) URL() string { return r.srv.URL }

func (r *recordingServer) snapshot() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]byte, len(r.bodies))
	for i, b := range r.bodies {
		out[i] = append([]byte(nil), b...)
	}
	return out
}

func (r *recordingServer) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.bodies)
}

// recordingMailer is an emailsink.MailerFunc fixture that captures
// every Message it sees. Mutex-protected for the same reason as
// recordingServer.
type recordingMailer struct {
	mu       sync.Mutex
	messages []emailsink.Message
}

func (m *recordingMailer) mailer() emailsink.Mailer {
	return emailsink.MailerFunc(func(_ context.Context, msg emailsink.Message) error {
		m.mu.Lock()
		m.messages = append(m.messages, msg)
		m.mu.Unlock()
		return nil
	})
}

func (m *recordingMailer) snapshot() []emailsink.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]emailsink.Message, len(m.messages))
	copy(out, m.messages)
	return out
}

func (m *recordingMailer) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.messages)
}

// errCapture records sink errors emitted by TeeBus's onErr callback.
type errCapture struct {
	mu   sync.Mutex
	errs []error
}

func (e *errCapture) onErr() bus.ErrFunc {
	return func(err error) {
		e.mu.Lock()
		e.errs = append(e.errs, err)
		e.mu.Unlock()
	}
}

func (e *errCapture) snapshot() []error {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]error, len(e.errs))
	copy(out, e.errs)
	return out
}

// typedSeverityPayload is a typed payload that satisfies
// notify.WithSeverity. Used to verify the typed-payload severity
// resolution path (vs. the map[string]any fallback).
type typedSeverityPayload struct {
	Message string
	Level   notify.Severity
}

func (p typedSeverityPayload) Severity() notify.Severity { return p.Level }

// -----------------------------------------------------------------
// TestE2E_AllSinksWiredThroughTeeBus — primary test.
// -----------------------------------------------------------------

func TestE2E_AllSinksWiredThroughTeeBus(t *testing.T) {
	t.Parallel()

	// Fixtures: a webhook server (pages), an in-memory mailer
	// (summaries), an audit buffer (forensic capture), and an
	// onErr capture for TeeBus sink failures.
	pagesServer := newRecordingServer(t)
	summariesMailer := &recordingMailer{}
	var auditBuf bytes.Buffer
	errs := &errCapture{}

	// pages sink: webhook → FilterSink (Critical+ AND breaker.# topic).
	pages := notify.NewFilterSink(
		webhooksink.New(pagesServer.URL()),
		notify.WithMinSeverity(notify.SeverityCritical),
		notify.WithTopicPattern("kit.runtime.breaker.#"),
	)

	// summaries sink: email → FilterSink (Warn+ AND billing.# topic)
	// → RetrySink. Even though our recordingMailer never fails,
	// wrapping in RetrySink proves the wiring compiles + runs and
	// that the additional decorator does not perturb pass-through
	// semantics.
	subj := emailsink.LiteralTemplate("alert")
	body := emailsink.LiteralTemplate("body")
	summariesEmail := emailsink.New(
		summariesMailer.mailer(),
		emailsink.WithRecipients("ops@example.com"),
		emailsink.WithSubject(subj),
		emailsink.WithBody(body),
	)
	summariesFilter := notify.NewFilterSink(
		summariesEmail,
		notify.WithMinSeverity(notify.SeverityWarn),
		notify.WithTopicPattern("billing.#"),
	)
	summaries := notify.NewRetrySink(
		summariesFilter,
		notify.WithMaxAttempts(2),
		notify.WithBackoff(notify.ExponentialBackoff(1*time.Millisecond, 1.0, false)),
	)

	// audit sink: every event, no filter.
	audit := bus.NewJSONLSink(&auditBuf)

	// Wire TeeBus.
	b := bus.New()
	tee := bus.NewTeeBus(b, []bus.Sink{pages, summaries, audit}, errs.onErr())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		_ = tee.Close(ctx)
	})

	// Workload: a varied mix of (Topic, Source, Severity, Payload)
	// shapes covering every routing case.
	type want struct {
		toPages, toSummaries bool
	}
	workload := []struct {
		ev   bus.Event
		want want
	}{
		// Critical breaker event → pages + audit only.
		{
			ev: bus.Event{
				Topic:   "kit.runtime.breaker.tripped",
				Source:  "kit",
				Payload: typedSeverityPayload{Message: "fuse blew", Level: notify.SeverityCritical},
			},
			want: want{toPages: true},
		},
		// Critical breaker event with map-shape payload (cross-process
		// JSON shape) → pages + audit only.
		{
			ev: bus.Event{
				Topic:   "kit.runtime.breaker.opened",
				Source:  "kit",
				Payload: map[string]any{"severity": "critical", "name": "egress"},
			},
			want: want{toPages: true},
		},
		// Info breaker reset → fails pages severity floor; audit only.
		{
			ev: bus.Event{
				Topic:   "kit.runtime.breaker.reset",
				Source:  "kit",
				Payload: typedSeverityPayload{Level: notify.SeverityInfo},
			},
		},
		// Warn billing event → summaries + audit only (fails pages topic).
		{
			ev: bus.Event{
				Topic:   "billing.invoice.paid",
				Source:  "billing-svc",
				Payload: typedSeverityPayload{Level: notify.SeverityWarn, Message: "paid"},
			},
			want: want{toSummaries: true},
		},
		// Error billing event → summaries + audit only.
		{
			ev: bus.Event{
				Topic:   "billing.invoice.failed",
				Source:  "billing-svc",
				Payload: map[string]any{"severity": "error", "id": "inv_99"},
			},
			want: want{toSummaries: true},
		},
		// Info billing event → fails summaries severity floor; audit only.
		{
			ev: bus.Event{
				Topic:   "billing.invoice.created",
				Source:  "billing-svc",
				Payload: map[string]any{"severity": "info"},
			},
		},
		// No-payload entity event → defaults to Info; audit only.
		{
			ev: bus.Event{
				Topic:  "kit.runtime.entity.created",
				Source: "kit",
			},
		},
		// Critical event with map payload but topic outside any
		// sink-specific pattern → audit only.
		{
			ev: bus.Event{
				Topic:   "shipping.label.dispatched",
				Source:  "ship",
				Payload: map[string]any{"severity": "critical"},
			},
		},
		// Typed payload satisfying WithSeverity at Critical, billing
		// topic → summaries + audit only (still fails pages topic).
		{
			ev: bus.Event{
				Topic:   "billing.payout.failed",
				Source:  "billing-svc",
				Payload: typedSeverityPayload{Level: notify.SeverityCritical, Message: "stuck"},
			},
			want: want{toSummaries: true},
		},
		// Critical breaker.# event → pages + audit. Different
		// sub-topic to exercise the # wildcard.
		{
			ev: bus.Event{
				Topic:   "kit.runtime.breaker.degraded.persistent",
				Source:  "kit",
				Payload: typedSeverityPayload{Level: notify.SeverityCritical},
			},
			want: want{toPages: true},
		},
	}

	// Publish.
	ctx := context.Background()
	wantPages := 0
	wantSummaries := 0
	for _, w := range workload {
		if w.want.toPages {
			wantPages++
		}
		if w.want.toSummaries {
			wantSummaries++
		}
		if err := tee.Publish(ctx, w.ev); err != nil {
			t.Fatalf("tee.Publish(%q) failed: %v", w.ev.Topic, err)
		}
	}

	// All sinks should have completed (TeeBus.Publish drains
	// synchronously). Assert per-sink counts and content.
	if got := pagesServer.count(); got != wantPages {
		t.Errorf("pages webhook saw %d events, want %d", got, wantPages)
	}
	if got := summariesMailer.count(); got != wantSummaries {
		t.Errorf("summaries mailer saw %d events, want %d", got, wantSummaries)
	}

	// Audit JSONLSink buffers writes via bufio; flush via Close so
	// auditBuf reflects everything Drain wrote. Close is idempotent
	// (see go/runtime/bus/jsonlsink.go), so the cleanup-driven
	// tee.Close pass is harmless.
	if err := audit.Close(); err != nil {
		t.Fatalf("audit.Close: %v", err)
	}
	wantAudit := len(workload)
	gotLines := strings.Count(strings.TrimRight(auditBuf.String(), "\n"), "\n") + 1
	if gotLines != wantAudit {
		t.Errorf("audit JSONLSink wrote %d lines, want %d", gotLines, wantAudit)
	}

	// Per-event content checks: pages requests are all on
	// kit.runtime.breaker.* topics at >= Critical.
	for i, body := range pagesServer.snapshot() {
		if !strings.Contains(string(body), "kit.runtime.breaker.") {
			t.Errorf("pages[%d] body does not reference a breaker topic: %s", i, body)
		}
	}
	// Summaries Messages all carry the literal subject + body the
	// templates render (the filter only routes; rendering is fixed).
	for i, msg := range summariesMailer.snapshot() {
		if msg.Subject != "alert" {
			t.Errorf("summaries[%d] subject = %q, want %q", i, msg.Subject, "alert")
		}
		if len(msg.To) != 1 || msg.To[0] != "ops@example.com" {
			t.Errorf("summaries[%d] To = %v", i, msg.To)
		}
	}

	// onErr should have captured nothing (no sinks failed).
	if got := errs.snapshot(); len(got) != 0 {
		t.Errorf("TeeBus onErr captured %d errors, want 0: %v", len(got), got)
	}
}

// -----------------------------------------------------------------
// TestE2E_GuardrailScenarios — covers spec §3 #10 (redaction) and
// #11 (open-circuit terminal + isolation across sinks on a TeeBus).
// -----------------------------------------------------------------

func TestE2E_GuardrailScenarios(t *testing.T) {
	t.Parallel()

	// Two webhook servers: one wired through a redactor, one wired
	// through a tripped breaker. A third "control" webhook proves
	// the tripped breaker on one sink does not poison the others.
	redactorServer := newRecordingServer(t)
	breakerServer := newRecordingServer(t)
	controlServer := newRecordingServer(t)

	// Redactor masking a simple "secret=..." pattern.
	red := redact.New()
	if _, err := red.AddRule("inline-secret", `secret=\S+`, ""); err != nil {
		t.Fatalf("redact.AddRule: %v", err)
	}

	redactorSink := webhooksink.New(redactorServer.URL(), webhooksink.WithRedactor(red))

	// Tripped breaker: unique name, registered for cleanup.
	const breakerName = "notify-e2e-guardrails"
	t.Cleanup(func() { breaker.Unregister(breakerName) })
	br := breaker.New(breakerName)
	br.Trip("e2e: pre-test trip")

	breakerSink := webhooksink.New(breakerServer.URL(), webhooksink.WithBreaker(br))
	controlSink := webhooksink.New(controlServer.URL())

	errs := &errCapture{}

	b := bus.New()
	tee := bus.NewTeeBus(b, []bus.Sink{redactorSink, breakerSink, controlSink}, errs.onErr())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		_ = tee.Close(ctx)
	})

	// Publish an event whose payload contains the secret. The
	// JSON-encoded body will contain `"secret=hunter2"` which the
	// redactor must mask before the request hits the recording
	// server.
	ev := bus.Event{
		Topic:     "kit.test.secret.published",
		Source:    "e2e",
		Timestamp: time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC),
		Payload:   map[string]any{"note": "secret=hunter2 leaked"},
	}
	if err := tee.Publish(context.Background(), ev); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// Redactor sink: 1 request, secret masked.
	if got := redactorServer.count(); got != 1 {
		t.Fatalf("redactor server saw %d requests, want 1", got)
	}
	body := string(redactorServer.snapshot()[0])
	if strings.Contains(body, "hunter2") {
		t.Errorf("redactor failed to mask secret; body = %s", body)
	}
	if !strings.Contains(body, "REDACTED") {
		t.Errorf("redactor default Mask should produce ***REDACTED***; body = %s", body)
	}

	// Breaker sink: 0 requests (open-circuit short-circuited
	// before HTTP egress), and onErr captured an
	// ErrBrokenCircuit.
	if got := breakerServer.count(); got != 0 {
		t.Errorf("breaker server saw %d requests; tripped breaker must short-circuit", got)
	}
	captured := errs.snapshot()
	foundCircuit := false
	for _, e := range captured {
		if errors.Is(e, breaker.ErrBrokenCircuit) {
			foundCircuit = true
			break
		}
	}
	if !foundCircuit {
		t.Errorf("expected onErr to capture ErrBrokenCircuit; captured=%v", captured)
	}

	// Control sink: 1 request — the tripped breaker on a sibling
	// sink does NOT poison the bus. Each sink owns its own
	// breaker; TeeBus continues fanning to the rest after a sink
	// error per go/runtime/bus/sink.go TeeBus.Publish.
	if got := controlServer.count(); got != 1 {
		t.Errorf("control server saw %d requests, want 1 — sibling breaker trip must not affect it", got)
	}
}

// -----------------------------------------------------------------
// TestE2E_DefaultsToSeverityInfo — regression: events with no
// severity-bearing payload default to Info, so a Warn floor filters
// them out and a no-floor filter passes them.
// -----------------------------------------------------------------

func TestE2E_DefaultsToSeverityInfo(t *testing.T) {
	t.Parallel()

	warnFloor := newRecordingServer(t)
	noFloor := newRecordingServer(t)

	warnFloorSink := notify.NewFilterSink(
		webhooksink.New(warnFloor.URL()),
		notify.WithMinSeverity(notify.SeverityWarn),
	)
	noFloorSink := notify.NewFilterSink(
		webhooksink.New(noFloor.URL()),
	)

	b := bus.New()
	tee := bus.NewTeeBus(b, []bus.Sink{warnFloorSink, noFloorSink}, nil)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		_ = tee.Close(ctx)
	})

	cases := []bus.Event{
		// nil payload → Info default
		{Topic: "t.a", Source: "e2e"},
		// non-map non-WithSeverity payload → Info default
		{Topic: "t.b", Source: "e2e", Payload: "just a string"},
		// map without severity key → Info default
		{Topic: "t.c", Source: "e2e", Payload: map[string]any{"id": 1}},
	}
	for _, ev := range cases {
		if err := tee.Publish(context.Background(), ev); err != nil {
			t.Fatalf("Publish %q: %v", ev.Topic, err)
		}
	}

	if got := warnFloor.count(); got != 0 {
		t.Errorf("warn-floor sink saw %d events, want 0 (Info < Warn floor)", got)
	}
	if got := noFloor.count(); got != len(cases) {
		t.Errorf("no-floor sink saw %d events, want %d", got, len(cases))
	}
}
