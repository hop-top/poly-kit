# notify

Transport-agnostic notification sinks for the kit bus.

`go/runtime/notify` builds on top of `bus.Sink` to add the four
concerns that turn a published event into a human-visible
notification: a severity convention, a filter decorator, a retry +
dead-letter decorator, and a small catalog of guardrail-wired
outbound sinks (webhook, email, OS-native). Each decorator is
itself a `bus.Sink`, so they compose freely with each other and
with the existing local sinks (`bus.JSONLSink`, `bus.StdoutSink`).

## When to use

- Routing a subset of bus events outward — Slack/Discord webhook,
  ops email digest, desktop notification on warn-and-above.
- Adding at-least-once delivery to a paging-grade sink (webhook to
  PagerDuty, SMS via a webhook gateway).
- Filtering an existing sink by topic + severity without touching
  the publisher or the `TeeBus`.

## When NOT to use

- Direct calls between tightly-coupled modules. Call the function.
- End-user notifications (welcome emails, password resets,
  preference-driven multi-channel routing). The `bus.Sink`
  interface accommodates a managed-provider sink (Novu / Courier
  / Knock); the MVP does not ship one.
- An incident-management product. Notify pushes events out;
  acknowledgement, escalation, and on-call rotation are the
  destination's problem.
- Cross-process durable queueing. The bus is in-process; sinks
  fire-and-forget. Wire a workflow runtime
  (`go/runtime/job/{temporal,hatchet,restate}`) when you need
  durability.

## Quick start

End-to-end wiring example:

```go
package main

import (
	"context"
	"fmt"
	"os"

	"hop.top/kit/go/core/breaker"
	"hop.top/kit/go/core/redact"
	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/runtime/notify"
	emailsink "hop.top/kit/go/runtime/notify/sinks/email"
	osnotifysink "hop.top/kit/go/runtime/notify/sinks/osnotify"
	webhooksink "hop.top/kit/go/runtime/notify/sinks/webhook"
)

func wireBus(ctx context.Context) (bus.Bus, error) {
	red := redact.Default()
	webBreaker := breaker.New("notify-webhook" /* policies */)
	smtpBreaker := breaker.New("notify-email" /* policies */)
	osBreaker := breaker.New("notify-osnotify" /* policies */)

	deadLetter, err := bus.NewJSONLSinkFile("/var/log/kit/dl.jsonl")
	if err != nil {
		return nil, fmt.Errorf("open dead-letter sink: %w", err)
	}

	pages := notify.NewRetrySink(
		notify.NewFilterSink(
			webhooksink.New(
				os.Getenv("PAGERDUTY_URL"),
				webhooksink.WithRedactor(red),
				webhooksink.WithBreaker(webBreaker),
			),
			notify.WithMinSeverity(notify.SeverityCritical),
		),
		notify.WithMaxAttempts(5),
		notify.WithDeadLetter(deadLetter),
	)

	subjectTmpl, _ := emailsink.TextTemplate("[{{.Topic}}] alert")
	bodyTmpl, _ := emailsink.TextTemplate("{{.Source}}: {{.Payload}}")
	summaries := notify.NewFilterSink(
		emailsink.New(
			emailsink.NewSMTPMailer("smtp.local", 25),
			emailsink.WithRecipients("ops@example.com"),
			emailsink.WithFrom("kit@example.com"),
			emailsink.WithSubject(subjectTmpl),
			emailsink.WithBody(bodyTmpl),
			emailsink.WithRedactor(red),
			emailsink.WithBreaker(smtpBreaker),
		),
		notify.WithMinSeverity(notify.SeverityWarn),
		notify.WithTopicPattern("billing.#"),
	)

	osSink, err := osnotifysink.New(
		osnotifysink.WithTitle(osnotifysink.LiteralTemplate("kit alert")),
		osnotifysink.WithText(osnotifysink.LiteralTemplate("see logs")),
		osnotifysink.WithRedactor(red),
		osnotifysink.WithBreaker(osBreaker),
	)
	if err != nil {
		return nil, fmt.Errorf("init os notify: %w", err)
	}
	desktop := notify.NewFilterSink(
		osSink,
		notify.WithMinSeverity(notify.SeverityWarn),
	)

	audit, err := bus.NewJSONLSinkFile("/var/log/kit/audit.jsonl")
	if err != nil {
		return nil, fmt.Errorf("open audit sink: %w", err)
	}

	return bus.NewTeeBus(bus.New(), []bus.Sink{pages, summaries, desktop, audit}, nil), nil
}
```

