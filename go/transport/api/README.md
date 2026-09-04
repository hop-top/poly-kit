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

## Command projection

A tool that registers the `api` service gets a REST projection of its
own command tree for free. No `Expose`, no `MountREST`, no mounting
code: the service reflects the completed cobra tree when it starts and
mounts one route per conformant command, plus an OpenAPI document
describing them.

Reflection happens at **service start**, not at registration, because
that is the first moment the tree is complete.

This is additive. `APIConfig.Handlers` and `APIConfig.Resources` keep
working unchanged, and they are mounted **first**, so an adopter route
always wins a pattern collision.

### Route shape

Everything lives under a versioned prefix:

```text
/v1/commands/<command>/<subcommand>
```

The version is in the path rather than a header because the shape of
the projection is derived from the command tree: adding a required
flag changes a request schema without the adopter touching a route.
A path version gives that churn somewhere to land.

The existing `cmdsurface.MountREST` mount (default prefix `/cmd`,
POST-with-`Invocation`-envelope) is unchanged and stays the explicit,
adopter-driven path. Projection is the automatic one.

### Method selection

| Side-effect class | Method | Rationale |
|-------------------|--------|-----------|
| `read`            | `GET`  | safe and cacheable |
| `write`           | `POST` | not safe, not cacheable |
| `destructive`     | `POST` | as above; also gated by policy |
| `interactive`     | —      | never mounted |

Only two methods. A finer mapping (`PUT` for idempotent writes,
`DELETE` for destructive ones) reads better in isolation but cannot be
honored: kit's vocabulary has no notion of resource identity, so there
is no target for `PUT`/`DELETE` semantics, and a caller seeing
`DELETE` would reasonably expect the URL to name the thing deleted.

### Parameters

| Method | Flags | Positional arguments |
|--------|-------|----------------------|
| `GET`  | query string, typed per the flag's declared type | repeated `?arg=` in order |
| `POST` | `flags` object in a JSON body | `args` array in the same body |

A `POST` also honors query flags so the two can be mixed; the body
wins on conflict, being the more explicit statement. Undeclared query
parameters are ignored (query strings collect tracking junk); an
undeclared flag in a **body** is a `400` naming the flag.

Hidden and deprecated flags are not projected: they are not part of
the supported surface.

### Discovery

`GET /v1/commands` lists **every** reflected command, mounted or not.
Non-invocable entries carry `invocable: false` and a stable reason;
they carry no `method` or `route`, since advertising a route that can
only 404 helps nobody.

```json
{
  "prefix": "/v1/commands",
  "commands": [
    {"name": "list", "invocable": true,
     "method": "GET", "route": "/v1/commands/list"},
    {"name": "shell", "invocable": false, "reason": "interactive"}
  ],
  "reasons": ["interactive"],
  "exit_status": [{"exit_code": 0, "status": 200}]
}
```

Invocable commands sort first. The reason vocabulary is the reflector's
(`interactive`, `unauthorized-destructive`, `hidden-internal`,
`deprecated`, `not-runnable`, `builtin`, `management-only`,
`malformed-schema`) and appears in the OpenAPI document as an enum.

### Policy

Execution goes through the `cmdsurface` `Bridge`, so safety level,
permissions and confirmation are enforced by the same gate every other
surface uses. Interactive and unauthorized-destructive commands are
withheld at mount, not refused per call. See
[the adopter guide](../../../docs/adopters/guides/expose-cli-over-rest.md)
for the task walkthrough.

#### Permit destructive commands on REST

```go
cli.WithAPI(cli.APIConfig{
    Policy: cmdsurface.Policy{
        AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceREST},
    },
})
```

That lifts the transport ceiling only. A command declaring
`kit/requires-confirmation` still runs its own gate, which has no TTY
here, so the call must carry `"flags":{"confirm":"yes"}`; a typed-token
command additionally needs `confirm-token`, and the refusal names the
expected value.

#### Withhold a command from REST

```go
cli.WithAPI(cli.APIConfig{
    Hide: []string{"admin *", "debug dump"},
})
```

Patterns are `"widget add"`, `"widget *"`, or `"*"`. Hidden commands
stay in discovery with `invocable: false` and the reason
`withheld-by-config`, and other surfaces are unaffected. `Expose` is
the allow-list counterpart: empty mounts the whole tree, and `Hide`
is applied after it.

### Exit codes

A command that runs returns its structured output as the response
body, with its exit code mapped to a status:

| Exit | Code | Status | Meaning |
|------|------|--------|---------|
| 0  | `OK`                 | 200 | success |
| 1  | `GENERIC`            | 500 | unclassified failure |
| 2  | `USAGE`              | 400 | the request was wrong |
| 3  | `NOT_FOUND`          | 404 | |
| 4  | `CONFLICT`           | 409 | |
| 5  | `UNAUTHORIZED`       | 403 | see below |
| 6  | `TRANSIENT`          | 503 | retry may clear it |
| 64 | `RATE_LIMITED`       | 429 | |
| 65 | `PROVENANCE_MISSING` | 422 | well-formed, cannot be acted on |

Any other exit code is 500.

`UNAUTHORIZED` maps to **403, not 401**. Auth already ran and passed
before the command executed, so the refusal is about what this
authenticated caller may do — which is 403's meaning. A 401 would
invite a pointless retry with credentials.

Refusals where the command never ran are distinct from exit codes:

| Condition | Status | Code |
|-----------|--------|------|
| command withheld on this surface | 404 | `not_invocable` |
| policy refuses a destructive command | 403 | `destructive_blocked` |

A `not_invocable` body carries the descriptor's reason, which is what
separates "no such command" from "that command exists and is withheld".

An unconfirmed destructive command is refused by the command itself,
not by the projection: it exits `UNAUTHORIZED` and the table above
maps that to `403`, with the command's own message in `stderr`.

### Auth

The projection installs no auth. Routes are registered through the
router, so `APIConfig.Auth` and the rest of the middleware stack wrap
them exactly as they wrap an adopter's own routes — discovery
included.

### OpenAPI

With `WithOpenAPI` configured, every projected operation is described
at `/openapi.json`: parameters or request body per the method, the
declared output schema on `data` where the adopter declared one, the
confirmation flags where a command is gated (`confirm` as an enum of
its accepted values), and `[destructive]` in the summary
so danger is visible in a generated client's method list. Operation
ids are `commands_<path_with_underscores>`.

Only handlers registered on the raw router serve traffic; the huma
registration describes, so middleware wraps exactly one path.

Without `WithOpenAPI`, projection still mounts, and a **minimal** spec
is served at the same `/openapi.json` — enough to find every operation,
its method and its path. Full schemas are what `WithOpenAPI` buys.

Streaming and compat output are out of scope: the response is the
command's structured result.
