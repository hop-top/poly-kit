# Bus API Reference

Reference for `hop.top/kit/go/runtime/bus`. For concepts, when to
use the bus, and the topic-shape rules, see
[bus-overview.md](../concepts/bus-overview.md). For task-led pages and decision
help, follow the links in that overview.

Audience: authors of Go packages that publish or subscribe to kit
events. TS and Python ports are planned but not yet implemented.

## Quick example

```go
b := bus.New()
defer b.Close(context.Background())

unsub := b.Subscribe("kit.ai.#", func(ctx context.Context, e bus.Event) error {
    fmt.Println(e.Topic, e.Payload)
    return nil
})
defer unsub()

err := b.Publish(ctx, bus.NewEvent(
    "kit.ai.request.started", "kit.ai.client", payload,
))
```

## Event

Standard envelope for all bus messages.

| Field       | Type      | Description                        |
|-------------|-----------|------------------------------------|
| `Topic`     | `Topic`   | dot-separated 4-segment path,     |
|             | (string)  | e.g. `"kit.ai.request.started"`   |
| `Source`    | `string`  | emitter id, e.g. `"kit.ai.client"` |
| `Timestamp` | `time`    | creation time (auto-set by        |
|             |           | `NewEvent`)                        |
| `Payload`   | `any`     | event-specific data                |

### Creating Events

| Language | Function                                     |
|----------|----------------------------------------------|
| Go       | `bus.NewEvent(topic, source, payload)`       |
| TS       | `createEvent(topic, source, payload)` *(planned)* |
| Python   | `create_event(topic, source, payload)` *(planned)* |

Timestamp is set automatically to current time.

## Bus

Pub/sub hub. Create, subscribe, publish, close.

### Creating a Bus

| Language | Function          | Returns |
|----------|-------------------|---------|
| Go       | `bus.New()`       | `Bus`   |
| TS       | `createBus()` *(planned)* | `Bus`   |
| Python   | `create_bus()` *(planned)* | `Bus`   |

### Bus interface (Go)

```go
type Bus interface {
    Publish(ctx context.Context, e Event) error
    Subscribe(pattern string, h Handler) Unsubscribe
    SubscribeAsync(pattern string, h AsyncHandler) Unsubscribe
    Close(ctx context.Context) error
}
```

### Subscribe

```go
unsub := bus.Subscribe("kit.ai.#", func(ctx, e) error {
    return nil
})
unsub()  // remove subscription
```

### Publish

Delivers event to all matching subscribers:

1. Sync handlers run in registration order.
2. First sync error vetoes — remaining handlers skipped.
3. Async handlers launch after all sync handlers succeed.

### Handler types (Go only)

| Type           | Signature                             |
|----------------|---------------------------------------|
| `Handler`      | `func(ctx, Event) error` — sync, blocks publisher, can veto |
| `AsyncHandler` | `func(ctx, Event)` — goroutine, never blocks publisher |

TS and Python (planned): all handlers are async; sync veto via
returned promise rejection / raised exception.

## Topic patterns

MQTT-style wildcards on dot-separated segments:

| Pattern                  | Matches                                  |
|--------------------------|------------------------------------------|
| `kit.ai.request.started` | exact match only                         |
| `kit.ai.request.*`       | one trailing segment: `started`, `errored`; NOT `request.started.foo` |
| `kit.ai.#`               | zero+ trailing: `kit.ai`, `kit.ai.request.started`, etc. |

`#` must be the last segment. `*` matches exactly one.

## Topic format

Published topics MUST follow the canonical 4-segment shape:

```
[Source].[Category].[Object].[Action]
```

| Rule          | Value                                              |
|---------------|----------------------------------------------------|
| Segment regex | `^[a-z][a-z0-9_]*$` (lowercase, snake_case ok)    |
| Segment count | exactly 4 (published topics)                       |
| Total length  | ≤ 128 chars                                        |
| Wildcards     | `*`, `#` allowed only in subscribe patterns        |

The vocabulary of valid sources, categories, objects, and actions
is the source of truth at
`~/.ops/docs/glossary-event-names.md`.

## Enforcement modes

Validation runs every `Publish`. Three modes:

| Mode     | Behavior                                                |
|----------|---------------------------------------------------------|
| `off`    | No validation                                           |
| `warn`   | **Default.** Validate; report failures via `ErrFunc`; publish proceeds |
| `strict` | Validate; return `ErrInvalidTopic` from `Publish`       |

To configure: see `docs/adopters/guides/configure-bus-enforcement.md` (task page,
P3). To choose between modes: see `docs/adopters/guides/choose-enforcement-mode.md`
(decision page, P3).

## Sinks

Side-effect processors (logging, metrics, tracing). Errors never
block publish or handler delivery.

### Sink interface

```go
type Sink interface {
    Drain(ctx context.Context, e Event) error
    Close() error
}
```

### Built-in sinks

| Sink          | Output                                     |
|---------------|--------------------------------------------|
| `StdoutSink`  | human-readable to stdout — format: `[timestamp] topic source: payload` |
| `JSONLSink`   | newline-delimited JSON to writer/file      |

```go
sink := bus.NewStdoutSink()
sink := bus.NewJSONLSink(w)
sink, err := bus.NewJSONLSinkFile("/tmp/events.jsonl")
```

### TeeBus

Wraps a Bus and fans published events to sinks. Sink errors
reported via `ErrFunc` callback, never block publisher.

```go
tee := bus.NewTeeBus(b, []bus.Sink{jsonlSink}, onErr)
tee.Publish(ctx, event)
```

## Lifecycle

### Close

Stops accepting new publishes (`ErrBusClosed` returned). Waits for
in-flight async handlers, respecting context deadline.

```go
ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
defer cancel()
err := b.Close(ctx)
```

## Advanced: cross-process delivery

Adapter interface for cross-process event delivery (see `T-0529`).
The current in-memory bus will become one adapter; others (NATS,
Redis PubSub) will be pluggable via the same `Bus` interface.

## Cross-language parity

| Feature           | Go     | TS       | Python   |
|-------------------|--------|----------|----------|
| Event type        | yes    | planned  | planned  |
| Bus create        | yes    | planned  | planned  |
| Subscribe         | yes    | planned  | planned  |
| MQTT wildcards    | yes    | planned  | planned  |
| Sync handlers     | yes    | n/a      | n/a      |
| Async handlers    | yes    | default  | default  |
| Sinks (Tee)       | yes    | planned  | planned  |
| Close / drain     | yes    | planned  | planned  |

## Related pages

- [`docs/adopters/guides/hook-cli-into-bus.md`](../guides/hook-cli-into-bus.md) — task: end-to-end publish + subscribe (P3)
- [`docs/adopters/guides/configure-bus-enforcement.md`](../guides/configure-bus-enforcement.md) — task: turn on `strict` (P3)
- [`docs/adopters/guides/choose-enforcement-mode.md`](../guides/choose-enforcement-mode.md) — decision: `off` / `warn` / `strict` (P3)
- [`docs/adopters/reference/domain-events.md`](domain-events.md) — kit-emitted topic catalog
