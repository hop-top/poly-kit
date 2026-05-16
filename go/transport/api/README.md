# api

REST API exposure and client integration for kit.

Includes a bus-integration middleware that publishes one event
at request start and one at request end (after the handler
returns).

## Default topics

| Topic                          | When |
|--------------------------------|------|
| `kit.api.request.started`      | before handler runs |
| `kit.api.request.ended`        | after handler returns |

> Breaking change in this release. Prior to T-0122 the
> middleware emitted `api.request.start` and `api.request.end`
> — both non-conformant (3 segments, present-tense). The old
> topics have been removed with no back-compat alias.
> Subscribers MUST update.

## Adopter rebrand

```go
import "hop.top/kit/go/transport/api"

r := api.NewRouter(
    api.WithBusIntegration(b,
        api.WithTopicPrefix("myapp.api.request"),
    ),
)
// emits: myapp.api.request.{started,ended}
```

`WithTopics` overrides individual topics; non-empty entries are
validated via `bus.ValidateTopic` (panics on invalid input).
