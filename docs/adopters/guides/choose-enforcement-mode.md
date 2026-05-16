# Choose a bus enforcement mode

Pick `off`, `warn`, or `strict` based on your stage and risk
tolerance.

## Who this is for

Developers deciding which enforcement mode to ship. Once chosen,
configure it via
[configure-bus-enforcement.md](configure-bus-enforcement.md).

## Comparison

| Mode | Best for | Tradeoff | When to switch |
|---|---|---|---|
| `off` | Migrating legacy code that publishes free-form topics | No safety net; bad topics propagate to subscribers and sinks | Switch to `warn` as soon as the worst offenders are renamed |
| `warn` (default) | Active development; most CI runs; staging | Violations only surface if you installed an `ErrFunc` reporter; events still deliver | Switch to `strict` before tagging a release or running prod |
| `strict` | Production binaries; release branches; conformance tests | A single bad publish call returns `ErrInvalidTopic` and the event is dropped | Stay here; downgrade only for a deliberate migration window |

## Recommendation

For most projects, **start with `warn`** and install a reporter so
you see every violation in dev and CI. **Flip to `strict` before
release** so a slipped topic surfaces as a test failure instead of
a silent drop in production.

Use `off` only as a temporary escape hatch when you're importing
events from a system that hasn't migrated to the 4-segment
contract yet.

## off

```go
b := bus.New(bus.WithEnforce(bus.ModeOff))
```

Validation is skipped entirely; the reporter is never called. Any
string is accepted as a topic, so subscribers and sinks see
whatever publishers send. This is the only mode that is forward-
incompatible: code that worked under `off` may fail under `warn`
or `strict` once enforcement turns on.

Pick `off` only when migrating a legacy publisher that you can't
fix in the same change. Plan the move to `warn` immediately
afterward.

## warn

```go
b := bus.New(
    bus.WithEnforce(bus.ModeWarn),       // also the default
    bus.WithInvalidTopicReporter(func(err error) {
        log.Printf("[bus] %v", err)
    }),
)
```

Validation runs on every `Publish`. Failures go to the configured
`ErrFunc`; the event is still delivered to subscribers. Without a
reporter the violations are silent — install one in dev and CI so
they're visible.

Pick `warn` for the bulk of development. It's the default
precisely because it gives you full observability without breaking
flows when a topic shape drifts.

## strict

```go
b := bus.New(bus.WithEnforce(bus.ModeStrict))
```

Validation runs on every `Publish`. Failures invoke the reporter
**and** return `ErrInvalidTopic`; the event is not delivered. Use
`errors.Is(err, bus.ErrInvalidTopic)` to test.

Pick `strict` for production binaries and release-gated tests. A
bad topic becomes a loud failure at the call site instead of a
silent drop downstream.

## Related pages

- [configure-bus-enforcement.md](configure-bus-enforcement.md) —
  apply the chosen mode via option, config, or env
- [hook-cli-into-bus.md](hook-cli-into-bus.md) — publish + subscribe
  end-to-end
- `~/.ops/docs/glossary-event-names.md` — canonical topic vocabulary
