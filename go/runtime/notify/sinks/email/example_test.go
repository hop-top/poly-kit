// Package emailsink_test mirrors the email code blocks in
// docs/specs/notifications.md §8.2 into compile-tested examples.
//
// Spec drift exposed by these examples (reported, not fixed; the spec
// is locked):
//
//   - WithContentType is shipped but missing from the §8.2 options
//     list. Example below demonstrates it; spec needs the option
//     added at next revision.
//
// Other shipped helpers not enumerated by §8.2 (LiteralTemplate,
// TextTemplate, MailerFunc) are demonstrated below as well; the spec
// could either name them as part of the public surface or stay silent
// and treat them as supporting types — that's a §8.2 revision call.
package emailsink_test

import (
	"context"
	"fmt"

	"hop.top/kit/go/core/breaker"
	"hop.top/kit/go/core/redact"
	"hop.top/kit/go/runtime/bus"
	emailsink "hop.top/kit/go/runtime/notify/sinks/email"
)

// ExampleNew demonstrates the §8.2 constructor signature
// `func New(m Mailer, opts ...Option) bus.Sink` along with every
// Option from the spec:
//
//   - WithSubject(t Template)
//   - WithBody(t Template)
//   - WithRecipients(addrs...)
//   - WithFrom(addr)
//   - WithRedactor(r *redact.Redactor)
//   - WithBreaker(b breaker.Breaker)
//
// And the WithContentType option (shipped, not in the §8.2 spec
// listing — drift item #3 in the P4.2 docs report).
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
		// NOTE: spec §8.2 does not list WithContentType; shipped code does.
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

// ExampleNewSMTPMailer demonstrates the §8.2 SMTP transport
// constructor: `func NewSMTPMailer(host string, port int, opts ...SMTPOption) Mailer`.
// Construction does not dial; SMTP dial happens lazily inside
// SMTPMailer.Send (spec §8.2). The returned Mailer is wired into
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

// ExampleMessage shows the §8.2 Message struct shape:
// `type Message struct { To []string; Subject, Body string; From string; ContentType string }`.
// The shipped struct has the same fields (different declared order).
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
