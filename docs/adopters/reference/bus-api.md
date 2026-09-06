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

| Field         | Go type      | JSON key                | Description |
|---------------|--------------|-------------------------|-------------|
| `Topic`       | `Topic` (string) | `topic`             | dot-separated 4-segment path, e.g. `"kit.ai.request.started"` |
| `Source`      | `string`     | `source`                | emitter id, e.g. `"kit.ai.client"` |
| `Timestamp`   | `time.Time`  | `timestamp`             | creation time (auto-set by `NewEvent`) |
| `Payload`     | `any`        | `payload`               | event-specific data |
| `WorkspaceID` | `string`     | `workspace_id,omitempty` | wsm workspace ULID; empty = global event |

JSON keys are lowercase. Cross-process subscribers parse the lowercase
form; capitalized keys break them.

Payload crosses process boundaries as JSON (network, SQLite and hub
adapters). In-process subscribers get the original Go value.
Cross-process subscribers get the decoded form: objects become
`map[string]any`, arrays `[]any`, numbers `float64`. The publisher's
struct type is not preserved over the wire. Typed consumers re-marshal:

```go
raw, _ := json.Marshal(e.Payload)
var pl events.TaskCreatedPayload
if err := json.Unmarshal(raw, &pl); err != nil { /* ... */ }
```

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
| Go       | `bus.New(opts ...Option)` | `Bus`   |
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
| Wildcards     | `*`, `#` rejected in published topics; allowed only in subscribe patterns |

Two validators, different jobs:

| Function                 | When it runs                     | Checks |
|--------------------------|----------------------------------|--------|
| `bus.Validate(Topic)`    | every `Publish`, per enforcement mode | 4 segments, segment regex, ≤ 128 chars, no wildcards |
| `bus.ValidateTopic(Topic)` | construction time, via `bus.PrefixTopics` | 4 segments, segment charset, **plus** past-tense action |

The past-tense rule lives in `ValidateTopic` only. `Publish` does not
enforce it: a topic like `kit.ai.request.start` passes `Validate` and is
delivered. Modules that build their topic tables with `PrefixTopics` get
the past-tense check at wiring time instead.

`ValidateTopic` accepts an action segment that ends in `ed`, or is one of
an internal irregular-form list (`started`, `sent`, `built`, `half_opened`,
`paid`, …). The list is unexported; extending it means a change to
`go/runtime/bus/topics.go`.

`PrefixTopics` takes a 3-segment prefix plus action segments and returns a
`TopicMap`:

```go
tm, err := bus.PrefixTopics("wsm.runtime.workspace", []string{"created", "updated"})
// tm["created"] == bus.Topic("wsm.runtime.workspace.created")
```

