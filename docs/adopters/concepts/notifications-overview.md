# Kit notifications overview

> Audience: operators wiring a kit-powered product who want to push
> bus events outward to humans — Slack/Discord/PagerDuty webhooks,
> ops email, desktop notifications.

Kit's bus already routes events between in-process modules. The
`go/runtime/notify` package plugs a small catalog of outbound
sinks into that bus so events can leave the process and reach a
human notification channel without bespoke plumbing per channel.

## Who this is for

You are operating a kit-built CLI, server, or worker. You have a
publisher that emits `bus.Event` (audit, breaker-tripped, billing
threshold, etc.) and you want a subset of those events to surface
as a webhook POST, an email, or a desktop alert — with delivery
guarantees, redaction, and circuit-breaker gating that match the
rest of the kit guardrail story.

If you are building end-user notifications (welcome emails,
password resets, in-app inbox, multi-channel preferences with
fallback), this package is not enough — see "When to add a new
sink" below.

## What this gives you

- Three reference outbound sinks in `go/runtime/notify/sinks/`:
  webhook (HTTP POST with header/auth/template), email (pluggable
  `Mailer`; SMTP transport ships), and OS-native (darwin
  osascript, linux notify-send).
- A filter decorator (`FilterSink`) that drops events by topic
  pattern, severity floor, or arbitrary predicate.
- A retry decorator (`RetrySink`) that adds at-least-once delivery
  with pluggable backoff and a dead-letter `bus.Sink`.
- A severity convention (`SeverityOf(bus.Event)`) that reads
  severity from typed payloads or `map[string]any` shapes without
  forcing every emitter to migrate.

It does NOT replace the bus, an incident-management product, a
workflow runtime, or a managed-provider integration.

Reach for it when:

- Routing a subset of bus events outward: Slack/Discord webhook,
  ops email digest, desktop notification on warn-and-above.
- Adding at-least-once delivery to a paging-grade sink (webhook to
  PagerDuty, SMS via a webhook gateway).
- Filtering an existing sink by topic + severity without touching
  the publisher or the `TeeBus`.

Do not reach for it when:

- Modules are tightly coupled. Call the function.
- You need end-user notifications (welcome emails, password resets,
  preference-driven multi-channel routing). The `bus.Sink` interface
  accommodates a managed-provider sink (Novu / Courier / Knock); the
  MVP does not ship one.
- You need an incident-management product. Notify pushes events out;
  acknowledgement, escalation, and on-call rotation are the
  destination's problem.
- You need cross-process durable queueing. The bus is in-process;
  sinks fire-and-forget. Wire a workflow runtime
  (`go/runtime/job/{temporal,hatchet,restate}`) when you need
  durability.

## Quick start

The spec §9 wiring end to end: a webhook for criticals with retry
and a dead-letter file, an email for billing warnings, a desktop
alert on warn-and-above, an audit file, all teed off the same bus:

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

Publish to the returned bus as normal; `TeeBus` fans every event to every sink,
the filters trim, the retry handles transient failures.

## Severity convention

Severity is opt-in. Existing emitters work as-is — no payload
migration, no `bus.Event` change.

| Constant | Numeric | Typical channel |
|----------|---------|-----------------|
| `SeverityDebug` | 0 | development trace; muted in production |
| `SeverityInfo` | 1 | routine ops; default when payload is silent |
| `SeverityWarn` | 2 | desktop / chat heads-up |
| `SeverityError` | 3 | email / chat threshold |
| `SeverityCritical` | 4 | page-now (PagerDuty / SMS) |

Two ways to advertise severity:

- Typed payload: implement
  `Severity() notify.Severity` on the payload struct.
- Map / JSON payload: include a lowercase
  `"severity": "warn"` key (or numeric `2`).

`SeverityOf(bus.Event)` reads severity from the payload without
mutating `bus.Event` itself. Resolution order:

1. `e.Payload` satisfies `WithSeverity`: return `p.Severity()`.
2. `e.Payload` is `map[string]any` with a `severity` key whose value
   is a lowercase keyword (`debug`/`info`/`warn`/`error`/`critical`)
   or a number in `[SeverityDebug, SeverityCritical]`.
3. Otherwise `SeverityInfo` (the default).

