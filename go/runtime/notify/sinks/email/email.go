// Package emailsink delivers bus events as email messages via a
// pluggable Mailer. Subject and body are templated against the
// bus.Event before delivery.
//
// Pipeline order on Drain (per the package-wide guardrail
// convention in go/runtime/notify/guardrails.go):
//
//	render subject + body → redactor.Apply on each → breaker-wrapped Mailer.Send
//
// Construction cannot fail (notifications.md §3 decision #9). Required
// fields (recipients, subject template, body template) are validated
// at Drain time so a misconfigured sink surfaces as a per-event error
// rather than a constructor panic.
//
// Cross-references:
//
//   - docs/specs/notifications.md §3 #9, #10, #11; §7.5; §8.2
//   - go/runtime/notify/guardrails.go (pipeline order, ErrBrokenCircuit
//     terminal semantics for RetrySink)
//   - go/core/redact/README.md (Redactor.Apply semantics)
//   - go/core/breaker/README.md (Breaker.WrapCtx; ErrBrokenCircuit)
package emailsink

import (
	"bytes"
	"context"
	"fmt"
	"text/template"

	"hop.top/kit/go/core/breaker"
	"hop.top/kit/go/core/redact"
	"hop.top/kit/go/runtime/bus"
)

// DefaultContentType is applied when WithContentType is not provided.
const DefaultContentType = "text/plain; charset=utf-8"

// Message is the structure delivered to a Mailer. Subject and Body
// are rendered (and redacted, when WithRedactor is set) strings.
type Message struct {
	From        string
	To          []string
	ContentType string
	Subject     string
	Body        string
}

// Mailer sends a fully-rendered Message. Implementations may dial
// SMTP, call an HTTP API, or capture for testing.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

// MailerFunc is a func adapter for Mailer (test convenience).
type MailerFunc func(ctx context.Context, msg Message) error

// Send invokes f(ctx, msg).
func (f MailerFunc) Send(ctx context.Context, msg Message) error { return f(ctx, msg) }

// Template renders against a bus.Event. Implementations parse text
// at construction time and execute on Render.
type Template interface {
	Render(e bus.Event) (string, error)
}

// textTemplateImpl wraps a parsed text/template for Render.
type textTemplateImpl struct {
	tmpl *template.Template
}

// Render executes the parsed template against e and returns the
// resulting string.
func (t *textTemplateImpl) Render(e bus.Event) (string, error) {
	var buf bytes.Buffer
	if err := t.tmpl.Execute(&buf, e); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// TextTemplate parses tmpl as a text/template and returns a Template
// that renders against bus.Event. Returns an error if tmpl fails to
// parse.
func TextTemplate(tmpl string) (Template, error) {
	t, err := template.New("emailsink").Parse(tmpl)
	if err != nil {
		return nil, err
	}
	return &textTemplateImpl{tmpl: t}, nil
}

// literalTemplateImpl ignores the event and returns a fixed string.
type literalTemplateImpl struct {
	s string
}

// Render returns the fixed string regardless of e.
func (l *literalTemplateImpl) Render(_ bus.Event) (string, error) {
	return l.s, nil
}

// LiteralTemplate returns a Template that always renders s regardless
// of the event. Useful for static subjects or constant body fragments.
func LiteralTemplate(s string) Template {
	return &literalTemplateImpl{s: s}
}

// Sink is a bus.Sink that renders + redacts + sends an email per
// bus.Event via the configured Mailer.
type Sink struct {
	mailer      Mailer
	from        string
	contentType string
	recipients  []string
	subjectTmpl Template
	bodyTmpl    Template
	redactor    *redact.Redactor
	breaker     breaker.Breaker
}

// opts holds the option-bag mutated by Option closures during New.
type opts struct {
	from        string
	contentType string
	recipients  []string
	subject     Template
	body        Template
	redactor    *redact.Redactor
	breaker     breaker.Breaker
}

// Option configures a Sink at construction.
type Option func(*opts)

// WithFrom sets the From address used on the rendered Message.
func WithFrom(addr string) Option {
	return func(o *opts) { o.from = addr }
}

// WithContentType overrides the Message ContentType. Defaults to
// DefaultContentType when not provided.
func WithContentType(ct string) Option {
	return func(o *opts) { o.contentType = ct }
}

// WithRecipients sets the To list. Required at Drain time; missing
// recipients yield a per-Drain error.
func WithRecipients(addrs ...string) Option {
	return func(o *opts) { o.recipients = append([]string(nil), addrs...) }
}

// WithSubject sets the subject Template. Required at Drain time.
func WithSubject(t Template) Option {
	return func(o *opts) { o.subject = t }
}

// WithBody sets the body Template. Required at Drain time.
func WithBody(t Template) Option {
	return func(o *opts) { o.body = t }
}

// WithRedactor wraps rendered subject + body through r.Apply before
// egress. Default nil = no-op.
func WithRedactor(r *redact.Redactor) Option {
	return func(o *opts) { o.redactor = r }
}

// WithBreaker gates Mailer.Send through b. Default nil = no-op.
// Callers MUST treat breaker.ErrBrokenCircuit as terminal (see
// guardrails.go) — RetrySink already does so.
func WithBreaker(b breaker.Breaker) Option {
	return func(o *opts) { o.breaker = b }
}

// New returns an email sink that renders + sends bus events through
// the given Mailer. Constructor cannot fail (decision #9): if any
// required field is missing, Drain returns an error per call. Pass
// any combination of Option closures to configure the sink.
func New(m Mailer, options ...Option) bus.Sink {
	o := opts{contentType: DefaultContentType}
	for _, opt := range options {
		opt(&o)
	}
	return &Sink{
		mailer:      m,
		from:        o.from,
		contentType: o.contentType,
		recipients:  o.recipients,
		subjectTmpl: o.subject,
		bodyTmpl:    o.body,
		redactor:    o.redactor,
		breaker:     o.breaker,
	}
}

// Drain renders the configured templates against e, applies any
// configured redactor to the rendered strings, then sends the
// resulting Message through the Mailer (gated by the configured
// breaker, when present).
//
// Open-circuit error (breaker.ErrBrokenCircuit) is returned unwrapped
// by breaker.WrapCtx so callers can use errors.Is — see
// notify/guardrails.go.
func (s *Sink) Drain(ctx context.Context, e bus.Event) error {
	if s.mailer == nil {
		return fmt.Errorf("emailsink: mailer is required")
	}
	if len(s.recipients) == 0 {
		return fmt.Errorf("emailsink: no recipients configured")
	}
	if s.subjectTmpl == nil || s.bodyTmpl == nil {
		return fmt.Errorf("emailsink: subject and body templates required")
	}

	subject, err := s.subjectTmpl.Render(e)
	if err != nil {
		return fmt.Errorf("emailsink: render subject: %w", err)
	}
	body, err := s.bodyTmpl.Render(e)
	if err != nil {
		return fmt.Errorf("emailsink: render body: %w", err)
	}

	if s.redactor != nil {
		subject = s.redactor.Apply(subject)
		body = s.redactor.Apply(body)
	}

	msg := Message{
		From:        s.from,
		To:          append([]string(nil), s.recipients...),
		ContentType: s.contentType,
		Subject:     subject,
		Body:        body,
	}

	send := func(ctx context.Context) error {
		return s.mailer.Send(ctx, msg)
	}
	if s.breaker != nil {
		return breaker.WrapCtx(s.breaker, ctx, send)
	}
	return send(ctx)
}

// Close is a no-op; the embedded Mailer owns its own resources.
func (s *Sink) Close() error { return nil }
