# bus

## What it answers

How does one package tell the rest of the process that something
happened, without knowing who listens? In-process publish/subscribe
with MQTT-style wildcards (`*` one segment, `#` zero or more trailing
segments); optional adapters fan events out to SQLite or to peer
processes. Wrong package for request/response (call the function) and
for durable cross-process queueing (`go/runtime/job`).

## Use it when

- a command or service emits a lifecycle event → `bus.NewEvent` + `Publish`
- another package must react, or veto, that event → `Subscribe` (sync, can veto) or `SubscribeAsync`
- events must reach a log, a file or a human → `bus.NewTeeBus` with a `bus.Sink` (`JSONLSink`, `StdoutSink`, or `go/runtime/notify`)
- you mint topics for a new emitter → `bus.TopicOf(...).Action(...)`, or `bus.PrefixTopics` for a rebrandable prefix
- an event needs why / how / with-what / during-what → embed `bus.Qualifiers` in the payload
- an event must leave as a CloudEvent → `bus.ToCloudEvent` / `bus.MarshalCloudEvent`; peers speak it with `WithFrameFormat(bus.FrameCloudEvents)`

## Quick start

```go
b := bus.New()
defer b.Close(context.Background())

var wg sync.WaitGroup
wg.Add(1)

b.Subscribe("order.created", func(ctx context.Context, e bus.Event) error {
	fmt.Printf("topic=%s source=%s payload=%v\n", e.Topic, e.Source, e.Payload)
	wg.Done()
	return nil
})

_ = b.Publish(context.Background(), bus.NewEvent("order.created", "checkout", "item-42"))
wg.Wait()

// Output:
// topic=order.created source=checkout payload=item-42
```

Verified by `example_test.go` in this directory.

## Contract

- Published topics are exactly four dot-separated segments,
  `[Source].[Category].[Object].[Action]`, each `^[a-z][a-z0-9_]*$`,
  total ≤ 128 chars, Action in past tense; wildcards only in
  subscribe patterns. `Validate` runs on every `Publish`.
- Enforcement mode: `ModeOff`, `ModeWarn` (default: report, then
  publish), `ModeStrict` (report, return `*InvalidTopicError`, drop).
  Precedence: `WithEnforce` > `kit.bus.enforce` config >
  `KIT_BUS_ENFORCE` env > default.
- Sync handlers run in registration order; the first error vetoes.
  Async handlers start only after every sync handler succeeded.
- Qualifiers (`Reason`, `Mechanism`, `Property`, `Circumstance`) travel
  in the payload, never in the topic string.
- After `Close`, `Publish` returns `ErrBusClosed`.
- `NewEvent` mints an `evt_` TypeID. CloudEvents: `type` = topic,
  `workspaceid` = `WorkspaceID`, extensions `[a-z0-9]+` (string, bool,
  int32). Network frames default to the kit format;
  `WithFrameFormat(FrameCloudEvents)` sends CloudEvents (origin in
  `kitorigin`), kit to peers that do not advertise it. Both are read.
- Network streams (auth on both sides): ordered per sender, offsets
  via `StreamPositionFrom`, remote subscribe, resume or
  `kit.runtime.stream.invalidated`; see [bus network streams](../../../docs/contributors/architecture/bus-streams.md).

## Neighbours

- `go/runtime/notify`: outbound sinks (webhook, email, OS-native), filter and retry decorators built on `bus.Sink`.
- `go/runtime/sideeffect`: dry-run wrapper that stamps `Mechanism: "dry_run"` on published payloads.
- `go/transport/api`: WebSocket transport behind `WithNetwork`.

## See also

- [Bus overview](../../../docs/adopters/concepts/bus-overview.md): topic grammar, object modifier, qualifiers, migrating existing emitters
- [Bus API](../../../docs/adopters/reference/bus-api.md): types, builder, `ParseTopic`, qualifiers, enforcement, sinks
- [Hook a CLI into the bus](../../../docs/adopters/guides/hook-cli-into-bus.md)
- [Domain events](../../../docs/adopters/reference/domain-events.md): kit-emitted topic catalog
