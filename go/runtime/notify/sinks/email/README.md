# emailsink

## What it answers

How does a bus event become an email? Subject and body are templated
against the `bus.Event`, optionally redacted, then handed to a
breaker-wrapped `Mailer.Send`. The `Mailer` is pluggable; an SMTP
implementation ships. Wrong package for chat webhooks (`../webhook`)
and for end-user transactional mail (managed provider, not shipped).

## Use it when

- the target is a mailbox or an ops digest → `emailsink.New(mailer, emailsink.WithRecipients(...), emailsink.WithSubject(t), emailsink.WithBody(t))`
- you have an SMTP relay → `emailsink.NewSMTPMailer(host, port, emailsink.WithSMTPAuth(...), emailsink.WithSMTPTLS(true))`
- you use Sendgrid / Mailgun / SES → implement `Mailer` (one `Send` method); `MailerFunc` adapts a closure for tests

## Quick start

Local SMTP (maildev / mailhog) for development:

```go
subjectTmpl, _ := emailsink.TextTemplate("[{{.Topic}}] {{.Severity}}")
bodyTmpl, _ := emailsink.TextTemplate(
    "{{.Source}} emitted {{.Topic}}\n\nPayload: {{.Payload}}\n")

mailer := emailsink.NewSMTPMailer("localhost", 1025,
    emailsink.WithSMTPFrom("kit@example.com"))

ops := emailsink.New(
    mailer,
    emailsink.WithRecipients("ops@example.com", "secfeed@example.com"),
    emailsink.WithSubject(subjectTmpl),
    emailsink.WithBody(bodyTmpl),
    emailsink.WithRedactor(redact.Default()),
    emailsink.WithBreaker(breaker.New("notify-email")),
)
```

## Contract

- `New` returns `bus.Sink` with no error: construction has no IO
  (spec decision #9). Recipients, subject and body templates are
  validated at `Drain`; a misconfigured sink returns a per-event
  error instead of panicking.
- Pipeline: `render(subject) + render(body) → redactor.Apply on each →
  breaker.WrapCtx(Mailer.Send)`.
- `breaker.ErrBrokenCircuit` comes back unwrapped (`errors.Is` works);
  `RetrySink` treats it as terminal.
- `SMTPMailer` dials lazily on every `Send`, no pooling, honours
  `ctx` cancellation through the dialer (default `net.Dialer`
  timeout 30s). `WithSMTPTLS(true)` is STARTTLS and errors when the
  server does not advertise it.
- `TextTemplate` parse errors fail at construction;
  `LiteralTemplate` ignores the event.

## Neighbours

- `../webhook`, `../osnotify`: the other reference sinks.
- `go/runtime/notify`: `FilterSink`, `RetrySink`, severity.

## See also

- [Notify sink reference](../../../../../docs/adopters/reference/notify-sinks.md#email-emailsink): options, `Mailer` and `Message`, SMTP options, templates
- [`go/runtime/notify/guardrails.go`](../../guardrails.go): pipeline convention godoc
- [`go/core/breaker/README.md`](../../../../core/breaker/README.md): `WrapCtx` semantics
- [`go/core/redact/README.md`](../../../../core/redact/README.md): `Apply` semantics
