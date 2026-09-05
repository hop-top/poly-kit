# sinks

Outbound `bus.Sink` implementations that turn a bus event into something a human sees; each renders through a `Template`, redacts, then delivers through a breaker.

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`webhook/`](webhook/README.md) | HTTP POST through a breaker-wrapped `http.RoundTripper` | the target is Slack, Discord, PagerDuty, or any HTTP endpoint |
| [`email/`](email/README.md) | templated subject and body handed to a pluggable `Mailer`; `NewSMTPMailer` ships | the target is a mailbox or an ops digest |
| [`osnotify/`](osnotify/README.md) | desktop notification via the platform notifier binary | the target is the operator's own screen |

## Conventions

- Constructors return `bus.Sink`, so every sink composes with `notify.Filter` and `notify.Retry` and with the local sinks in `runtime/bus`.
- Delivery is fire-and-forget at the sink; wrap with `notify.Retry` for at-least-once.
- Rendered bodies pass through `core/redact` before leaving the process when a redactor is configured.
