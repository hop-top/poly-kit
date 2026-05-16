# Domain Event Topic Catalog

Topic format: `<app>.<entity>.<action>` (dot-separated, per `bus.Topic`).

Wildcard rules (MQTT-style, per `bus/event.go`):
- `*` matches one segment: `aps.profile.*` matches `aps.profile.created`
- `#` matches zero+ trailing segments: `tlc.task.#` matches all task events

Direction: all topics below are **outbound** (published by source app).
Blocking: sync handlers can veto via error return; async handlers never block.

## aps -- Agent Profile Service

| Topic | Blocking | Payload Fields |
|---|---|---|
| `aps.profile.created` | false | `ProfileID string`, `DisplayName string`, `Email string`, `Department string`, `Capabilities []string` |
| `aps.profile.updated` | false | `ProfileID string`, `Fields []string` (changed field names), `Department string` |
| `aps.profile.deleted` | false | `ProfileID string` |
| `aps.adapter.linked` | false | `ProfileID string`, `AdapterType string`, `AdapterID string` |
| `aps.adapter.unlinked` | false | `ProfileID string`, `AdapterType string`, `AdapterID string` |

Source constant prefix: `"aps"`. Wildcard examples:
- `aps.profile.*` -- all profile lifecycle events
- `aps.#` -- everything from aps

## tlc -- Task Lifecycle

| Topic | Blocking | Payload Fields |
|---|---|---|
| `tlc.task.created` | false | `TaskID string`, `Title string`, `TrackID string`, `AssignedTo string`, `Tags []string` |
| `tlc.task.claimed` | false | `TaskID string`, `ClaimedBy string`, `TrackID string` |
| `tlc.task.completed` | false | `TaskID string`, `CompletedBy string`, `TrackID string`, `DurationSec int64` |
| `tlc.task.reopened` | false | `TaskID string`, `ReopenedBy string`, `Note string` |
| `tlc.track.created` | false | `TrackID string`, `Title string`, `Type string` |
| `tlc.track.activated` | false | `TrackID string`, `TriggerTaskID string` |
| `tlc.track.completed` | false | `TrackID string`, `TaskCount int`, `DurationSec int64` |

Source constant prefix: `"tlc"`. Wildcard examples:
- `tlc.task.*` -- all task state changes
- `tlc.track.*` -- all track state changes
- `tlc.#` -- everything from tlc

## ctxt -- Knowledge Engine (dpkms)

| Topic | Blocking | Payload Fields |
|---|---|---|
| `ctxt.object.ingested` | false | `ObjectID string`, `Type string`, `Pipeline string`, `Tags []string`, `ProfileID string`, `DurationMs int64` |
| `ctxt.object.updated` | false | `ObjectID string`, `Type string`, `Fields []string` |
| `ctxt.object.deleted` | false | `ObjectID string` |
| `ctxt.job.completed` | false | `JobID string`, `ObjectCount int`, `DurationMs int64` |
| `ctxt.job.failed` | false | `JobID string`, `Error string`, `ObjectID string` |

Source constant prefix: `"ctxt"`. Wildcard examples:
- `ctxt.object.*` -- all object lifecycle events
- `ctxt.job.*` -- pipeline job outcomes
- `ctxt.#` -- everything from ctxt

## uhp -- Hook Protocol

| Topic | Blocking | Payload Fields |
|---|---|---|
| `uhp.hook.fired` | true | `HookID string`, `Event string`, `CLI string`, `Action string`, `DurationMs int64` |
| `uhp.hook.blocked` | true | `HookID string`, `Event string`, `CLI string`, `Reason string` |

Source constant prefix: `"uhp"`. Notes:
- Both uhp topics are **blocking** (sync handlers); downstream can
  observe/audit hook decisions before they finalize.
- Wildcard: `uhp.hook.*` -- all hook outcomes

## Go Convention

Topic constants and payload structs live in each app's
`internal/events/events.go`, following the pattern in
`kit/go/ai/llm/events.go`:

```go
const (
    TopicProfileCreated bus.Topic = "aps.profile.created"
    TopicProfileUpdated bus.Topic = "aps.profile.updated"
    // ...
)

type ProfileCreatedPayload struct {
    ProfileID    string   `json:"profile_id"`
    DisplayName  string   `json:"display_name"`
    Email        string   `json:"email"`
    Department   string   `json:"department"`
    Capabilities []string `json:"capabilities"`
}
```

Publish after successful state change (never before).
Use `bus.NewEvent(topic, source, payload)` to construct.

## Subscription Matrix

| Subscriber | Subscribes To | Purpose |
|---|---|---|
| ctxt | `aps.profile.*` | auto-provision knowledge profile |
| tlc | `aps.profile.*` | assignee validation |
| ops | `aps.profile.created` | email alias + filter provisioning |

## Transport

- Phase 1-2: `bus.SQLiteAdapter` (single-process, per-app)
- Phase 3: `bus.NetworkAdapter` (WebSocket cross-process via dpkms hub)

## References

- Bus package: `kit/go/runtime/bus/`
- LLM events pattern: `kit/go/ai/llm/events.go`
- Publisher interface: `kit/go/runtime/domain/publisher.go`
- Integration plan: `~/.ops/tracks/domain-bus/plan.md`
