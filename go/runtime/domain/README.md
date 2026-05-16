# domain

Core business entities and state machine logic for kit
applications.

Two emitters publish to the bus:

- `Service[T]` — generic CRUD service, one event per
  Create/Update/Delete
- `StateMachine` — guarded transitions, a pre/post pair per
  successful transition

## Default topics

| Emitter        | Topic                                    | When |
|----------------|------------------------------------------|------|
| `Service[T]`   | `kit.runtime.entity.created`             | after `Create` |
| `Service[T]`   | `kit.runtime.entity.updated`             | after `Update` |
| `Service[T]`   | `kit.runtime.entity.deleted`             | after `Delete` |
| `StateMachine` | `kit.runtime.state.pre_transitioned`     | sync, veto-able, before transition |
| `StateMachine` | `kit.runtime.state.post_transitioned`    | fire-and-forget, after success |

## Adopter rebrand

`Service[T]` — set the 3-segment prefix; CRUD action segments
are appended automatically:

```go
import "hop.top/kit/go/runtime/domain"

svc := domain.NewService(repo,
    domain.WithTopicPrefix[Workspace]("myapp.runtime.workspace"),
)
// emits: myapp.runtime.workspace.{created,updated,deleted}
```

`StateMachine` — same shape, `WithSMTopicPrefix` to avoid the
generic-parameter collision:

```go
sm := domain.NewStateMachine(rules, pub,
    domain.WithSMTopicPrefix("myapp.task.state"),
)
// emits: myapp.task.state.{pre_transitioned,post_transitioned}
```

`WithTopics` / `WithSMTopics` override one action at a time;
empty fields keep the default.

If you also use `runtime/sync.Replicator` and override
`Service[T]`'s prefix, pass the same prefix to
`sync.WithSubscriptionPrefix[T]` so the replicator captures
your entity events.
