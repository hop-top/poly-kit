# Expose your CLI over REST

Serve your cobra tree as a versioned REST API: one route per
conformant command, an OpenAPI document, and no mounting code.

## Who this is for

Developers building a kit CLI who want scripts, services, or a
front end to call their commands over HTTP. Registering the `api`
service is the whole setup — the tree is reflected when the server
starts and every conformant command gets a route. For LLM hosts
calling commands as tools, see
[expose-cli-over-mcp.md](expose-cli-over-mcp.md) instead.

## Before you begin

You need:

- A kit project with a cobra root (see
  [create-cli-project.md](create-cli-project.md))
- `hop.top/kit/go/console/cli` and
  `hop.top/kit/go/transport/cmdsurface` importable
- Commands annotated with `kit/side-effect`; the annotation picks
  the HTTP method and gates what may run remotely

## What you get

`WithAPI` mounts **a REST projection of your whole command tree**:

- **`/v1/commands/<path>`** — one route per invocable command, with
  the method its side-effect class selects
- **`GET /v1/commands`** — a discovery listing of *every* command,
  including the ones that are not mounted and why
- **`/openapi.json`** — an OpenAPI document covering the projected
  routes

**No `Expose` or `MountREST` call is required.** Adding a command to
your tree adds its route the next time the server starts.

Projection is additive. `APIConfig.Handlers` and
`APIConfig.Resources` are mounted first, so your own routes always
win a collision, and everything the projection adds lives under
`/v1/commands` behind your existing auth.

## Steps

### 1. Register the api service

```go
package main

import (
    "context"

    "hop.top/kit/go/console/cli"
)

func main() {
    root := cli.New(cli.Config{Name: "mytool", Version: "1.4.2"},
        cli.WithAPI(cli.APIConfig{Addr: ":8080"}),
    )
    _ = root.Execute(context.Background())
}
```

```bash
mytool serve
```

Every command carrying `kit/side-effect` is now reachable. Nothing
else changes: `--addr` and `--no-auth` work as before.

### 2. Discover the commands

Ask the server what it serves:

```bash
curl -s http://127.0.0.1:8080/v1/commands
```

```json
{
  "tool": "mytool",
  "prefix": "/v1/commands",
  "commands": [
    {
      "name": "widget list",
      "side_effect": "read",
      "invocable": true,
      "method": "GET",
      "route": "/v1/commands/widget/list"
    },
    {
      "name": "shell",
      "side_effect": "interactive",
      "invocable": false,
      "reason": "interactive"
    }
  ],
  "reasons": ["interactive"],
  "exit_status": [{"exit_code": 0, "status": 200}]
}
```

Entries with `"invocable": false` have no route. The `reason` says
why — `interactive`, `unauthorized-destructive`, `hidden-internal`,
`deprecated`, `withheld-by-config` and the rest of the reflector's
vocabulary. Read it before assuming a missing route is a bug.

### 3. Call a read command

A command annotated `kit/side-effect: read` is a `GET`. Flags go in
the query string, positional arguments in repeated `arg`:

```bash
curl -s 'http://127.0.0.1:8080/v1/commands/widget/list?limit=5&all=true'
```

```json
{
  "exit_code": 0,
  "data": {"widgets": [{"id": "w-1"}]},
  "stdout": ""
}
```

Values are converted to the flag's declared type, so `limit=5`
arrives as a number. `data` carries the command's structured output
when it declares an output schema.

### 4. Call a write command

Anything not annotated `read` is a `POST`, with flags and arguments
in a JSON body:

```bash
curl -s -X POST http://127.0.0.1:8080/v1/commands/widget/add \
  -H 'Content-Type: application/json' \
  -d '{"flags":{"force":true},"args":["gadget"]}'
```

```json
{"exit_code": 0, "data": {"id": "w-2"}}
```

