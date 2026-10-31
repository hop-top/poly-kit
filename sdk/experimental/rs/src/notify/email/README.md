# email

## What it answers

How does a bus event become a mail: subject and body templates, redaction,
a breaker, then a pluggable `Mailer`, with a small SMTP client shipped.
Port of Go `runtime/notify/sinks/email`. Feature `notify-email`, native
only. Wrong place for webhooks (`notify/webhook`), the desktop
(`notify/osnotify`) or HTTP mail APIs: implement `Mailer` for those.

## Use it when

- an event must reach a mailbox → `EmailSink::new(mailer).recipients(..).subject(..).body(..)`
- the transport is SMTP → `SmtpMailer::new(host, port)`, with `.auth`, `.starttls(true)`, `.from`, `.policy`
- the transport is something else, or a test → implement `Mailer` or pass a closure `Fn(&Context, &Message)`
- subjects or bodies may carry secrets → `.redactor(..)`; a flaky relay → `.breaker(..)`

## Quick start

```rust
use hop_top_kit::notify::email::{EmailSink, SmtpMailer};
use hop_top_kit::notify::TextTemplate;

let sink = EmailSink::new(SmtpMailer::new("localhost", 25).from("ops@example.com"))
    .recipients(["oncall@example.com"])
    .subject(TextTemplate::parse("[{{.Source}}] {{.Topic}}")?)
    .body(TextTemplate::parse("{{.Payload}}")?);
```

## Contract

- Construction cannot fail: recipients, mailer and both templates are checked at drain time and report per event.
- Pipeline: render subject and body, redact each, breaker gate, `Mailer::send`. An open circuit returns
  `BROKEN_CIRCUIT` unwrapped and the mailer is not called.
- `SmtpMailer` dials per send (no pooling) over a std `TcpStream` after `NetPolicy::check_dial`, so `--offline`
  applies and loopback stays reachable. No mail crate: its own dial would sidestep the policy.
- EHLO, STARTTLS when enabled (rustls on ring, platform roots, or `.tls_config(..)`), AUTH PLAIN only when
  advertised and only over TLS or to a loopback host, MAIL/RCPT/DATA with dot-stuffing.
- Unlike Go, a `From`, recipient or subject with a line break is refused (header injection). The TCP connect honours
  the context deadline; a send past the dial does not poll for cancellation, its socket I/O times out instead.

## Neighbours

- [`notify/`](../README.md): severity, filter and retry decorators, the other sinks
- `redact`, `breaker`, `netpolicy`: the guardrails this sink takes

## See also

- [Notify sink reference](../../../../../../docs/adopters/reference/notify-sinks.md)
- [`go/runtime/notify`](../../../../../../go/runtime/notify/README.md), the Go reference
