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

## Quick start

Trim of the [spec §9](../../contributors/specs/notifications.md#9-wiring-example-end-to-end)
wiring — a webhook for criticals, an email for warnings, an audit
file, all teed off the same bus:

```go
import (
    "hop.top/kit/go/core/breaker"
    "hop.top/kit/go/core/redact"
    "hop.top/kit/go/runtime/bus"
    "hop.top/kit/go/runtime/notify"
    emailsink "hop.top/kit/go/runtime/notify/sinks/email"
    webhooksink "hop.top/kit/go/runtime/notify/sinks/webhook"
)

red := redact.Default()

pages := notify.NewRetrySink(
    notify.NewFilterSink(
        webhooksink.New(
            os.Getenv("PAGERDUTY_URL"),
            webhooksink.WithRedactor(red),
            webhooksink.WithBreaker(breaker.New("pages")),
        ),
        notify.WithMinSeverity(notify.SeverityCritical),
    ),
    notify.WithMaxAttempts(5),
)

subjectTmpl, _ := emailsink.TextTemplate("[{{.Topic}}]")
bodyTmpl, _ := emailsink.TextTemplate("{{.Source}}: {{.Payload}}")
ops := notify.NewFilterSink(
    emailsink.New(
        emailsink.NewSMTPMailer("smtp.local", 25),
        emailsink.WithRecipients("ops@example.com"),
        emailsink.WithSubject(subjectTmpl),
        emailsink.WithBody(bodyTmpl),
        emailsink.WithRedactor(red),
    ),
    notify.WithMinSeverity(notify.SeverityWarn),
)

audit, _ := bus.NewJSONLSinkFile("/var/log/kit/audit.jsonl")

b := bus.NewTeeBus(bus.New(), []bus.Sink{pages, ops, audit}, nil)
```

Publish to `b` as normal; `TeeBus` fans every event to every sink,
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

`SeverityOf` checks both shapes; missing or invalid → `SeverityInfo`.

See [spec §5](../../contributors/specs/notifications.md#5-severity-convention) for
the full wire contract.

## Composition pattern

Decorators are `bus.Sink`s. Outermost first:

```
RetrySink
  └─ FilterSink
       └─ <reference sink>
```

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

## See also

- [`docs/contributors/specs/notifications.md`](../../contributors/specs/notifications.md) — full spec, decisions, test plan
- [`go/runtime/notify/README.md`](../../../go/runtime/notify/README.md) — package reference
- [ADR-0012](../../contributors/adr/0012-notify-build-on-bus-sink.md) — build-on-bus-sink decision
- [`docs/adopters/concepts/bus-overview.md`](bus-overview.md) — bus pub/sub primer
- [`docs/contributors/audits/redact-egress-audit.md`](../../contributors/audits/redact-egress-audit.md) — egress audit
- [`docs/contributors/audits/breaker-primitives-audit.md`](../../contributors/audits/breaker-primitives-audit.md) — breaker audit
- [ADR-0005](../../contributors/adr/0005-kit-redact-egress-filtering.md) — redact egress filtering
- [ADR-0006](../../contributors/adr/0006-kit-breaker-runtime-circuit-breakers.md) — breaker runtime
