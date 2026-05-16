# emailsink

Email delivery sink for kit bus events via a pluggable `Mailer`.
Subject and body are templated against the `bus.Event` before
delivery, optionally redacted, then handed to a breaker-wrapped
`Mailer.Send`.

## Constructor

```go
func New(m Mailer, opts ...Option) bus.Sink
```

No error: per spec decision #9, construction has no IO. SMTP dial
happens lazily inside `SMTPMailer.Send`. Required fields
(recipients, subject template, body template) are validated at
`Drain` time; a misconfigured sink returns a per-event error
instead of panicking on construction.

## Options

| Option | Default | Effect |
|--------|---------|--------|
| `WithFrom(addr)` | `""` | Default From address; can be overridden per `Message` (SMTP sets its own default via `WithSMTPFrom`). |
| `WithContentType(ct)` | `text/plain; charset=utf-8` | RFC 822 `Content-Type` header. |
| `WithRecipients(addrs...)` | required | To: list. Empty list = `Drain` error. |
| `WithSubject(t)` | required | Subject `Template`; nil = `Drain` error. |
| `WithBody(t)` | required | Body `Template`; nil = `Drain` error. |
| `WithRedactor(r)` | `nil` | `r.Apply` runs on rendered subject AND body before `Send`. |
| `WithBreaker(b)` | `nil` | Wraps `Mailer.Send` via `breaker.WrapCtx`. Open circuit short-circuits before dial. |

## The `Mailer` interface

```go
type Mailer interface {
    Send(ctx context.Context, msg Message) error
}

type Message struct {
    From        string
    To          []string
    ContentType string
    Subject     string
    Body        string
}
```

A `MailerFunc` adapter is provided for tests; production code wires
the bundled `SMTPMailer` or rolls a Sendgrid / Mailgun / SES
adapter that satisfies the same interface.

## SMTP transport

```go
func NewSMTPMailer(host string, port int, opts ...SMTPOption) Mailer
```

Dials lazily on every `Send` — no connection pooling, by design
(reference impl). Honours `ctx` cancellation through the dialer.

| SMTP option | Default | Effect |
|-------------|---------|--------|
| `WithSMTPAuth(user, pass)` | none | PLAIN auth; identity host stamped at construction. |
| `WithSMTPTLS(true)` | `false` | Opportunistic STARTTLS upgrade; errors if server does not advertise the extension. |
| `WithSMTPFrom(addr)` | none | Default From when `Message.From` is empty. |
| `WithSMTPTLSConfig(cfg)` | `&tls.Config{ServerName: host}` | Override TLS config used by STARTTLS. |
| `WithSMTPDialer(d)` | `&net.Dialer{Timeout: 30s}` | Override the underlying TCP dialer. Tests use this for deterministic deadlines. |

## Templates

```go
type Template interface {
    Render(e bus.Event) (string, error)
}
```

| Helper | Behaviour |
|--------|-----------|
| `TextTemplate(src)` | Parses `src` as `text/template`; renders against `bus.Event`. Parse errors at construction. |
| `LiteralTemplate(s)` | Always renders `s` regardless of the event. Useful for fixed subjects ("kit alert"). |

## Pipeline

Per [`go/runtime/notify/guardrails.go`](../../guardrails.go):

```
render(subject) + render(body) → redactor.Apply on each → breaker.WrapCtx(Mailer.Send)
```

`breaker.ErrBrokenCircuit` is returned unwrapped by
`breaker.WrapCtx` so `errors.Is` keeps working — `RetrySink` treats
it as terminal.

## Usage

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

## See also

- [`go/runtime/notify/README.md`](../../README.md) — package overview
- [`go/runtime/notify/guardrails.go`](../../guardrails.go) — pipeline convention godoc
- [`go/core/breaker/README.md`](../../../../core/breaker/README.md) — `WrapCtx` semantics
- [`go/core/redact/README.md`](../../../../core/redact/README.md) — `Apply` semantics
