# api

## What it answers

How a kit tool exposes REST: the router and middleware stack, the
bus-integration middleware that publishes one event per request, and
the automatic projection of the tool's own command tree under
`/v1/commands` when it registers the `api` service. Explicit,
adopter-driven mounting of arbitrary surfaces is
`go/transport/cmdsurface`; a Unix socket is `go/transport/socket`.

## Use it when

- build the router and wrap handlers → `api.NewRouter`, `APIConfig.Handlers`, `APIConfig.Resources`
- publish request start and end on the bus → `api.WithBusIntegration(b, ...)`
- rebrand the emitted topics → `api.WithTopicPrefix`, `api.WithTopics`
- get every conformant command as a REST route for free → register the `api` service; no `Expose`, no `MountREST`
- permit a destructive command on REST → `cli.APIConfig{Policy: cmdsurface.Policy{AllowDestructiveOn: ...}}`
- keep a command off REST → `cli.APIConfig{Hide: []string{"admin *"}}`
- describe every projected operation → `WithOpenAPI`, served at `/openapi.json`

## Quick start

```go
import "hop.top/kit/go/transport/api"

r := api.NewRouter(
    api.WithBusIntegration(b,
        api.WithTopicPrefix("myapp.api.request"),
    ),
)
// emits: myapp.api.request.{started,ended}
```

## Contract

- Default topics are `kit.api.request.started` and `kit.api.request.ended`. Earlier releases emitted `api.request.start` / `api.request.end`, both non-conformant; removed with no back-compat alias, subscribers must update. `WithTopics` entries are validated via `bus.ValidateTopic`, which panics on invalid input.
- Projection reflects at **service start**, not registration, and is additive: `Handlers` and `Resources` mount first, so an adopter route always wins a pattern collision.
- Method selection: `read` becomes `GET`, `write` and `destructive` become `POST`, `interactive` is never mounted.
- `GET /v1/commands` lists every reflected command, mounted or not; non-invocable entries carry `invocable: false` and a stable reason, no `method` or `route`.
- Exit codes map to statuses (`0`→200, `2`→400, `3`→404, `4`→409, `5`→403, `6`→503, `64`→429, `65`→422, anything else 500). `UNAUTHORIZED` is 403, not 401.
- Refusals where the command never ran: `not_invocable` (404), `destructive_blocked` (403), `permission_denied` (403).
- The projection installs no auth. The service listens on `127.0.0.1:8080` and refuses a non-loopback address it would serve unauthenticated, at exit `2`, unless `services.api.insecure_remote` opts in.
- Streaming is out of scope: the projection is request/reply.

## Neighbours

- `go/transport/cmdsurface`: the bridge, the policy gate, and the explicit `MountREST` mount (prefix `/cmd`, POST-with-`Invocation`-envelope).
- `go/transport/socket`: the same command tree over a Unix domain socket.
- `go/transport/transportsvc`: the seam for a transport of your own.
- `go/console/cli`: `cli.WithAPI`, `cli.SetOutputSchema`, `cli.WithRootFactory`.

## See also

- [api package reference](../../../docs/adopters/reference/transport-api.md): bus topics, route shape, parameters, discovery, response body, exit-code mapping, refusals, auth claims, request provenance, OpenAPI
- [expose-cli-over-rest.md](../../../docs/adopters/guides/expose-cli-over-rest.md): the task walkthrough
- [secure-remote-serving.md](../../../docs/adopters/guides/secure-remote-serving.md): auth beyond loopback, the permission gate, the audit trail
- [serve lifecycle contract](../../../docs/contracts/serve-lifecycle.md)
