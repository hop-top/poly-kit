# notify

## What it answers

How does a published bus event reach a human: a Slack or PagerDuty
webhook, an ops mailbox, the operator's desktop? `notify` adds a
severity convention, a filter decorator, a retry + dead-letter
decorator and a catalog of guardrail-wired outbound sinks on top of
`bus.Sink`. Wrong package for end-user notifications (welcome emails,
preference routing), incident management (ack, escalation, on-call)
and durable queueing (`go/runtime/job`).

## Use it when

- a subset of events must leave the process → `notify.NewFilterSink(sink, notify.WithMinSeverity(...), notify.WithTopicPattern(...))`
- a paging-grade sink needs at-least-once delivery → `notify.NewRetrySink(sink, notify.WithMaxAttempts(n), notify.WithDeadLetter(dl))`
- a payload should advertise urgency → implement `WithSeverity` or add a `"severity"` key; read with `notify.SeverityOf`
- the destination is HTTP, a mailbox or the desktop → `sinks/webhook`, `sinks/email`, `sinks/osnotify`

## Quick start

```go
r := notify.NewRetrySink(
	failingSink{err: errors.New("transient")},
	notify.WithMaxAttempts(2),
	notify.WithBackoff(func(attempt int) time.Duration { return 0 }),
	notify.WithDeadLetter(nopSink{}),
)
defer r.Close()

if err := r.Drain(context.Background(), bus.NewEvent("kit.test", "ex", nil)); err != nil {
	fmt.Println("unexpected:", err)
	return
}
fmt.Println("ok")
// Output: ok
```

Verified by `example_test.go` in this directory (`ExampleNewRetrySink`);
the same file carries the end-to-end tee wiring.

## Contract

- Every decorator is a `bus.Sink`; compose outermost first:
  `RetrySink` → `FilterSink` → reference sink. Stacked filters AND.
- Filter rejection is silent: `Drain` returns `nil`, no I/O.
- Severity resolution: `WithSeverity` payload, then a `severity` key
  in `map[string]any` (keyword or number), else `SeverityInfo`.
- `breaker.ErrBrokenCircuit` from an inner sink is terminal for
  `RetrySink`: no further attempts, straight to the dead-letter, or
  returned unwrapped when no dead-letter is set.
- Outbound pipeline on every sink:
  `template render → redactor → breaker → egress`
  ([`guardrails.go`](guardrails.go)).
- Go-only MVP; ports wait on `bus.Sink` / `TeeBus` ports (ADR-0012).

## Neighbours

- [`sinks/`](sinks/README.md): the outbound sinks, one sub-package each: [`webhook/`](sinks/webhook/README.md), [`email/`](sinks/email/README.md), [`osnotify/`](sinks/osnotify/README.md).
- `go/runtime/bus`: `Sink`, `TeeBus`, `JSONLSink`, `StdoutSink`.
- `go/core/redact` and `go/core/breaker`: the guardrails every sink takes via `WithRedactor` / `WithBreaker`.

## See also

- [Notifications overview](../../../docs/adopters/concepts/notifications-overview.md): when to use, full wiring, severity, composition, guardrails, trust boundary
- [Notify sink reference](../../../docs/adopters/reference/notify-sinks.md): constructors, options, templates per sink
- [Bus overview](../../../docs/adopters/concepts/bus-overview.md)
