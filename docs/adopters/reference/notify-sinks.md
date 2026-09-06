# Notify sink reference

Reference for the three outbound sinks under
`hop.top/kit/go/runtime/notify/sinks`: constructors, options,
templates and the egress pipeline of each. For the mental model,
severity convention and composition pattern see
[notifications-overview.md](../concepts/notifications-overview.md).

Every sink follows the package-wide guardrail convention in
`go/runtime/notify/guardrails.go`: `template render → redactor →
breaker → egress`. The redactor sees exactly the wire bytes that
would otherwise leave the process; template transformations happen
first.

## webhook (`webhooksink`)

HTTP POST sink. Renders an event through a `Template`, optionally
redacts the rendered body, then POSTs it through a breaker-wrapped
`http.RoundTripper`.

```go
func New(url string, opts ...Option) bus.Sink
```

No error: per spec decision #9, construction has no IO.
Misconfiguration surfaces at the first `Drain`, not at startup.

### Options

| Option | Default | Effect |
|--------|---------|--------|
| `WithHeader(k, v)` | none | Adds an HTTP header. Multiple calls accumulate (Add semantics, not Set). |
| `WithAuthBearer(token)` | none | Sugar for `WithHeader("Authorization", "Bearer "+token)`. |
| `WithTemplate(t)` | `DefaultJSONTemplate()` | Body renderer; nil ignored. |
| `WithHTTPClient(c)` | `&http.Client{Timeout: 5s}` | When set, `WithTimeout` is ignored. `WithBreaker` still applies (wraps `c.Transport`). |
| `WithTimeout(d)` | `5s` | Overall request deadline via `http.Client.Timeout`. Ignored when `WithHTTPClient` is set. |
| `WithRedactor(r)` | `nil` | Applied via `r.ApplyBytes` to the rendered body before send. |
| `WithBreaker(b)` | `nil` | Wraps the `http.RoundTripper` via `breaker.WrapHTTP`. Open circuit short-circuits before any HTTP egress. |

### Pipeline

```
template.Render → redactor.ApplyBytes → http.Client.Do (transport wrapped by breaker.WrapHTTP)
```

The breaker integration lives at the `http.RoundTripper` layer.
`client.Do` returns an error wrapping `breaker.ErrBrokenCircuit`
when the circuit is open; `Drain` returns it wrapped via `%w` so
`errors.Is(err, breaker.ErrBrokenCircuit)` keeps working. The
surrounding `RetrySink` treats that as terminal: no retries.

Non-2xx responses produce an error containing the status code and
up to 512 bytes of the response body (limit-read; protects against
megabyte error strings from misbehaving servers).

### Templates

```go
type Template interface {
    Render(e bus.Event) (body []byte, contentType string, err error)
}
```

| Helper | Output |
|--------|--------|
| `DefaultJSONTemplate()` | Whole `bus.Event` marshalled as JSON; `application/json`. Default. Matches the JSONL line shape. |
| `SlackTemplate(tmpl string)` | Parses `tmpl` as `text/template`, executes against `bus.Event`, JSON-encodes as `{"text": "<rendered>"}`. Parse errors fail at construction; rendering errors surface from `Drain`. |

### Usage

Slack alert webhook with redaction + breaker, wrapped in a retry:

```go
slackTmpl, err := webhooksink.SlackTemplate(
    `{{.Topic}} on {{.Source}}: {{.Payload}}`)
if err != nil {
    return err
}

slackSink := webhooksink.New(
    os.Getenv("SLACK_WEBHOOK_URL"),
    webhooksink.WithTemplate(slackTmpl),
    webhooksink.WithRedactor(redact.Default()),
    webhooksink.WithBreaker(breaker.New("notify-slack")),
)

withRetry := notify.NewRetrySink(
    slackSink,
    notify.WithMaxAttempts(3),
    notify.WithDeadLetter(deadLetterSink),
)
```

## email (`emailsink`)

Email delivery sink via a pluggable `Mailer`. Subject and body are
templated against the `bus.Event` before delivery, optionally
redacted, then handed to a breaker-wrapped `Mailer.Send`.

```go
func New(m Mailer, opts ...Option) bus.Sink
```

No error: per spec decision #9, construction has no IO. SMTP dial
happens lazily inside `SMTPMailer.Send`. Required fields
(recipients, subject template, body template) are validated at
`Drain` time; a misconfigured sink returns a per-event error
instead of panicking on construction.

### Options

| Option | Default | Effect |
|--------|---------|--------|
| `WithFrom(addr)` | `""` | Default From address; can be overridden per `Message` (SMTP sets its own default via `WithSMTPFrom`). |
| `WithContentType(ct)` | `text/plain; charset=utf-8` | RFC 822 `Content-Type` header. |
| `WithRecipients(addrs...)` | required | To: list. Empty list = `Drain` error. |
| `WithSubject(t)` | required | Subject `Template`; nil = `Drain` error. |
| `WithBody(t)` | required | Body `Template`; nil = `Drain` error. |
| `WithRedactor(r)` | `nil` | `r.Apply` runs on rendered subject AND body before `Send`. |
| `WithBreaker(b)` | `nil` | Wraps `Mailer.Send` via `breaker.WrapCtx`. Open circuit short-circuits before dial. |

### The `Mailer` interface

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

### SMTP transport

```go
func NewSMTPMailer(host string, port int, opts ...SMTPOption) Mailer
```

