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
| `ID`          | `string`     | `id,omitempty`          | `evt_` TypeID, minted by `NewEvent` |
| `Subject`     | `string`     | `subject,omitempty`     | CloudEvents subject |
| `DataContentType` | `string` | `datacontenttype,omitempty` | media type of `Payload`; when empty, a CloudEvent gets `application/json` for a JSON payload and none for `[]byte` |
| `DataSchema`  | `string`     | `dataschema,omitempty`  | schema URI for `Payload` |
| `Extensions`  | `map[string]any` | `extensions,omitempty` | CloudEvents extension attributes |

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

The convention for choosing each segment, and the topics kit itself
emits, are in [event-topics.md](../../contracts/event-topics.md).

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

Kit-format network frames carry `workspace_id` and `id` when set;
frames without them (global events, older peers) decode as global with
an empty id. CloudEvents frames carry the workspace in the
`workspaceid` extension instead.

## CloudEvents

`bus.ToCloudEvent(e)` / `bus.FromCloudEvent(ce)` map an `Event` to and
from a CloudEvents 1.0 `CloudEvent`; `bus.MarshalCloudEvent` /
`bus.UnmarshalCloudEvent` do the same through structured-mode JSON.

| CloudEvents     | Event |
|-----------------|-------|
| `specversion`   | always `"1.0"` |
| `id`            | `ID`; minted (`evt_` TypeID) when empty |
| `source`        | `Source` |
| `type`          | `Topic`, verbatim |
| `subject`       | `Subject` |
| `time`          | `Timestamp`, UTC RFC 3339; unset when zero |
| `datacontenttype` | `DataContentType`; `application/json` when data is JSON and none set |
| `dataschema`    | `DataSchema` |
| `data`          | `Payload` as JSON; unset when `Payload` is nil. A non-JSON `DataContentType` needs a `string` or `[]byte` payload |
| `data_base64`   | `Payload` of type `[]byte` |
| `workspaceid`   | `WorkspaceID` |
| other extensions | `Extensions` |

Extension names are lowercase ASCII letters and digits; values are
string, bool or an integer in int32 range. `workspaceid` is reserved
for `WorkspaceID`, and `kitorigin` and `kitoffset` for the network
adapter. kit has no
alias option for the workspace extension: a consumer that wants another
name (a tenant id, say) maps `workspaceid` itself. `time` follows RFC
3339 with years 0000-9999, a `.` fraction of 1-9 digits and no leap
seconds. Violations return a `*bus.CloudEventError` that matches
`bus.ErrInvalidCloudEvent`. The full rule set is pinned by the vectors
in [`contracts/bus-v1/cloudevents`](../../../contracts/bus-v1/cloudevents/).

The network adapter sends kit frames by default, in every SDK. Opt in
to CloudEvents frames per adapter:

```go
bus.WithNetworkOption(bus.WithFrameFormat(bus.FrameCloudEvents))
```

On a link with a handshake (an `Authenticator` on both sides) the
format is negotiated: a peer that does not advertise `cloudevents` gets
kit frames. Without a handshake the configured format is sent as is, so
opt in there only once every peer decodes CloudEvents frames.

Inbound frames are accepted in either format whatever the setting: a
JSON object with a member named exactly `specversion` is a CloudEvents
frame, anything else a kit frame.
CloudEvents frames carry the loop-prevention origin in the `kitorigin`
extension and, on stream links, the offset in `kitoffset`; the adapter
strips both before delivery.

## Network streams

Two adapters that both configure an `Authenticator` negotiate a stream
link in the auth handshake (use `bus.OpenAuth()` when there is no
secret). On a stream link:

- every forwarded event carries an offset in the sender's stream,
  delivered to handlers as metadata: `bus.StreamPositionFrom(ctx)`
- a dialer subscribes by pattern and workspace scope:
  `WithSubscribe`, `WithScope`, `(*NetworkAdapter).Subscribe`,
  `Unsubscribe`, `SetScope`; the acceptor authorizes with
  `WithSubscribeAuthorizer` and filters per link with `WithLinkFilter`
- a reconnect resumes after the dialer's position with no gaps and no
  duplicates; when that is impossible the dialer publishes
  `kit.runtime.stream.invalidated`: mark live views fed by that peer
  stale and refetch
- the acceptor sends heartbeats (`WithHeartbeat`, default 15 s, 1 s to
  24 h); a
  dialer closes a link after three silent intervals and reconnects
- link state: `kit.runtime.link.connected` / `disconnected` events and
  `(*NetworkAdapter).LinkState(addr)`

```go
b := bus.New(
    bus.WithNetwork("wss://hub.example/bus"),
    bus.WithNetworkOption(
        bus.WithAuth(bus.OpenAuth()),
        bus.WithSubscribe("acme.crm.#"),
        bus.WithScope(bus.Scope{Workspaces: []string{"ws1"}}),
        bus.WithOutbox(),
    ),
)
b.Subscribe(string(bus.TopicStreamInvalidated), func(ctx context.Context, e bus.Event) error {
    // mark views stale, refetch
    return nil
})
```

