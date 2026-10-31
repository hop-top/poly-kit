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

- Features: `notify` is the wasm-safe core (`bus` and nothing else): `Severity`, `severity_of`, `FilterSink`,
  `RetrySink`, `exponential_backoff`, `Template`, `TextTemplate`, `LiteralTemplate`. The sinks are native only, one
  feature each: `notify-webhook` (`api`, `breaker`, `redact`, tokio), `notify-email` (`breaker`, `redact`, `netpolicy`,
  `base64`, `rustls`, `rustls-native-certs`), `notify-os` (`breaker`, `redact`). Authority: the crate
  [feature table](../../README.md#features).
- Every decorator is a `Sink`; compose outermost first: `RetrySink` → `FilterSink` → reference sink. Stacked filters AND.
- Filter rejection is silent: `drain` returns `Ok`, no I/O. Checks run topic, severity, predicate.
- Severity: a JSON object payload with a `severity` member, a lowercase keyword or a number `0..=4` (a float must have
  no fraction); anything else is `Info`. There is no typed `WithSeverity`: a Rust payload is a `serde_json::Value`, so
  a typed payload advertises severity by serializing the key. Go's `Severity(99).String()` = `unknown` has no
  counterpart, the enum has five values.
- Retry counts as Go does: attempt 0 is the first try, the wait before attempt `n` is `backoff(n - 1)`, `max_attempts`
  below 1 is 1, a context that ends during a wait returns `BusError::Context`. On exhaustion: the dead-letter sink's
  result, else the last inner error as it came.
- A breaker refusal is terminal for `RetrySink`: no more attempts, straight to the dead letter or the error. A `Sink`
  reports errors as `BusError::Handler(message)`, so the refusal is recognized by its text, `BROKEN_CIRCUIT`, anywhere
  in the message (`is_broken_circuit`); the sinks' own wrapping ("webhook: post: ...") still matches. A sink of yours
  that gates on a breaker reports `BusError::handler(BrokenCircuit.to_string())`.
- The default retry wait parks the thread and wakes early on cancellation or the deadline; it is not compiled for wasm32,
  where it does not wait: give `RetrySink::sleeper` one.
- Outbound pipeline on every sink: `template render → redactor → breaker → egress`. Redactor (`Arc<Redactor>`) and
  breaker (`Arc<Breaker>`) are opt-in. The breaker records the transport's outcome, as Go's wrappers do.
- Templates are Go's `text/template` reduced to literal text and `{{.Field}}` actions (`.Topic`, `.Source`,
  `.Timestamp`, `.ID`, `.WorkspaceID`, `.Subject`, `.DataContentType`, `.DataSchema`, `.Payload` and `.Payload.a.b`
  into a JSON object). Bad syntax fails at construction, an unknown field at render. A string renders raw, `null` as
  `<nil>`, a missing member as `<no value>`, any other value as compact JSON.
- Webhook: POST through the guarded `api` client, so an offline `NetPolicy` (`.policy(..)`) refuses a remote endpoint
  and allows loopback. A non-2xx is an error carrying the status and at most 512 bytes of the body. Default timeout 5s;
  `.header` accumulates and repeats; `.auth_bearer`. Redirects are not followed (Go follows up to ten): a 3xx is a
  non-2xx. `drain` is synchronous: it runs the request on a current-thread runtime on a helper thread, so it is safe
  inside an async task, and it honors the `Context`'s cancellation and deadline.
- Email: `EmailSink` renders subject and body, redacts each, then `Mailer::send` through the breaker. Recipients and both
  templates are checked at drain time. `SmtpMailer` dials per send over a std `TcpStream`, after `NetPolicy::check_dial`
  (so `--offline` applies; loopback stays reachable). No mail crate: a crate's own dial would sidestep the policy. EHLO,
  STARTTLS (`.starttls(true)`, rustls on the ring provider, platform roots, or `.tls_config(..)`), AUTH PLAIN only when
  advertised and only over TLS or to a loopback host (Go's rule), MAIL/RCPT/DATA with dot-stuffing. Unlike Go, an
  address, subject or content type with a line break is refused. A send past the dial does not poll for cancellation;
  its socket reads and writes time out instead.
- OS: macOS `osascript -e 'display notification "<text>" with title "<title>"'` (`"` and `\` escaped), Linux
  `notify-send <title> <text>` (probed on `PATH` in `OsNotifySink::new`), Windows an error. Spawned directly, never
  through a shell; `Runner` is the test seam (`.runner(..)`, `for_platform(..)`). `ExecRunner` kills the child when the
  context ends.
- Not ported: the typed-payload `WithSeverity` path. No `contracts/` corpus covers notify; the Go tests are ported (see
  `tests/notify_*.rs`).

## Neighbours

- `hop_top_kit::bus`: `Sink`, `TeeBus`, `JsonlSink`, `StdoutSink`
- `hop_top_kit::redact` and `hop_top_kit::breaker`: the guardrails every sink takes
- `hop_top_kit::netpolicy`: `NetPolicy` and the guarded client behind the webhook
- `hop_top_kit::util::retry`: the `Rng` and `SplitMix64` the jitter reuses

## See also

- [Notifications overview](../../../../../docs/adopters/concepts/notifications-overview.md)
- [Notify sink reference](../../../../../docs/adopters/reference/notify-sinks.md)
- [`go/runtime/notify`](../../../../../go/runtime/notify/README.md), the Go reference
