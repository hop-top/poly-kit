package emailsink_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"hop.top/kit/go/core/breaker"
	"hop.top/kit/go/core/redact"
	"hop.top/kit/go/runtime/bus"
	emailsink "hop.top/kit/go/runtime/notify/sinks/email"
)

// captureMailer records every call to Send.
type captureMailer struct {
	called atomic.Int64
	last   atomic.Pointer[emailsink.Message]
	err    error
}

func (c *captureMailer) Send(_ context.Context, msg emailsink.Message) error {
	c.called.Add(1)
	cp := msg
	c.last.Store(&cp)
	return c.err
}

func newEvent() bus.Event {
	return bus.Event{
		Topic:     "alerts.system.outage.fired",
		Source:    "test",
		Timestamp: time.Unix(0, 0),
		Payload:   map[string]any{"message": "hello world"},
	}
}

func TestDrain_Success(t *testing.T) {
	t.Parallel()

	cap := &captureMailer{}
	subj, _ := emailsink.TextTemplate("alert: {{.Topic}}")
	body := emailsink.LiteralTemplate("body fixed")

	s := emailsink.New(cap,
		emailsink.WithFrom("ops@example.com"),
		emailsink.WithRecipients("a@b.io", "c@d.io"),
		emailsink.WithSubject(subj),
		emailsink.WithBody(body),
	)

	if err := s.Drain(context.Background(), newEvent()); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	if got := cap.called.Load(); got != 1 {
		t.Fatalf("calls = %d, want 1", got)
	}
	got := cap.last.Load()
	if got == nil {
		t.Fatalf("captured message is nil")
	}
	if got.Subject != "alert: alerts.system.outage.fired" {
		t.Errorf("subject = %q", got.Subject)
	}
	if got.Body != "body fixed" {
		t.Errorf("body = %q", got.Body)
	}
	if got.From != "ops@example.com" {
		t.Errorf("from = %q", got.From)
	}
	if got.ContentType != emailsink.DefaultContentType {
		t.Errorf("content-type = %q", got.ContentType)
	}
	if len(got.To) != 2 || got.To[0] != "a@b.io" || got.To[1] != "c@d.io" {
		t.Errorf("recipients = %v", got.To)
	}
	if cerr := s.Close(); cerr != nil {
		t.Errorf("Close: %v", cerr)
	}
}

func TestDrain_NoRecipients_Errors(t *testing.T) {
	t.Parallel()

	cap := &captureMailer{}
	subj := emailsink.LiteralTemplate("s")
	body := emailsink.LiteralTemplate("b")

	s := emailsink.New(cap,
		emailsink.WithSubject(subj),
		emailsink.WithBody(body),
	)
	err := s.Drain(context.Background(), newEvent())
	if err == nil {
		t.Fatal("expected error")
	}
	if cap.called.Load() != 0 {
		t.Fatal("mailer should not be called")
	}
}

func TestDrain_NoTemplates_Errors(t *testing.T) {
	t.Parallel()

	cap := &captureMailer{}
	s := emailsink.New(cap, emailsink.WithRecipients("a@b.io"))
	err := s.Drain(context.Background(), newEvent())
	if err == nil {
		t.Fatal("expected error")
	}
	if cap.called.Load() != 0 {
		t.Fatal("mailer should not be called")
	}
}

func TestDrain_MailerError_Propagated(t *testing.T) {
	t.Parallel()

	want := errors.New("transport flaky")
	mailer := emailsink.MailerFunc(func(_ context.Context, _ emailsink.Message) error {
		return want
	})
	subj := emailsink.LiteralTemplate("s")
	body := emailsink.LiteralTemplate("b")
	s := emailsink.New(mailer,
		emailsink.WithRecipients("a@b.io"),
		emailsink.WithSubject(subj),
		emailsink.WithBody(body),
	)

	err := s.Drain(context.Background(), newEvent())
	if !errors.Is(err, want) {
		t.Fatalf("err = %v; want errors.Is(%v) true", err, want)
	}
}

func TestRedactor_RunsOnSubjectAndBody(t *testing.T) {
	t.Parallel()

	r := redact.New()
	if _, err := r.AddRule("token", `tok_[a-z0-9]+`, ""); err != nil {
		t.Fatalf("AddRule: %v", err)
	}

	cap := &captureMailer{}
	subj := emailsink.LiteralTemplate("alert tok_abc123 fired")
	body := emailsink.LiteralTemplate("body has tok_xyz789 inside")
	s := emailsink.New(cap,
		emailsink.WithRecipients("a@b.io"),
		emailsink.WithSubject(subj),
		emailsink.WithBody(body),
		emailsink.WithRedactor(r),
	)

	if err := s.Drain(context.Background(), newEvent()); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	got := cap.last.Load()
	if got == nil {
		t.Fatalf("nil message")
	}
	if containsTok(got.Subject) {
		t.Errorf("subject not redacted: %q", got.Subject)
	}
	if containsTok(got.Body) {
		t.Errorf("body not redacted: %q", got.Body)
	}
}

// containsTok reports whether s contains the literal "tok_" prefix.
func containsTok(s string) bool {
	for i := 0; i+4 <= len(s); i++ {
		if s[i:i+4] == "tok_" {
			return true
		}
	}
	return false
}

func TestBreaker_OpenCircuit_PropagatesErrBrokenCircuit(t *testing.T) {
	// not parallel: registering the breaker by name is a process-
	// global mutation.
	b := breaker.New("emailsink-test-trip")
	t.Cleanup(b.Reset)

	b.Trip("test")

	cap := &captureMailer{}
	subj := emailsink.LiteralTemplate("s")
	body := emailsink.LiteralTemplate("b")
	s := emailsink.New(cap,
		emailsink.WithRecipients("a@b.io"),
		emailsink.WithSubject(subj),
		emailsink.WithBody(body),
		emailsink.WithBreaker(b),
	)

	err := s.Drain(context.Background(), newEvent())
	if !errors.Is(err, breaker.ErrBrokenCircuit) {
		t.Fatalf("err = %v; want errors.Is(ErrBrokenCircuit) true", err)
	}
	if cap.called.Load() != 0 {
		t.Fatalf("mailer called %d times; want 0", cap.called.Load())
	}
}

func TestTextTemplate_RendersBusEvent(t *testing.T) {
	t.Parallel()

	tmpl, err := emailsink.TextTemplate("topic: {{.Topic}} src: {{.Source}}")
	if err != nil {
		t.Fatalf("TextTemplate: %v", err)
	}
	out, err := tmpl.Render(newEvent())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "topic: alerts.system.outage.fired src: test"
	if out != want {
		t.Errorf("got %q; want %q", out, want)
	}
}

func TestTextTemplate_BadSyntax_ReturnsError(t *testing.T) {
	t.Parallel()

	if _, err := emailsink.TextTemplate("{{.Topic"); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestLiteralTemplate(t *testing.T) {
	t.Parallel()

	tmpl := emailsink.LiteralTemplate("static")
	for i := 0; i < 3; i++ {
		out, err := tmpl.Render(newEvent())
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		if out != "static" {
			t.Errorf("got %q; want static", out)
		}
	}
}

func TestNew_NilMailer_ErrorsAtDrain(t *testing.T) {
	t.Parallel()

	subj := emailsink.LiteralTemplate("s")
	body := emailsink.LiteralTemplate("b")
	s := emailsink.New(nil,
		emailsink.WithRecipients("a@b.io"),
		emailsink.WithSubject(subj),
		emailsink.WithBody(body),
	)
	if err := s.Drain(context.Background(), newEvent()); err == nil {
		t.Fatal("expected error from nil mailer")
	}
}
