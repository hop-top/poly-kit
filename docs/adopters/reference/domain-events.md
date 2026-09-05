# Domain Event Topic Catalog

Catalog of the bus topics kit packages publish by default. Every topic
follows the 4-segment form `[Source].[Category].[Object].[Action]` — see
[bus-api.md](bus-api.md) for the grammar and the two validators.

Each package exposes its topics as a `Topics` struct plus a
`DefaultTopics` value. Adopters override one action with `WithTopics`, or
re-prefix the whole set with `WithTopicPrefix` (3-segment prefix,
`source.category.object`). Empty fields in a `WithTopics` value fall back
to the package default.

## `go/ai/llm`

`llm.Topics`, defaults in `llm.DefaultTopics`. Package-level vars
(`llm.TopicRequestStart`, …) read from the same struct.

| Field          | Default topic                | Payload struct         |
|----------------|------------------------------|------------------------|
| `RequestStart` | `kit.ai.request.started`     | `RequestStartPayload`  |
| `RequestEnd`   | `kit.ai.response.received`   | `RequestEndPayload`    |
| `RequestError` | `kit.ai.request.errored`     | `RequestErrorPayload`  |
| `Fallback`     | `kit.ai.fallback.applied`    | `FallbackPayload`      |
| `Route`        | `kit.ai.route.selected`      | `RoutePayload`         |
| `EvaResult`    | `kit.ai.eva.evaluated`       | `EvaResultPayload`     |

Payload fields:

| Struct                | Fields |
|-----------------------|--------|
| `RequestStartPayload` | `Request Request` |
| `RequestEndPayload`   | `Response Response`, `Duration time.Duration` |
| `RequestErrorPayload` | `Err error` (json `-`), `ErrMessage string` (json `error`) |
| `FallbackPayload`     | `From int`, `To int`, `Err error` (json `-`), `ErrMessage string` (json `error`) |
| `RoutePayload`        | `Router string`, `Score float64`, `Model string` |
| `EvaResultPayload`    | `Contract string`, `Passed bool`, `Violations []string` |

`Err` is `json:"-"`: it does not cross a process boundary. Cross-process
subscribers read `ErrMessage`.

## `go/runtime/domain` — Service[T] CRUD

`domain.Topics`, defaults in `domain.DefaultTopics`.

| Field          | Default topic                        | Phase |
|----------------|--------------------------------------|-------|
| `PreValidated` | `kit.runtime.entity.pre_validated`   | sync, before validation; error vetoes |
| `PrePersisted` | `kit.runtime.entity.pre_persisted`   | sync, after validation and before the repo write; error vetoes |
| `Created`      | `kit.runtime.entity.created`         | post-write, best effort |
| `Updated`      | `kit.runtime.entity.updated`         | post-write, best effort |
| `Deleted`      | `kit.runtime.entity.deleted`         | post-write, best effort |

Pre-events are shared across create, update and delete. Their payload
carries an `Op` field so subscribers discriminate on
`payload.op == "delete"`. Subscriber errors on the post events are
swallowed: they are notifications, not gates.

## `go/runtime/domain` — StateMachine

`domain.StateMachineTopics`, defaults in
`domain.DefaultStateMachineTopics`.

| Field              | Default topic                             | Phase |
|--------------------|-------------------------------------------|-------|
| `PreTransitioned`  | `kit.runtime.state.pre_transitioned`      | sync, veto-able |
| `PostTransitioned` | `kit.runtime.state.post_transitioned`     | fire-and-forget |

## `go/core/stage`

`stage.Topics`, defaults in `stage.DefaultTopics`. Validated by
`bus.ValidateTopic` in `init()`, so a typo in a default fails at load.

| Field          | Default topic                     |
|----------------|-----------------------------------|
| `Proposed`     | `kit.runtime.stage.proposed`      |
| `Transitioned` | `kit.runtime.stage.transitioned`  |
| `Entered`      | `kit.runtime.stage.entered`       |
| `Expired`      | `kit.runtime.stage.expired`       |
| `Violated`     | `kit.runtime.stage.violated`      |

## `go/core/upgrade`

`upgrade.Topics`, defaults in `upgrade.DefaultTopics`.

| Field        | Default topic                    | Meaning |
|--------------|----------------------------------|---------|
| `Released`   | `kit.core.upgrade.released`      | `Check` observed a new latest version |
| `Downloaded` | `kit.core.upgrade.downloaded`    | asset fetched |
| `Installed`  | `kit.core.upgrade.installed`     | running binary replaced |
| `Snoozed`    | `kit.core.upgrade.snoozed`       | user deferred the notification |

## `go/core/breaker`

`breaker.Topics`, defaults in `breaker.DefaultTopics`.

| Field        | Default topic                      |
|--------------|------------------------------------|
| `Tripped`    | `kit.core.breaker.tripped`         |
| `Opened`     | `kit.core.breaker.opened`          |
| `Closed`     | `kit.core.breaker.closed`          |
| `HalfOpened` | `kit.core.breaker.half_opened`     |

## `go/core/config` — reloadable snapshots

`config.ReloadTopics`, defaults in `config.DefaultReloadTopics`.

| Field          | Default topic                          |
|----------------|----------------------------------------|
| `Reloaded`     | `kit.config.snapshot.reloaded`         |
| `ReloadFailed` | `kit.config.snapshot.reload_failed`    |

## `go/transport/api` — HTTP middleware

`api.Topics`, defaults in `api.DefaultTopics`.

| Field          | Default topic               |
|----------------|-----------------------------|
| `RequestStart` | `kit.api.request.started`   |
| `RequestEnd`   | `kit.api.request.ended`     |

The middleware previously emitted `api.request.start` and
`api.request.end`. Both were non-conformant (3 segments, present tense)
and were removed with no back-compat alias.

## Overriding topics

```go
// Re-prefix a whole set from a 3-segment prefix.
svc := domain.NewService(repo,
    domain.WithTopicPrefix[Workspace]("wsm.runtime.workspace"))

// Override a single action; the rest keep their defaults.
svc := domain.NewService(repo,
    domain.WithTopics[Workspace](domain.Topics{
        Created: "wsm.runtime.workspace.created",
    }))
```

`WithTopicPrefix` panics when the prefix fails `bus.PrefixTopics`.
Constructors are wired at boot, so a bad prefix is a programmer error;
failing loudly beats silently falling back and leaving subscribers with
no events.

## Publishing

`domain.EventPublisher` is the interface kit packages publish through:

```go
type EventPublisher interface {
    Publish(ctx context.Context, topic, source string, payload any) error
}
```

Implementations may wrap `bus` or any other pub/sub. Publish after a
successful state change, never before.

## Related pages

- [`bus-api.md`](bus-api.md) — bus package reference, topic grammar
- [`docs/adopters/concepts/bus-overview.md`](../concepts/bus-overview.md) — concepts