The command's exit code sets the HTTP status: `0` is `200`, `2`
(`USAGE`) is `400`, `3` (`NOT_FOUND`) is `404`. See
[the exit-code table](../../../go/transport/api/README.md#exit-codes)
for the full mapping.

### 5. Permit destructive commands

Destructive commands are withheld from REST by default. Calling one
gets a `404`, and discovery explains it:

```json
{"name": "widget delete", "invocable": false,
 "reason": "unauthorized-destructive"}
```

Name the REST surface in `Policy` to permit them:

```go
import "hop.top/kit/go/transport/cmdsurface"

cli.WithAPI(cli.APIConfig{
    Addr: ":8080",
    Policy: cmdsurface.Policy{
        AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceREST},
    },
})
```

The route now exists. A command that also declares
`kit/requires-confirmation` still needs a token on every call —
permitting the tier is not the same as waiving the confirmation:

```bash
curl -s -X POST http://127.0.0.1:8080/v1/commands/widget/delete \
  -H 'X-Confirm-Token: any-non-empty-value' \
  -d '{"args":["w-1"]}'
```

Without the header the call is refused:

```json
{"status": 428, "code": "confirmation_required",
 "message": "X-Confirm-Token header required for this command"}
```

### 6. Keep a command off REST

`Hide` takes command patterns and withholds them from REST only —
the CLI and every other surface keep the command:

```go
cli.WithAPI(cli.APIConfig{
    Addr: ":8080",
    Hide: []string{"admin *", "debug dump"},
})
```

Patterns are `"widget add"` for one command, `"widget *"` for
everything below `widget`, and `"*"` for all of them. Hidden
commands stay in the discovery listing so an operator can see the
decision was deliberate:

```json
{"name": "admin reset", "invocable": false,
 "reason": "withheld-by-config"}
```

Use `Expose` for the opposite posture — an allow-list, where empty
means the whole tree and a non-empty list is the only thing mounted.
`Hide` is applied after `Expose`, so it carves exceptions out of it.

### 7. Read the OpenAPI document

Set `OpenAPI` to get a full document — request and response schemas,
your declared output schemas, the confirmation header where it is
required:

```go
import "hop.top/kit/go/transport/api"

cli.WithAPI(cli.APIConfig{
    Addr:    ":8080",
    OpenAPI: &api.OpenAPIConfig{Title: "mytool", Version: "1.4.2"},
})
```

```bash
curl -s http://127.0.0.1:8080/openapi.json | jq '.paths | keys'
```

Without `OpenAPI` set, projection still mounts and a minimal
document is served at the same path — enough to find every
operation, its method and its path.

### 8. Put it behind auth

`APIConfig.Auth` gates the projected routes and the discovery
endpoint exactly as it gates your own:

```go
import "net/http"

cli.WithAPI(cli.APIConfig{
    Addr: ":8080",
    Auth: func(r *http.Request) (any, error) {
        return validateToken(r.Header.Get("Authorization"))
    },
})
```

The projection installs no auth of its own and no second mechanism.
An unauthenticated call gets `401` before the command runs.

## Option reference

| Option | Default | Effect |
|---|---|---|
| `APIConfig.Addr` | `:8080` | Listen address. `--addr` overrides it. |
| `APIConfig.Auth` | none | Gates every route, projected and adopter-owned. `--no-auth` disables it. |
| `APIConfig.OpenAPI` | nil | Full spec at `/openapi.json`. Unset still serves a minimal one. |
| `APIConfig.Policy` | zero | Zero withholds all destructive commands. `AllowDestructiveOn: [SurfaceREST]` permits them. |
| `APIConfig.Expose` | empty | Empty mounts the whole tree; a non-empty list is an allow-list. |
| `APIConfig.Hide` | empty | Pattern list withheld from REST, applied after `Expose`. |
| `APIConfig.Handlers` | nil | Your own routes, mounted before the projection. |

## What the projection does not implement

Absence here is deliberate — a surface that guessed at these would
be lying about what your commands promise:

- **`PUT` and `DELETE`.** Reads are `GET`, everything else is
  `POST`. Kit's vocabulary has no resource identity, so there is no
  target for their semantics.
- **Interactive commands.** A command tiered `interactive` needs a
  terminal and a human; it is never mounted, and appears in
  discovery with `reason: "interactive"`.
- **Forced remote execution.** Commands the policy refuses stay
  refused. There is no override that runs one anyway.
- **Streaming and compat output.** The response is the command's
  structured result. Streams are not projected.

## Related pages

- [api README](../../../go/transport/api/README.md) — full package
  reference: route shape, mapping tables, discovery payload
- [expose-cli-over-mcp.md](expose-cli-over-mcp.md) — the same tree
  as MCP tools, for LLM hosts
- [serve-lifecycle contract](../../contracts/serve-lifecycle.md) —
  how services start, report ready, and stop
- [cmdsurface README](../../../go/transport/cmdsurface/README.md) —
  the bridge, the policy gate, and every other surface
