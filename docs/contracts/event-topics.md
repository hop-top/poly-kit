# Event Topics Convention

> 4-segment past-tense bus topics, configurable per-emitter.
> Authority: [`bus.ValidateTopic`](../../go/runtime/bus/topics.go).

Kit emitters publish to a structured bus. Every topic follows a
fixed 4-segment shape so subscribers can filter, route, and
audit deterministically. This page is the cross-reference for
adopters wiring kit-emitting packages into their own
applications.

## Convention

```
[Source].[Category].[Object].[Action]
```

Rules (enforced by `bus.ValidateTopic`):

- exactly **4** dot-separated segments
- segments are lowercase ASCII letters, digits, or underscores
- no empty segments, no leading or trailing dot
- the **action** segment is past-tense — either ends in `"ed"`
  or appears in the `pastTenseWhitelist`

| Segment | Meaning                                    | Examples              |
|---------|--------------------------------------------|-----------------------|
| Source  | system / domain owning the event           | `kit`, `wsm`, `myapp` |
| Category| logical grouping within the source         | `runtime`, `ai`, `core` |
| Object  | entity type that changed                   | `entity`, `request`, `breaker` |
| Action  | past-tense verb describing what happened   | `created`, `tripped`, `received` |

Multi-word actions use snake_case (`pre_transitioned`,
`half_opened`).

### Past-tense whitelist

Most actions are recognized by the `"ed"` suffix heuristic.
Irregular past-tense forms and a few in-flight participles
(`started`, `applied`, `selected`, `tripped`, `half_opened`,
…) live in
[`pastTenseWhitelist`](../../go/runtime/bus/topics.go) — extend
that list when introducing a new verb that doesn't fit the
heuristic.

## Defaults

Every kit emitter ships with a conformant `DefaultTopics`
struct. If you only consume kit events alongside other kit
events, you do not need to override anything.

| Package                      | Default prefix              | Topics |
|------------------------------|-----------------------------|--------|
| `runtime/domain.Service[T]`  | `kit.runtime.entity`        | `created`, `updated`, `deleted` |
| `runtime/domain.StateMachine`| `kit.runtime.state`         | `pre_transitioned`, `post_transitioned` |
| `ai/llm.Client`              | `kit.ai`                    | `request.started`, `response.received`, `request.errored`, `fallback.applied`, `route.selected`, `eva.evaluated` |
| `ai/ext/hook.Bus`            | `kit.ext.hook`              | `<action>` (adopter-defined) |
| `transport/api`              | `kit.api.request`           | `started`, `ended` |
| `core/breaker.Breaker`       | `kit.core.breaker`          | `tripped`, `opened`, `closed`, `half_opened` |
| `core/upgrade.Checker`       | `kit.core.upgrade`          | `released`, `downloaded`, `installed`, `snoozed` |

`core/breaker` and `core/upgrade` are bus-emitting since
T-0123/T-0124 — they require `WithPublisher` to opt in. The
other emitters publish unconditionally when wired to a bus.

## When to override

Override the prefix when:

- you consume kit events alongside non-kit publishers on the
  same bus and want a flat namespace under your application's
  source segment
- the topic shape is part of a public contract you ship (your
  own SDK, an event log, an audit pipeline)
- multiple instances of the same emitter need to be
  distinguishable on the bus (e.g. one breaker per upstream
  service)

Stay on defaults when:

- the bus is in-process and only consumed by other kit code
- you have a single instance of each emitter
- you do not export topic strings to external systems

## Adopter recipes

All examples below use `WithTopicPrefix` (or its package-named
equivalent) — the most common form. For finer control, every
package also exposes `WithTopics(...)` to override one action
at a time.

### Service[T] — entity CRUD

```go
import "hop.top/kit/go/runtime/domain"

svc := domain.NewService(repo,
    domain.WithTopicPrefix[Workspace]("myapp.runtime.workspace"),
)
// emits: myapp.runtime.workspace.{created,updated,deleted}
```

### StateMachine — transition phases

```go
import "hop.top/kit/go/runtime/domain"

sm := domain.NewStateMachine(rules, pub,
    domain.WithSMTopicPrefix("myapp.task.state"),
)
// emits: myapp.task.state.{pre_transitioned,post_transitioned}
```

### llm.Client — AI request lifecycle

```go
import "hop.top/kit/go/ai/llm"

c := llm.NewClient(provider,
    llm.WithTopicPrefix("myapp.ai"),
)
// emits: myapp.ai.request.started, myapp.ai.response.received,
//        myapp.ai.request.errored, myapp.ai.fallback.applied,
//        myapp.ai.route.selected,  myapp.ai.eva.evaluated
```

`llm` keeps the `object.action` suffix (`request.started`,
`response.received`, etc.) — only the 2-segment
`source.category` prefix is rebrandable. This preserves the
non-uniform action vocabulary (`request.*` vs `response.*` vs
`eva.*`) by design.

### hook — lifecycle hooks

```go
import "hop.top/kit/go/ai/ext/hook"

b := hook.NewBus(
    hook.WithHookTopicPrefix("myapp.hooks.lifecycle"),
)
// emits: myapp.hooks.lifecycle.<action>
```

