# notify

## What it answers

How does a published bus event reach a human: a Slack or PagerDuty webhook, an ops mailbox, the operator's desktop?
`notify` adds a severity convention, a filter decorator, a retry and dead-letter decorator and three outbound sinks on
top of the bus `Sink`. Wrong place for end-user notifications (welcome emails, preference routing), incident management
(ack, escalation, on-call) and durable queueing.

## Use it when

- a subset of events must leave the process → `FilterSink::new(sink).min_severity(..).topic_pattern(..)`
- a paging-grade sink needs at-least-once delivery → `RetrySink::new(sink).max_attempts(n).dead_letter(dl)`
- a payload should advertise urgency → add a `"severity"` key to the JSON object; read with `severity_of`
- the destination is HTTP, a mailbox or the desktop → `webhook::WebhookSink`, `email::EmailSink`, `osnotify::OsNotifySink`

## Quick start

```rust
use std::time::Duration;
use hop_top_kit::bus::{Context, Event};
use hop_top_kit::bus::Sink;
use hop_top_kit::notify::{FilterSink, RetrySink, Severity};
use hop_top_kit::notify::webhook::{SlackTemplate, WebhookSink};

let hook = WebhookSink::new("https://hooks.example.com/services/T000/B000/XXXX")
    .template(SlackTemplate::new("{{.Source}}: {{.Topic}}")?);

// Retry outermost, then the filter, then the reference sink.
let sink = RetrySink::new(FilterSink::new(hook).min_severity(Severity::Error).topic_pattern("billing.#"))
    .max_attempts(3)
    .backoff(|attempt| Duration::from_millis(100 << attempt));

let e = Event::new("billing.finance.invoice.failed", "billing", serde_json::json!({"severity": "critical"}));
sink.drain(&Context::background(), &e)?;
```

Put the sinks on a `TeeBus` (`hop_top_kit::bus::TeeBus::new(adapter, vec![Box::new(sink)])`) to fan every published
event out to them. The runnable form is `tests/notify_e2e.rs`.

## Contract

- Features: `notify` is the wasm-safe core (`bus` only): `Severity`, `FilterSink`, `RetrySink`, templates. Sinks are
  native only, one feature each: `notify-webhook`, `notify-email`, `notify-os`. Authority: the crate
  [feature table](../../README.md#features).
- Every decorator is a `Sink`; compose outermost first: `RetrySink` → `FilterSink` → reference sink. Stacked filters AND.
  Filter rejection is silent: `drain` returns `Ok`, no I/O.
- Severity: a JSON object payload's `severity` member, a lowercase keyword or a number `0..=4`; anything else is
  `Info`. No typed `WithSeverity`: a typed payload serializes the key.
- Retry counts as Go does: attempt 0 is the first try, the wait before attempt `n` is `backoff(n - 1)`. On
  exhaustion: the dead-letter sink's result, else the last inner error. A context ending during a wait returns
  `BusError::Context`.
- A breaker refusal is terminal for `RetrySink`: no more attempts, straight to the dead letter or the error. It is
  recognized by its text, `BROKEN_CIRCUIT` (`is_broken_circuit`), as a `Sink` reports `BusError::Handler(message)`.
- Outbound pipeline on every sink: `template render → redactor → breaker → egress`. Redactor and breaker are opt-in.
- Templates are Go's `text/template` cut to literal text and `{{.Field}}` actions. Bad syntax fails at construction,
  an unknown field at render.
- Webhook: POST through the guarded `api` client, so `--offline` refuses a remote endpoint and allows loopback.
  Non-2xx is an error with the status and at most 512 bytes of body; redirects are not followed (Go follows ten).
  `drain` is synchronous, safe inside an async task, and honors the `Context`.
- Email: see [`email/`](email/README.md). `SmtpMailer` dials through `NetPolicy`; AUTH PLAIN only over TLS or to
  loopback; line breaks in address or subject are refused.
- OS: macOS `osascript` (title and text are argv, never script source), Linux `notify-send --`, Windows an error.
  Spawned directly, never through a shell; `Runner` is the test seam.
- No `contracts/` corpus covers notify; the Go tests are ported (`tests/notify_*.rs`). Retry-wait defaults, template
  rendering rules and the rest: [Rust SDK reference, Notify](../../../../../docs/adopters/reference/rs-sdk.md#notify).

## Neighbours

- `hop_top_kit::bus`: `Sink`, `TeeBus`, `JsonlSink`, `StdoutSink`
- `hop_top_kit::redact` and `hop_top_kit::breaker`: the guardrails every sink takes
- `hop_top_kit::netpolicy`: `NetPolicy` and the guarded client behind the webhook
- `hop_top_kit::util::retry`: the `Rng` and `SplitMix64` the jitter reuses
- [`email/`](email/README.md): the email sink and its SMTP client

## See also

- [Notifications overview](../../../../../docs/adopters/concepts/notifications-overview.md)
- [Notify sink reference](../../../../../docs/adopters/reference/notify-sinks.md)
- [`go/runtime/notify`](../../../../../go/runtime/notify/README.md), the Go reference