See [spec §5](../../contributors/specs/notifications.md#5-severity-convention) for
the full wire contract.

## Composition pattern

Decorators are `bus.Sink`s. Outermost first:

```
RetrySink
  └─ FilterSink
       └─ <reference sink>
```

| Layer | Type | Purpose |
|-------|------|---------|
| `RetrySink` | outermost | At-least-once delivery; owns ctx + timer/select between attempts; routes exhausted events to a dead-letter `bus.Sink` (or returns last error). |
| `FilterSink` | middle | Drops events whose topic / severity / predicate does not match. Filter rejection is silent (`Drain` returns `nil`). |
| Reference sink | innermost | Renders, redacts, breaker-wraps, egresses. Each sink ships its own subpackage under `sinks/`; see [notify-sinks.md](../reference/notify-sinks.md). |

- Filters can be stacked (logical AND across the chain).
- Filter trims volume; if the event doesn't match the
  topic/severity/predicate, no I/O happens and `Drain` returns
  `nil` (silent rejection).
- Retry only kicks in on actual failures from the inner sink.
- Open-circuit (`breaker.ErrBrokenCircuit`) is **terminal** for
  `RetrySink` — no retries, route straight to the dead-letter
  sink (or return unwrapped). Retrying would defeat the breaker.

The dead-letter is just another `bus.Sink` — `bus.JSONLSink` for
forensic capture, another webhook for a fallback channel, or omit
`WithDeadLetter` to surface the last error to the upstream
`TeeBus.ErrFunc`.

## When to add a new sink

The `bus.Sink` interface is the entire extension contract:

```go
type Sink interface {
    Drain(ctx context.Context, e bus.Event) error
    Close() error
}
```

You do not extend `notify`. You write a sink, it satisfies
`bus.Sink`, and every notify decorator (`FilterSink`,
`RetrySink`) composes with it for free.

Cases where a new sink makes sense:

- A managed notification provider (Novu / Courier / Knock) for
  end-user notifications: welcome emails, preference-driven
  multi-channel routing, in-app inbox. The MVP does not ship one
  — a `novusink` / `couriersink` adapter is a deliberate seam,
  see [spec §11](../../contributors/specs/notifications.md#11-out-of-scope-follow-ups).
- A messaging product with first-party API semantics
  (Slack-incoming-webhook is already covered by `SlackTemplate`
  in `webhooksink`; a real Slack Web API client would be a new
  sink).
- A digest / batching layer that buffers N events over a window
  and sends one summary. Out of scope for MVP; sketched in spec §11.

When you write the sink, follow the
[guardrail integration convention](../../../go/runtime/notify/guardrails.go):
expose `WithRedactor(*redact.Redactor)` and
`WithBreaker(breaker.Breaker)`, and run them in pipeline order
(template → redactor → breaker → egress).

## Guardrails

Every outbound reference sink integrates redaction and breakers:

- `WithRedactor(r *redact.Redactor)`: applied to the rendered
  payload immediately before egress. Default `nil` = no-op.
- `WithBreaker(b breaker.Breaker)`: gates egress. Default `nil`.
  An open circuit returns `breaker.ErrBrokenCircuit`, which
  `RetrySink` treats as terminal: no further attempts, route
  straight to the dead-letter (or return unwrapped).

Pipeline order on every outbound sink:
`template render → redactor → breaker → egress`. The
[`guardrails.go`](../../../go/runtime/notify/guardrails.go) godoc is
the package-wide convention.

## Trust boundary

The redactor is YOUR redactor. The sink does not know what your
payload contains, what your secrets look like, or which fields
are PII. Wire `WithRedactor(redact.Default())` (the kit-shipped
gitleaks + Presidio rules) as a baseline; layer your own rules
via a custom `*redact.Redactor` for tenant-specific or domain
patterns.

The redactor runs on the **rendered** wire payload — webhook body
bytes, email subject + body strings, osnotify title + text — not
on `bus.Event` itself. Template transformations happen first so
the redactor sees exactly what would otherwise leave the process.

Per [`go/core/redact/PERF.md`](../../../go/core/redact/PERF.md), redact
is currently suited to heavyweight egress (telemetry batches,
LLM responses); notification volume is naturally low (one `Drain`
per matching event), so the per-payload cost amortises easily.

## Cross-language parity

Go-only MVP. `bus.Sink` and `bus.TeeBus` themselves are still
Go-only (TS / Python ports of pub/sub exist but Sinks/Tee are
marked `planned`). Notify ports are gated on the bus primitives
porting first. See ADR-0012 and spec §3 decision #8.

## See also

- [notify-sinks.md](../reference/notify-sinks.md): webhook, email and osnotify constructors, options, templates, pipelines

- [`docs/contributors/specs/notifications.md`](../../contributors/specs/notifications.md) — full spec, decisions, test plan
- [`go/runtime/notify/README.md`](../../../go/runtime/notify/README.md) — package README
- [ADR-0012](../../contributors/adr/0012-notify-build-on-bus-sink.md) — build-on-bus-sink decision
- [`docs/adopters/concepts/bus-overview.md`](bus-overview.md) — bus pub/sub primer
- [`docs/contributors/audits/redact-egress-audit.md`](../../contributors/audits/redact-egress-audit.md) — egress audit
- [`docs/contributors/audits/breaker-primitives-audit.md`](../../contributors/audits/breaker-primitives-audit.md) — breaker audit
- [ADR-0005](../../contributors/adr/0005-kit-redact-egress-filtering.md) — redact egress filtering
- [ADR-0006](../../contributors/adr/0006-kit-breaker-runtime-circuit-breakers.md) — breaker runtime
