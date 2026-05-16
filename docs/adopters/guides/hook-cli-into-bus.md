# Hook a CLI command into the bus

Publish events from your command and observe them with a sink.

## Who this is for

Developers building a kit CLI who want their commands to emit
events other packages (or sinks) can react to.

## Before you begin

You need:

- A kit project (see
  [create-cli-project.md](create-cli-project.md))
- `hop.top/kit/go/runtime/bus` importable
- A topic that fits `[Source].[Category].[Object].[Action]` —
  see `~/.ops/docs/glossary-event-names.md`

For a tool named `mytool` emitting a deploy-started event, the
topic is:

```
mytool.commands.deploy.started
```

## Recommended path

Create one bus in `main`, share it with command and sink, publish
once, log once. The end-to-end snippet is in Steps.

## Steps

### 1. Create the bus

```go
// cmd/mytool/main.go
package main

import (
    "context"
    "log"

    "hop.top/kit/go/runtime/bus"
)

func main() {
    b := bus.New() // ModeWarn by default
    defer b.Close(context.Background())

    // Register sink before any publish.
    registerLogger(b)

    if err := runDeploy(context.Background(), b); err != nil {
        log.Fatal(err)
    }
}
```

### 2. Subscribe (the sink)

```go
func registerLogger(b bus.Bus) {
    b.Subscribe("mytool.commands.deploy.#",
        func(ctx context.Context, e bus.Event) error {
            log.Printf("[bus] %s from=%s payload=%+v",
                e.Topic, e.Source, e.Payload)
            return nil
        })
}
```

`#` matches zero or more trailing segments, so this sink picks up
`deploy.started`, `deploy.succeeded`, `deploy.failed`.

### 3. Publish (the command)

```go
type DeployStartedPayload struct {
    Target string `json:"target"`
    Commit string `json:"commit"`
}

func runDeploy(ctx context.Context, b bus.Bus) error {
    return b.Publish(ctx, bus.NewEvent(
        "mytool.commands.deploy.started",
        "mytool.commands.deploy",
        DeployStartedPayload{Target: "prod", Commit: "abc123"},
    ))
}
```

`bus.NewEvent` stamps `Timestamp` automatically.

## Verify the result

```bash
go run ./cmd/mytool
```

Expected stderr:

```
[bus] mytool.commands.deploy.started from=mytool.commands.deploy payload={Target:prod Commit:abc123}
```

If nothing prints, check:

- Subscription registered before `Publish`
- Pattern matches the topic (try `#` to catch everything)
- Bus not yet closed

## Troubleshooting

### Publish returns `bus: invalid topic ...`

Strict mode rejected your topic. Either fix the topic to fit the
4-segment shape (see `~/.ops/docs/glossary-event-names.md`) or
relax the mode for development — see
[configure-bus-enforcement.md](configure-bus-enforcement.md).

### Sink runs but blocks the command

You used `Subscribe` (sync). For background work, use
`SubscribeAsync`:

```go
b.SubscribeAsync("mytool.commands.deploy.#",
    func(ctx context.Context, e bus.Event) {
        // runs in its own goroutine; cannot veto
    })
```

### Want events written to a file

Use `JSONLSink` and `TeeBus` instead of a custom subscriber:

```go
sink, _ := bus.NewJSONLSinkFile("/tmp/mytool.jsonl")
b = bus.NewTeeBus(b, []bus.Sink{sink}, func(err error) {
    log.Printf("sink error: %v", err)
})
```

## Optional

### Sync vs async handlers

| Type | Signature | Vetoes? | Blocks publisher? |
|---|---|---|---|
| `Handler` | `func(ctx, Event) error` | yes (first error stops) | yes |
| `AsyncHandler` | `func(ctx, Event)` | no | no |

Use sync to gate side effects on validation. Use async for logs,
metrics, traces.

### MQTT-style wildcards

Subscribe patterns only:

| Pattern | Matches |
|---|---|
| `mytool.commands.deploy.started` | exact |
| `mytool.commands.*.started` | one segment in slot 3 |
| `mytool.commands.#` | zero+ trailing segments |

`#` must be the last segment. `*` matches exactly one. Wildcards
are never allowed in published topics.

## Advanced

`Publish` ordering inside one event:

1. Sync handlers run in subscription order.
2. First sync error vetoes; remaining handlers (sync and async)
   are skipped; error returns to caller.
3. Async handlers launch only after all sync handlers succeed.

`Close(ctx)` blocks until in-flight async handlers finish or
`ctx` expires. New `Publish` calls after `Close` return
`bus.ErrBusClosed`.

## Related pages

- [configure-bus-enforcement.md](configure-bus-enforcement.md) —
  set off / warn / strict
- [choose-enforcement-mode.md](choose-enforcement-mode.md) — pick
  the right mode for your stage
- [bus-api.md](../reference/bus-api.md) — full type and method reference
- `~/.ops/docs/glossary-event-names.md` — canonical topic vocabulary