Enforced by `Validate` and `ValidateTopic`. The Object segment may
carry a modifier after the first underscore (`snapshot_reload`); see
[bus-overview.md](../concepts/bus-overview.md#object-modifier).

Invalid input returns a `*bus.InvalidTopicError` carrying the
offending topic and the reason (`errors.As`); it unwraps to the
sentinel `bus.ErrInvalidTopic` (`errors.Is`).

The vocabulary of valid sources, categories, objects, and actions
is the source of truth at
`~/.ops/docs/glossary-event-names.md`.

## Enforcement modes

Validation runs every `Publish`. Three modes:

| Mode     | Behavior                                                |
|----------|---------------------------------------------------------|
| `off`    | No validation; reporter not invoked                     |
| `warn`   | **Default.** Validate; report failures to the reporter; event still delivered |
| `strict` | Validate; report failures, and return the error from `Publish`; event not delivered |

Install the reporter with `bus.WithInvalidTopicReporter(fn ErrFunc)`;
the default is a no-op. Set the mode with `bus.WithEnforce(bus.ModeStrict)`
or `bus.WithEnforceFromEnv()`.

`Publish` returns an `*bus.InvalidTopicError` carrying the offending
`Topic` and a `Reason`. It unwraps to the `bus.ErrInvalidTopic` sentinel,
so test with `errors.Is(err, bus.ErrInvalidTopic)`.

Resolution precedence is explicit `WithEnforce` > config key
`kit.bus.enforce` > env `KIT_BUS_ENFORCE` > default `warn`. Unparseable
values fall through to the next layer rather than erroring.

In `strict` the returned error is a `*InvalidTopicError` and the
event is not delivered. In `warn` the reporter set by
`WithInvalidTopicReporter` receives it and Publish proceeds.

Override precedence, highest first: explicit `WithEnforce` >
`kit.bus.enforce` config > `KIT_BUS_ENFORCE` env > default (`warn`).

```go
b := bus.New(bus.WithEnforce(bus.ModeStrict))
b := bus.New(bus.WithEnforceFromEnv())            // KIT_BUS_ENFORCE=strict
b := bus.New(bus.WithInvalidTopicReporter(report))
```

To configure: see `docs/adopters/guides/configure-bus-enforcement.md` (task page,
P3). To choose between modes: see `docs/adopters/guides/choose-enforcement-mode.md`
(decision page, P3).

## Topic builder

`bus.TopicOf` is the typed-construction path for new emitters.
`Action` is the terminal method: it validates and returns the final
`bus.Topic`. Bad input panics at the construction site.

```go
import "hop.top/kit/go/runtime/bus"

t := bus.TopicOf("kit", "config", "snapshot").Action("reloaded")
// → "kit.config.snapshot.reloaded"

t := bus.TopicOf("kit", "config", "snapshot").
    Mod("reload").
    Action("failed")
// → "kit.config.snapshot_reload.failed"
```

`bus.PrefixedTopicOf` is a convenience for fixed prefixes with an
optional inline modifier:

```go
bus.PrefixedTopicOf("kit", "config", "snapshot", "reload").Action("failed")
// → "kit.config.snapshot_reload.failed"
```

`bus.PrefixTopics` expands a 3-segment prefix into a `TopicMap` of
past-tense actions. The two paths coexist; pick the one that fits the
emitter's shape.

```go
import "hop.top/kit/go/runtime/bus"

tm, err := bus.PrefixTopics("myapp.runtime.user",
    []string{"created", "updated", "deleted"})
// tm["created"] == "myapp.runtime.user.created"
```

Most adopters do not call `PrefixTopics` directly; they pass a prefix
to a package-level `WithTopicPrefix` option.

## Parsing topics

`bus.ParseTopic` is the inverse of the builder. It validates the
input, splits the Object segment on the first underscore, and returns
a builder plus the action so callers can re-render or retarget:

```go
b, action, err := bus.ParseTopic("kit.config.snapshot_reload.failed")
// b.SourceSeg()   == "kit"
// b.CategorySeg() == "config"
// b.ObjectSeg()   == "snapshot"
// b.ModifierSeg() == "reload"
// action          == "failed"

// Retarget by mutating the builder and re-rendering:
again := b.Mod("retry").Action(action)
// → "kit.config.snapshot_retry.failed"
```

## Qualifiers

The four axes that describe why / how / with-what / during-what live
in the payload, not the topic string. See
[bus-overview.md](../concepts/bus-overview.md#qualifiers-convention)
for the rationale.

```go
type Qualifiers struct {
    Reason       string `json:"reason,omitempty"`       // why
    Mechanism    string `json:"mechanism,omitempty"`    // how
    Property     string `json:"property,omitempty"`     // with-attribute
    Circumstance string `json:"circumstance,omitempty"` // during-context
}
```

Embed `bus.Qualifiers` in any payload struct that wants qualifier
semantics. Both anonymous and named embeds work:

```go
// Anonymous embed (preferred):
type SnapshotReloadFailed struct {
    bus.Qualifiers
    SnapshotID string `json:"snapshot_id"`
}

// Named embed:
type SnapshotReloadFailed struct {
    Q          bus.Qualifiers `json:"qualifiers"`
    SnapshotID string         `json:"snapshot_id"`
}
```

The `Publish` API does not change. Subscribers extract qualifiers
generically via `bus.QualifiersFrom`:

```go
b.Subscribe("kit.config.snapshot_reload.failed", func(ctx context.Context, e bus.Event) error {
    if q, ok := bus.QualifiersFrom(e.Payload); ok {
        // q.Reason, q.Mechanism, q.Property, q.Circumstance
    }
    return nil
})
```

`Qualifiers` fields are opaque strings; adopters define the
controlled vocabulary per event type. An empty `Qualifiers`
JSON-marshals to `{}` because every field carries `omitempty`.

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
| `StdoutSink`  | human-readable — format: `[2006-01-02T15:04:05] topic source: payload`, payload truncated at 120 runes |
| `JSONLSink`   | newline-delimited JSON to writer/file; line keys `topic`, `source`, `timestamp`, `payload` |

```go
sink := bus.NewStdoutSink()             // os.Stdout
sink := bus.NewStdoutSinkWriter(w)      // any io.Writer
sink := bus.NewJSONLSink(w)             // caller owns w; Close flushes only
sink, err := bus.NewJSONLSinkFile("/tmp/events.jsonl") // sink owns the file
```

### TeeBus

Wraps a Bus and fans published events to sinks. Sink errors
reported via `ErrFunc` callback, never block publisher.

`onErr` is variadic and optional. `Close` closes the wrapped bus, then
every sink, and returns the first error.

```go
tee := bus.NewTeeBus(b, []bus.Sink{jsonlSink})          // errors dropped
tee := bus.NewTeeBus(b, []bus.Sink{jsonlSink}, onErr)   // errors reported
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

`bus.New` takes options and defaults to an in-memory adapter:

```go
b := bus.New(
    bus.WithMaxAsync(512),
    bus.WithEnforce(bus.ModeStrict),
)
```

Shipped adapters, selected with `bus.WithAdapter` or `bus.WithNetwork`:

| Adapter          | Constructor                        | Transport |
|------------------|------------------------------------|-----------|
| `MemoryAdapter`  | `bus.NewMemoryAdapter()`           | in-process (default) |
| `SQLiteAdapter`  | `bus.NewSQLiteAdapter(path, ...)`  | SQLite-backed queue |
| `NetworkAdapter` | `bus.WithNetwork(addrs...)`        | WebSocket peers |

```go
b := bus.New(
    bus.WithNetwork("ws://peer-a:9090/bus"),
    bus.WithNetworkOption(
        bus.WithFilter(bus.TopicFilter{Allow: []string{"cluster.#"}}),
        bus.WithOriginID("node-1"),
    ),
)
```

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