Every adapter keeps a log of what it forwards (`WithStreamLog(n)`,
default 1024 entries, or `WithLog` for your own `StreamLog`); size a
hub's log for its clients' reconnect window. `WithOutbox()` keeps
events published while an outbound peer is down and sends them in
order on reconnect; overflow drops the oldest and counts them
(`OutboxDropped`, `WithOutboxDropFunc`). A failed first dial through
`WithNetwork` or `Dial` keeps retrying.

Ordering holds per sending adapter through sync handlers; async
handlers stay unordered. A consumer that queues events onward must
persist the position it applied, from `StreamPositionFrom`, not
`Position(addr)`. A bus without the ordered publish hook (for example
the SQLite adapter) still forwards through the log and per-link
writers, but appends from an async handler, so its order is not
guaranteed; it advertises no streams and every link it makes is a
legacy link.

`Subscribe`, `Unsubscribe` and `SetScope` wait for the acceptor's
reply, which only the link's own reader can deliver. Called with the
context of a sync handler that link is delivering (`link.connected`,
`stream.invalidated`, or an event from that peer) they return an error
wrapping `bus.ErrReentrantRequest` instead of blocking. To react to
those events with a request, subscribe the handler with
`SubscribeAsync` (on kit's buses, async handlers run off the
delivering goroutine without the marker and may pass their own
context), or start a goroutine with a context not derived from the
handler's. A sync handler that calls with an unrelated context blocks
until the link closes, then gets an error. A custom `Bus` that runs
async handlers with the publish context must drop the marker itself,
or build their context from `context.Background()`.

Async handlers share the bus's bounded pool (`WithMaxAsync`). If every
slot holds a handler waiting on a reply from one link, that link's
reader blocks on the pool when it publishes the next event, so the
reply never arrives: the stall lasts until the link closes (on a
stream link, the dialer's watchdog closes it after three heartbeat
intervals). Keep request-making async handlers well below the pool
size, or bound their requests with a context deadline.

`Subscribe`, `Unsubscribe` and `SetScope` need a registered address
(`Connect`, `Dial` or `WithNetwork`); after `Disconnect` they return
`bus.ErrDisconnected`. Use `WithSubscribe` and `WithScope` for the set a
first connect asks for.

Subscribe authorizers (`WithSubscribeAuthorizer`) run on the link's
writer, which keeps the ack's offset an exact fence. They must be fast:
a slow authorizer delays events and heartbeats on that link, and one
slower than three heartbeat intervals gets the link closed by the
dialer.

Handshake failures: a refused session (`*bus.StreamError`) or a
malformed reply (a protocol error) is final, so the address is not
retried until the next explicit `Connect`. A rejected token
(`bus.ErrAuthFailed`, code `auth`) and a missing ack (code `timeout`)
are retried by a reconnect loop, which `WithNetwork`, `Dial` or a
dropped link starts; a first `Connect` that fails starts none, so use
`Dial` to keep trying. Each failure is reported through
`WithNetworkErrorFunc` and as `kit.runtime.link.disconnected` with
`final` and `code` (`protocol`, `auth`, `timeout`, or the session error
code). A `Connect` that loses a race with `Disconnect` returns
`bus.ErrDisconnected`.

### Behaviour changes

These change how the network adapter behaves for existing adopters:

- Forwarding is ordered per sending adapter: one writer per link reads
  the adapter's log, instead of one goroutine per event.
- An outbox link resends a frame whose write failed (a possible
  duplicate); frames written but lost in transit are not resent.
- A slow legacy link skips entries the log evicted before its writer
  reached them, instead of back-pressuring publishers. Skips are
  counted (`(*NetworkAdapter).Skipped(addr)`, `WithSkipFunc`) and
  reported through `WithNetworkErrorFunc`.
- `Disconnect(addr)` forgets the address: no reconnect, and its outbox
  and position are dropped.
- A failed first dial through `WithNetwork` (or `Dial`) keeps retrying
  in the background, unless the failure is final.
- `FrameCloudEvents` falls back to kit frames on a handshake link whose
  peer does not advertise CloudEvents.
- A reconnect that cannot resume publishes
  `kit.runtime.stream.invalidated` (reason `unsupported` on links
  without streams), and outbound links publish
  `kit.runtime.link.connected` / `disconnected`.
- `kitoffset` is a reserved CloudEvents extension, like `kitorigin`.
The wire contract is
[bus network streams](../../contributors/architecture/bus-streams.md),
pinned by [`contracts/bus-v1/streams`](../../../contracts/bus-v1/streams/README.md).

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