## Composition

Every decorator is a `bus.Sink`; ordering is by what should run
outermost first.

| Layer | Type | Purpose |
|-------|------|---------|
| `RetrySink` | outermost | At-least-once delivery; owns ctx + timer/select between attempts; routes exhausted events to a dead-letter `bus.Sink` (or returns last error). |
| `FilterSink` | middle | Drops events whose topic / severity / predicate does not match. Filter rejection is silent (`Drain` returns `nil`). |
| Reference sink | innermost | Renders, redacts, breaker-wraps, egresses. Each sink ships its own subpackage under `sinks/`. |

Filters can be stacked (logical AND across the chain). Retry
wrapping a filter is the common pages pattern: filter cuts the
volume first, retry only kicks in for events that survive the
filter. Wrap a `bus.JSONLSink` as the dead-letter for forensic
capture; pass `nil` (no `WithDeadLetter`) to surface the last
error to the upstream `TeeBus.ErrFunc`.

## Severity convention

Severity is read from the event payload via `SeverityOf(bus.Event)`
without mutating `bus.Event` itself. Resolution order:

1. `e.Payload` satisfies `WithSeverity` — return `p.Severity()`.
2. `e.Payload` is `map[string]any` with a `severity` key whose
   value is a lowercase keyword (`debug`/`info`/`warn`/`error`/
   `critical`) or a number in `[SeverityDebug, SeverityCritical]`.
3. Otherwise — `SeverityInfo` (the default).

| Constant | Numeric | Use |
|----------|---------|-----|
| `SeverityDebug` | 0 | Development trace, usually muted. |
| `SeverityInfo` | 1 | Routine operational events; default. |
| `SeverityWarn` | 2 | Degradation worth a heads-up. |
| `SeverityError` | 3 | Failure needing attention; typical email/chat threshold. |
| `SeverityCritical` | 4 | Page-now urgency; typical PagerDuty/SMS threshold. |

Existing emitters work as-is at `SeverityInfo`. Callers that care
opt in by implementing `WithSeverity` on their payload struct or
including a `"severity"` JSON field. See spec §5 for the full
wire contract.

## Guardrails

Every outbound reference sink integrates redaction and breakers:

- `WithRedactor(r *redact.Redactor)` — applied to the rendered
  payload immediately before egress. Default `nil` = no-op.
- `WithBreaker(b breaker.Breaker)` — gates egress. Default `nil`.
  An open circuit returns `breaker.ErrBrokenCircuit`, which
  `RetrySink` treats as terminal: no further attempts, route
  straight to the dead-letter (or return unwrapped).

Pipeline order on every outbound sink:
`template render → redactor → breaker → egress`. The redactor sees
exactly the wire bytes that would otherwise leave the process —
template transformations happen first. See
[`guardrails.go`](guardrails.go) for the package-wide convention
godoc.

## Cross-language parity

Go-only MVP. `bus.Sink` and `bus.TeeBus` themselves are still
Go-only (TS / Python ports of pub/sub exist but Sinks/Tee are
marked `planned`). Notify ports are gated on the bus primitives
porting first, tracked under `kit-notify-polyglot`. See ADR-0012
and spec §3 decision #8.

## See also

- [`go/runtime/bus/README.md`](../bus/README.md) — bus topic + sink reference
- [`sinks/webhook/README.md`](sinks/webhook/README.md) — HTTP POST sink
- [`sinks/email/README.md`](sinks/email/README.md) — Mailer-pluggable email sink
- [`sinks/osnotify/README.md`](sinks/osnotify/README.md) — darwin/linux desktop notifications
