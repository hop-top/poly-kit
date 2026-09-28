// Package emailsink_test holds compile-tested examples of the email
// sink, including the supporting helpers (LiteralTemplate,
// TextTemplate, MailerFunc).
package emailsink_test

import (
	"context"
	"fmt"

	"hop.top/kit/go/core/breaker"
	"hop.top/kit/go/core/redact"
	"hop.top/kit/go/runtime/bus"
	emailsink "hop.top/kit/go/runtime/notify/sinks/email"
)

// ExampleNew demonstrates the constructor signature
// `func New(m Mailer, opts ...Option) bus.Sink` along with every
// Option:
//
//   - WithSubject(t Template)
//   - WithBody(t Template)
//   - WithRecipients(addrs...)
//   - WithFrom(addr)
//   - WithRedactor(r *redact.Redactor)
//   - WithBreaker(b breaker.Breaker)
//   - WithContentType(ct)
func ExampleNew() {
	captured := ""
	mailer := emailsink.MailerFunc(func(_ context.Context, msg emailsink.Message) error {
		captured = msg.Subject
		return nil
	})

	subj, err := emailsink.TextTemplate("alert: {{.Topic}}")
	if err != nil {
		fmt.Println("subj parse:", err)
		return
	}
	body := emailsink.LiteralTemplate("body fixed")

	red := redact.Default()
	b := breaker.New("email-example-new")
	defer breaker.Unregister("email-example-new")

	sink := emailsink.New(
		mailer,
		emailsink.WithFrom("ops@example.com"),
		emailsink.WithRecipients("a@example.com", "c@example.com"),
		emailsink.WithSubject(subj),
		emailsink.WithBody(body),
		emailsink.WithContentType("text/plain; charset=utf-8"),
		emailsink.WithRedactor(red),
		emailsink.WithBreaker(b),
	)
	defer sink.Close()

	if err := sink.Drain(context.Background(), bus.NewEvent("kit.runtime.breaker.tripped", "ex", nil)); err != nil {
		fmt.Println("unexpected:", err)
		return
	}
	fmt.Println(captured)
	// Output: alert: kit.runtime.breaker.tripped
}

// ExampleNewSMTPMailer demonstrates the SMTP transport
// constructor: `func NewSMTPMailer(host string, port int, opts ...SMTPOption) Mailer`.
// Construction does not dial; SMTP dial happens lazily inside
// SMTPMailer.Send. The returned Mailer is wired into
// emailsink.New like any other Mailer.
func ExampleNewSMTPMailer() {
	mailer := emailsink.NewSMTPMailer("smtp.local", 25)
	sink := emailsink.New(
		mailer,
		emailsink.WithFrom("ops@example.com"),
		emailsink.WithRecipients("ops-team@example.com"),
		emailsink.WithSubject(emailsink.LiteralTemplate("alert")),
		emailsink.WithBody(emailsink.LiteralTemplate("body")),
	)
	defer sink.Close()
	// Setup-only: we don't Drain because the SMTP host is fictitious.
	fmt.Println("wired")
	// Output: wired
}

// ExampleMessage shows the Message struct shape:
// To, Subject, Body, From and ContentType.
func ExampleMessage() {
	msg := emailsink.Message{
		To:          []string{"a@example.com"},
		Subject:     "hello",
		Body:        "world",
		From:        "ops@example.com",
		ContentType: emailsink.DefaultContentType,
	}
	fmt.Println(msg.Subject, msg.Body, msg.ContentType)
	// Output: hello world text/plain; charset=utf-8
}
