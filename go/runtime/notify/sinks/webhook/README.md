# webhooksink

HTTP POST sink for kit bus events. Renders an event through a
`Template`, optionally redacts the rendered body, then POSTs it
through a breaker-wrapped `http.RoundTripper`.

## Constructor

```go
func New(url string, opts ...Option) bus.Sink
```

No error: per spec decision #9, construction has no IO.
Misconfiguration surfaces at the first `Drain`, not at startup.

## Options

| Option | Default | Effect |
|--------|---------|--------|
| `WithHeader(k, v)` | none | Adds an HTTP header. Multiple calls accumulate (Add semantics, not Set). |
| `WithAuthBearer(token)` | none | Sugar for `WithHeader("Authorization", "Bearer "+token)`. |
| `WithTemplate(t)` | `DefaultJSONTemplate()` | Body renderer; nil ignored. |
| `WithHTTPClient(c)` | `&http.Client{Timeout: 5s}` | When set, `WithTimeout` is ignored. `WithBreaker` still applies (wraps `c.Transport`). |
| `WithTimeout(d)` | `5s` | Overall request deadline via `http.Client.Timeout`. Ignored when `WithHTTPClient` is set. |
| `WithRedactor(r)` | `nil` | Applied via `r.ApplyBytes` to the rendered body before send. |
| `WithBreaker(b)` | `nil` | Wraps the `http.RoundTripper` via `breaker.WrapHTTP`. Open circuit short-circuits before any HTTP egress. |

## Pipeline

Per the package-wide guardrail convention
([`go/runtime/notify/guardrails.go`](../../guardrails.go)):

```
template.Render → redactor.ApplyBytes → http.Client.Do (transport wrapped by breaker.WrapHTTP)
```

The breaker integration lives at the `http.RoundTripper` layer.
`client.Do` returns an error wrapping `breaker.ErrBrokenCircuit`
when the circuit is open; `Drain` returns it wrapped via `%w` so
`errors.Is(err, breaker.ErrBrokenCircuit)` keeps working. The
surrounding `RetrySink` treats that as terminal — no retries.

Non-2xx responses produce an error containing the status code and
up to 512 bytes of the response body (limit-read; protects against
megabyte error strings from misbehaving servers).

## Templates

```go
type Template interface {
    Render(e bus.Event) (body []byte, contentType string, err error)
}
```

Two helpers ship out of the box:

| Helper | Output |
|--------|--------|
| `DefaultJSONTemplate()` | Whole `bus.Event` marshalled as JSON; `application/json`. Default. Matches the JSONL line shape. |
| `SlackTemplate(tmpl string)` | Parses `tmpl` as `text/template`, executes against `bus.Event`, JSON-encodes as `{"text": "<rendered>"}`. Parse errors fail at construction; rendering errors surface from `Drain`. |

## Usage

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

## See also

- [`go/runtime/notify/README.md`](../../README.md) — package overview
- [`go/runtime/notify/guardrails.go`](../../guardrails.go) — pipeline convention godoc
- [`go/core/breaker/README.md`](../../../../core/breaker/README.md) — `WrapHTTP` semantics
- [`go/core/redact/README.md`](../../../../core/redact/README.md) — `ApplyBytes` semantics
