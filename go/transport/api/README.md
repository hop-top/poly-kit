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
- guard every route, huma's and unmatched paths included → `api.WithOuterMiddleware`; `api.WithMiddleware` wraps only routes registered through `Handle` and `Mount`
- publish request start and end on the bus → `api.WithBusIntegration(b, ...)`
- rebrand the emitted topics → `api.WithTopicPrefix`, `api.WithTopics`
- get every conformant command as a REST route for free → register the `api` service; no `Expose`, no `MountREST`
- permit a destructive command on REST → `cli.APIConfig{Policy: cmdsurface.Policy{AllowDestructiveOn: ...}}`
- keep a command off REST → `cli.APIConfig{Hide: []string{"admin *"}}`
- describe every projected operation → `WithOpenAPI`, served at `/openapi.json`
- stream a long-running command's output as it is written → `<route>/stream`, server-sent events, same method and parameters
- keep browser pages out (DNS rebinding, cross-site writes) → `api.HostCheck`, `api.ListenerHosts`, `api.OriginCheck`; on by default on kit's HTTP listeners (`api`, `mcp`, `rpc`)
- set hardening response headers → `api.SecurityHeaders`

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
- Exit codes map to statuses (`0`→200, `2`→400, `3`→404, `4`→409, `5`→403, `6`→503, `7`→403, `64`→429, `65`→422, `70`→503, anything else 500). `UNAUTHORIZED` is 403, not 401. `CONSENT_REFUSED` (7) shares 403: re-send the same call with `confirm`. `PREREQUISITE` (70) is 503, not 500 — a declared dependency is unreachable, so the identical call succeeds once an operator repairs it.
- Refusals where the command never ran: `not_invocable` (404), `destructive_blocked` (403), `insufficient_scope` (403, with `WWW-Authenticate: Bearer error="insufficient_scope", scope="…"`), `permission_denied` (403).
- The projection installs no auth. The service listens on `127.0.0.1:8080` and refuses a non-loopback address it would serve unauthenticated, at exit `2`, unless `services.api.insecure_remote` opts in.
- Every invocable command also streams at `<route>/stream` (`api.StreamSuffix`), on the same method with the same parameters, as `text/event-stream`: `event` frames per output line, then one terminal `result` (the response body plus the mapped `status`) or `error` frame, with a `: ping` keep-alive every 15s. Refusals the transport or bridge make (400, 401, 404, 403) are statuses before the stream opens; the command's own outcomes, its confirmation refusal included, are the terminal frame. Disconnecting cancels the command; stopping the service ends open streams with a `503` `shutting_down` error frame. Mounted only when the executor implements `api.CommandStreamer`; the `api` service's does.
- Long-running work: SSE here, `InvokeStream` on `cmdsurface.MountRPC`, the experimental MCP tasks extension (`mcpsdk.WithTasks`) for work that must outlive its caller. The comparison lives in the [reference](../../../docs/adopters/reference/transport-api.md#long-running-work-on-each-transport).
- The `api` service refuses a `Host` its listener does not answer for (`403` `host_rejected`) and a cross-origin browser write (`403` `origin_rejected`), and sets `nosniff`, `no-referrer` and a deny-all CSP on every response. Keys: `services.api.host_check`, `origin_check`, `security_headers`.

## Neighbours

- `go/transport/cmdsurface`: the bridge, the policy gate, and the explicit `MountREST` mount (prefix `/cmd`, POST-with-`Invocation`-envelope).
- `go/transport/socket`: the same command tree over a Unix domain socket.
- `go/transport/transportsvc`: the seam for a transport of your own.
- `go/console/cli`: `cli.WithAPI`, `cli.SetOutputSchema`, `cli.WithRootFactory`.

## See also

- [api package reference](../../../docs/adopters/reference/transport-api.md): bus topics, route shape, parameters, discovery, response body, exit-code mapping, refusals, streaming and long-running work, auth claims, request provenance, transport guards, OpenAPI
- [expose-cli-over-rest.md](../../../docs/adopters/guides/expose-cli-over-rest.md): the task walkthrough
- [secure-remote-serving.md](../../../docs/adopters/guides/secure-remote-serving.md): auth beyond loopback, the permission gate, the audit trail
- [serve lifecycle contract](../../../docs/contracts/serve-lifecycle.md)
