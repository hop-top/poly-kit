# webhooksink

## What it answers

How does a bus event become an HTTP POST? Renders the event through a
`Template`, optionally redacts the rendered body, then POSTs it through
a breaker-wrapped `http.RoundTripper`. Wrong package for a first-party
messaging API client (write a separate sink) and for mail (`../email`).

## Use it when

- the target is Slack, Discord, PagerDuty or any HTTP endpoint → `webhooksink.New(url, opts...)`
- the target expects Slack's `{"text": ...}` shape → `webhooksink.WithTemplate(webhooksink.SlackTemplate(tmpl))`
- the endpoint needs a bearer token → `webhooksink.WithAuthBearer(token)`

## Quick start

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

## Contract

- `New` returns `bus.Sink` with no error: construction has no IO
  (spec decision #9); misconfiguration surfaces at the first `Drain`.
- Pipeline: `template.Render → redactor.ApplyBytes → http.Client.Do`,
  transport wrapped by `breaker.WrapHTTP`.
- Open circuit: `Drain` returns an error wrapping
  `breaker.ErrBrokenCircuit` (`errors.Is` works); `RetrySink` treats
  it as terminal.
- Non-2xx: error carries the status code and up to 512 bytes of the
  response body.
- `WithHTTPClient` makes `WithTimeout` a no-op; `WithBreaker` still
  wraps `c.Transport`. Default client timeout 5s.
- Default template: whole `bus.Event` as `application/json`, the
  JSONL line shape.

## Neighbours

- `../email`, `../osnotify`: the other reference sinks.
- `go/runtime/notify`: `FilterSink`, `RetrySink`, severity.

## See also

- [Notify sink reference](../../../../../docs/adopters/reference/notify-sinks.md#webhook-webhooksink): options table, templates, pipeline
- [`go/runtime/notify/guardrails.go`](../../guardrails.go): pipeline convention godoc
- [`go/core/breaker/README.md`](../../../../core/breaker/README.md): `WrapHTTP` semantics
- [`go/core/redact/README.md`](../../../../core/redact/README.md): `ApplyBytes` semantics