Hook actions are open-ended adopter-defined strings. Validation
is best-effort at fire time — non-past-tense actions skip the
publish rather than fail construction.

### transport/api — REST middleware

```go
import "hop.top/kit/go/transport/api"

r := api.NewRouter(
    api.WithBusIntegration(b,
        api.WithTopicPrefix("myapp.api.request"),
    ),
)
// emits: myapp.api.request.{started,ended}
```

Note: `transport/api` is **the breaking change** in this
release. Topics moved from non-conformant
`api.request.{start,end}` (3 segments, present-tense) to the
4-segment past-tense default `kit.api.request.{started,ended}`.
Subscribers MUST update.

### core/breaker — runtime fuses

```go
import "hop.top/kit/go/core/breaker"

b := breaker.New("api",
    breaker.WithPublisher(pub),
    breaker.WithTopicPrefix("myapp.core.breaker"),
)
// emits: myapp.core.breaker.{tripped,opened,closed,half_opened}
```

`WithPublisher` is REQUIRED for any bus emission — without it
the breaker still logs via slog but never publishes.

### core/upgrade — version lifecycle

```go
import "hop.top/kit/go/core/upgrade"

c := upgrade.New(
    upgrade.WithPublisher(pub),
    upgrade.WithTopicPrefix("myapp.core.upgrade"),
)
// emits: myapp.core.upgrade.{released,downloaded,installed,snoozed}
```

Same `WithPublisher` rule as breaker.

## Subscription patterns

Prefer **suffix matching** on the action segment for any
dispatch logic. Action vocabulary is stable across prefixes by
design, so suffix-based switches survive any adopter rename.

```go
b.Subscribe("myapp.runtime.workspace.*", func(_ context.Context, e bus.Event) error {
    s := string(e.Topic)
    action := s[strings.LastIndex(s, ".")+1:]
    switch action {
    case "created":  // ...
    case "updated":  // ...
    case "deleted":  // ...
    }
    return nil
})
```

This is the pattern used by
[`runtime/sync.Replicator`](../../go/runtime/sync/replicator.go),
which exposes
[`WithSubscriptionPrefix`](../../go/runtime/sync/replicator.go)
so adopters that override `domain.Service[T]`'s prefix can
keep replication working without re-binding handlers.

## Validating new topics

Three construction paths, in order of preference:

1. **`bus.TopicOf` builder** — typed, chainable, validates on
   `Action(...)`. Recommended for new emitters that publish a
   handful of related events:

   ```go
   import "hop.top/kit/go/runtime/bus"

   topic := bus.TopicOf("myapp", "runtime", "user").Action("created")
   //   → "myapp.runtime.user.created"
   ```

   The builder also accepts an optional snake_case `Mod` joined
   into the Object segment with `_`, so a sub-classified Object
   stays one segment on the wire:

   ```go
   bus.TopicOf("kit", "config", "snapshot").Mod("reload").Action("failed")
   //   → "kit.config.snapshot_reload.failed"
   ```

2. **`bus.PrefixTopics`** — expands a 3-segment prefix into a
   `TopicMap` of validated 4-segment topics. Useful when the
   action set is enumerated and the prefix is fixed:

   ```go
   bus.PrefixTopics("wsm.runtime.workspace", []string{"created", "updated"})
   ```

3. **`bus.ValidateTopic`** — call directly when you have a
   pre-built string and just want to assert it conforms:

   ```go
   if err := bus.ValidateTopic("myapp.runtime.user.created"); err != nil {
       panic(err)
   }
   ```

For the inverse — parsing a topic string back into source /
category / object / modifier / action — use `bus.ParseTopic`.

### Qualifiers belong in the payload

Reason / mechanism / property / circumstance metadata stays on
the event payload, not in the topic string. Embed
`bus.Qualifiers` in your payload struct to make these fields
discoverable by observability code:

```go
type ReloadFailed struct {
    bus.Qualifiers
    SourcePaths []string `json:"source_paths"`
}
```

Topics function as routing keys; encoding qualifiers into the
topic explodes the routing tree, fragments metric series, and
breaks pinned subscribers when a new qualifier appears. See
[ADR-0017](../adr/0017-bus-topic-naming-and-qualifiers.md) for
the full rationale.

## Cross-references

- [`bus.ValidateTopic` GoDoc](../../go/runtime/bus/topics.go)
- [`bus.TopicOf` builder](../../go/runtime/bus/builder.go)
- [`bus.ParseTopic` inverse](../../go/runtime/bus/builder.go)
- [`bus.Qualifiers` payload struct](../../go/runtime/bus/qualifiers.go)
- [`bus.PrefixTopics` GoDoc](../../go/runtime/bus/topics.go)
- [`bus.pastTenseWhitelist`](../../go/runtime/bus/topics.go)
- [Bus API Reference](../adopters/reference/bus-api.md)
- [Domain events guide](../adopters/reference/domain-events.md)
- [`runtime/sync.Replicator`](../../go/runtime/sync/replicator.go)
- [ADR-0017 Bus topic naming and Qualifiers](../adr/0017-bus-topic-naming-and-qualifiers.md)
- [RELEASING.md](../releasing.md) — current release notes