Dials lazily on every `Send`: no connection pooling, by design
(reference impl). Honours `ctx` cancellation through the dialer.

| SMTP option | Default | Effect |
|-------------|---------|--------|
| `WithSMTPAuth(user, pass)` | none | PLAIN auth; identity host stamped at construction. |
| `WithSMTPTLS(true)` | `false` | Opportunistic STARTTLS upgrade; errors if server does not advertise the extension. |
| `WithSMTPFrom(addr)` | none | Default From when `Message.From` is empty. |
| `WithSMTPTLSConfig(cfg)` | `&tls.Config{ServerName: host}` | Override TLS config used by STARTTLS. |
| `WithSMTPDialer(d)` | `&net.Dialer{Timeout: 30s}` | Override the underlying TCP dialer. Tests use this for deterministic deadlines. |

### Templates

```go
type Template interface {
    Render(e bus.Event) (string, error)
}
```

| Helper | Behaviour |
|--------|-----------|
| `TextTemplate(src)` | Parses `src` as `text/template`; renders against `bus.Event`. Parse errors at construction. |
| `LiteralTemplate(s)` | Always renders `s` regardless of the event. Useful for fixed subjects ("kit alert"). |

### Pipeline

```
render(subject) + render(body) → redactor.Apply on each → breaker.WrapCtx(Mailer.Send)
```

`breaker.ErrBrokenCircuit` is returned unwrapped by
`breaker.WrapCtx` so `errors.Is` keeps working; `RetrySink` treats
it as terminal.

### Usage

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

## osnotify (`osnotifysink`)

OS-native desktop notification sink. Renders a title + text against
the `bus.Event`, optionally redacts, then shells out via a
breaker-wrapped runner.

```go
func New(opts ...Option) (bus.Sink, error)
```

The exception case to spec decision #9: construction CAN fail
because the constructor probes platform tooling. Returning
`(bus.Sink, error)` lets callers fail-fast on startup rather than
discovering "notify-send not on PATH" on the first event.

### Platform support

| Platform | Behaviour |
|----------|-----------|
| `darwin` | `osascript -e 'display notification ... with title ...'`. `osascript` ships with macOS; no probe, no install needed. |
| `linux` | `notify-send <title> <text>`. Probed via `exec.LookPath` at construction; missing → `New` returns an error. |
| `windows` | `New` returns `errors.New("osnotify: not supported on windows in MVP")`. Tracked for follow-up. |
| other | `New` returns `errors.New("osnotify: unsupported platform <GOOS>")`. |

The probe runs ONCE at construction. A `notify-send` installed
later is not picked up; this matches kit's fail-fast-on-misconfiguration
pattern.

### Options

| Option | Default | Effect |
|--------|---------|--------|
| `WithTitle(t)` | required | `Template` rendered as the notification title. Drain errors if unset. |
| `WithText(t)` | required | `Template` rendered as the notification body. Drain errors if unset. |
| `WithRedactor(r)` | `nil` | `r.Apply` runs on rendered title AND text before egress. |
| `WithBreaker(b)` | `nil` | Wraps `runner.Run` via `breaker.WrapCtx`. Open circuit short-circuits before exec. |

### Templates

Same interface as `emailsink.Template`:

```go
type Template interface {
    Render(e bus.Event) (string, error)
}
```

Helpers: `TextTemplate(src)` (parses `text/template`) and
`LiteralTemplate(s)` (fixed string).

### Runner injection

The internal `runner` interface (`runner.Run(ctx, name, args...)`)
abstracts `os/exec.CommandContext` so unit tests can assert command
construction without shelling out. The injection option
(`withRunner`) is unexported by design: production callers never
need to override the production `execRunner`. Only tests in the
same package use it via package-internal access. There is no public
extensibility path for swapping the exec layer; alternative
notification mechanisms should ship as separate sinks.

### Pipeline

```
render(title) + render(text) → redactor.Apply on each → breaker.WrapCtx(runner.Run)
```

`breaker.ErrBrokenCircuit` is returned unwrapped by
`breaker.WrapCtx`; `RetrySink` treats it as terminal.

AppleScript escaping (darwin): the rendered title/text are wrapped
in AppleScript double-quote literals with `"` and `\` escaped.
notify-send (linux) takes title/text as positional `argv` entries
(no shell interpretation), so no escaping is needed beyond what
`exec.CommandContext` already does.

### Usage

Desktop alert on warn-and-above, fronted by a filter:

```go
title, _ := osnotifysink.TextTemplate("[{{.Topic}}]")
text, _ := osnotifysink.TextTemplate("{{.Source}}: {{.Payload}}")

osSink, err := osnotifysink.New(
    osnotifysink.WithTitle(title),
    osnotifysink.WithText(text),
    osnotifysink.WithRedactor(redact.Default()),
    osnotifysink.WithBreaker(breaker.New("notify-osnotify")),
)
if err != nil {
    return fmt.Errorf("init os notify: %w", err)
}

desktop := notify.NewFilterSink(
    osSink,
    notify.WithMinSeverity(notify.SeverityWarn),
)
```

## Related pages

- [notifications-overview.md](../concepts/notifications-overview.md): severity, composition, guardrails, when to add a sink
- [`go/core/breaker/README.md`](../../../go/core/breaker/README.md): `WrapCtx` and `WrapHTTP` semantics
- [`go/core/redact/README.md`](../../../go/core/redact/README.md): `Apply` and `ApplyBytes` semantics
