# Configure bus topic enforcement

Set the bus to off, warn, or strict, and observe topic violations.

## Who this is for

Developers running a kit bus and choosing how strictly to enforce
the `[Source].[Category].[Object].[Action]` topic contract.

## Before you begin

You need:

- A kit project that uses `hop.top/kit/go/runtime/bus`
- A way to read configuration (option flag, config file, or env)

Modes available:

| Mode | Behavior |
|---|---|
| `off` | No validation. |
| `warn` | **Default.** Validate; report failures via `ErrFunc`; publish proceeds. |
| `strict` | Validate; return `ErrInvalidTopic`; publish blocked. |

## Recommended path

Leave the default (`warn`) and install an `ErrFunc` reporter.
Watch the reporter logs in development; you'll see every
violation while still getting events delivered.

```go
b := bus.New(
    bus.WithInvalidTopicReporter(func(err error) {
        log.Printf("[bus] %v", err)
    }),
)
```

Flip to `strict` before release.

## Steps

Set the mode by one of three mechanisms. Precedence (highest
first): **option > config > env > default**.

### Option 1: Go option (highest)

```go
b := bus.New(bus.WithEnforce(bus.ModeStrict))
```

Wins over config and env. Use when the binary owns the policy.

### Option 2: Config key

In your `core/config` source (e.g. `~/.config/kit/config.yaml`):

```yaml
kit:
  bus:
    enforce: strict
```

Resolve via `bus.ModeFromConfig(getter)` and pass through:

```go
m := bus.ModeFromConfig(cfg) // cfg satisfies ConfigGetter
b := bus.New(bus.WithEnforce(m))
```

`ConfigGetter` is `Get(key) (string, bool)` — `core/config.Config`
already satisfies it.

### Option 3: Env var

```bash
export KIT_BUS_ENFORCE=strict   # off | warn | strict
```

Pick it up at construction:

```go
b := bus.New(bus.WithEnforceFromEnv())
```

### Install the reporter

The reporter callback receives the validation error in `warn` and
`strict`. Without it, warn-mode violations are silently swallowed.

```go
b := bus.New(
    bus.WithEnforce(bus.ModeWarn),
    bus.WithInvalidTopicReporter(func(err error) {
        log.Printf("[bus] %v", err)
    }),
)
```

## Verify the result

Publish a deliberately invalid 2-segment topic:

```go
err := b.Publish(ctx, bus.NewEvent(
    "bad.topic",     // only 2 segments — violates contract
    "test.source",
    nil,
))
fmt.Println("err:", err)
```

Expected output by mode:

| Mode | stdout | reporter callback fires? |
|---|---|---|
| `off` | `err: <nil>` | no |
| `warn` | `err: <nil>` (event delivered) | yes |
| `strict` | `err: bus: invalid topic "bad.topic" (...)` | yes |

In `warn`, the reporter prints the violation and any subscribers
still receive the event. In `strict`, `Publish` returns
`bus.ErrInvalidTopic` (use `errors.Is` to test).

## Troubleshooting

### My topic is rejected in strict mode

The 4-segment, lowercase, snake_case-allowed contract is hard.
Check your topic against the rules and the canonical vocabulary at
`~/.ops/docs/glossary-event-names.md`.

### No warnings appear in warn mode

Warn mode reports through `ErrFunc`; with the default no-op
reporter, violations vanish. Install one:

```go
b := bus.New(bus.WithInvalidTopicReporter(func(err error) {
    log.Printf("[bus] %v", err)
}))
```

### Env var ignored

Construction-site precedence wins. If your code calls
`bus.New(bus.WithEnforce(bus.ModeStrict))`, the env var has no
effect. Drop the option, or use `bus.WithEnforceFromEnv()`.

### Unparseable mode

`ModeFromString` accepts `off`, `warn`, `strict` (case-insensitive,
trimmed). Anything else returns `ModeWarn` and an error;
`ModeFromEnv` and `ModeFromConfig` swallow the error and fall
through to the next layer.

## Optional

### Mix construction-time env with runtime override

```go
b := bus.New(
    bus.WithEnforceFromEnv(),                  // baseline from env
    bus.WithEnforce(bus.ModeStrict),           // overrides above
)
```

Later options win; the second `WithEnforce` overrides the first.

## Related pages

- [choose-enforcement-mode.md](choose-enforcement-mode.md) — pick
  the right mode for your stage
- [hook-cli-into-bus.md](hook-cli-into-bus.md) — emit your first
  event
- `~/.ops/docs/glossary-event-names.md` — canonical vocabulary
- [bus-api.md](../reference/bus-api.md) — full bus reference
