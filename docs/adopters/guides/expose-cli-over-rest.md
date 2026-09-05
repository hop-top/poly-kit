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
        cli.WithAPI(cli.APIConfig{}), // listens on 127.0.0.1:8080
    )
    _ = root.Execute(context.Background())
}
```

```bash
mytool serve
```

Every command carrying `kit/side-effect` is now reachable from this
machine. The default address is loopback; to reach the API from
another host, put it behind `Auth` first — see step 8 and
[secure-remote-serving.md](secure-remote-serving.md).

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
    },
    {
      "name": "serve",
      "side_effect": "write",
      "invocable": false,
      "reason": "self-hosting"
    }
  ],
  "reasons": ["interactive", "self-hosting"],
  "exit_status": [{"exit_code": 0, "status": 200}]
}
```

Entries with `"invocable": false` have no route. The `reason` says
why — `interactive`, `self-hosting`, `unauthorized-destructive`,
`hidden-internal`, `deprecated`, `withheld-by-config` and the rest of
the reflector's vocabulary. Read it before assuming a missing route
is a bug. `serve` is always `self-hosting`: it is the process you are
talking to.

### 3. Get structured output

A command that declares an output schema answers in `data` — its
output decoded, not its text. Declare the schema with
`cli.SetOutputSchema` and render through `output.Dispatch`, the same
way the command renders on the CLI:

```go
package main

import (
    "context"
    "log"

    "github.com/spf13/cobra"

    "hop.top/kit/go/console/cli"
    "hop.top/kit/go/console/output"
)

// Widget is one row of `widget list`. The json tags name the fields
// in data; the table tags name the columns on the CLI.
type Widget struct {
    ID   string `json:"id" table:"ID"`
    Name string `json:"name" table:"NAME"`
}

func main() {
    root := cli.New(cli.Config{Name: "mytool", Version: "1.4.2"},
        cli.WithStatus(cli.StatusConfig{}),
        cli.WithAPI(cli.APIConfig{Addr: ":8080"}),
    )

    list := &cobra.Command{
        Use:   "list",
        Short: "List widgets",
        Long:  "List every widget.",
        RunE: func(cmd *cobra.Command, _ []string) error {
            widgets := []Widget{{ID: "w-1", Name: "bolt"}}
            return output.Dispatch(cmd, root.Viper, widgets)
        },
    }
    cli.SetSideEffect(list, cli.SideEffectRead)
    err := cli.SetOutputSchema(list, cli.OutputSchema{Type: &[]Widget{}, Version: "1.0"})
    if err != nil {
        log.Fatal(err)
    }

    widget := &cobra.Command{Use: "widget", Short: "Manage widgets"}
    widget.AddCommand(list)
    root.Cmd.AddCommand(widget)

    if err := root.Execute(context.Background()); err != nil {
        log.Fatal(err)
    }
}
```

`WithStatus` mounts the `status` command every kit root is validated
to have. On the CLI this prints a table; `--format=json` prints the
JSON. Over REST the projection runs the command as `--format=json`
and decodes what it wrote:

```bash
curl -s http://127.0.0.1:8080/v1/commands/widget/list
```

```json
{"exit_code":0,"data":[{"id":"w-1","name":"bolt"}]}
```

`data` is exactly the JSON the command rendered. A command without a
schema answers with its default rendering in `stdout` instead, and
`data` is absent. `format` is a root flag, not one the command
declares, so the projection does not accept it: a schema-declaring
command always answers in `data`.

### 4. Call a read command

A command annotated `kit/side-effect: read` is a `GET`. Flags go in
the query string, positional arguments in repeated `arg`:

```bash
curl -s 'http://127.0.0.1:8080/v1/commands/widget/list?limit=5&all=true'
```

```json
{"exit_code":0,"data":[{"id":"w-1","name":"bolt"}]}
```

Values are converted to the flag's declared type, so `limit=5`
arrives as a number. `data` is present because `widget list` declares
an output schema (step 3); undeclared query parameters are ignored.

### 5. Call a write command

Anything not annotated `read` is a `POST`, with flags and arguments
in a JSON body:

```bash
curl -s -X POST http://127.0.0.1:8080/v1/commands/widget/add \
  -H 'Content-Type: application/json' \
  -d '{"flags":{"force":true},"args":["gadget"]}'
```

```json
{"exit_code":0,"stdout":"created widget w-2\n"}
```

`widget add` declares no schema, so its output arrives as the text it
printed. Declare one (step 3) and the same call answers
`{"exit_code":0,"data":{"id":"w-2"}}`.

The command's exit code sets the HTTP status: `0` is `200`, `2`
(`USAGE`) is `400`, `3` (`NOT_FOUND`) is `404`. See
[the exit-code table](../../../go/transport/api/README.md#exit-codes)
for the full mapping.

### 6. Permit a destructive command

Destructive commands are withheld from REST by default. There is no
route, and discovery says why:

```json
{"name": "widget delete", "invocable": false,
 "reason": "unauthorized-destructive"}
```

Permit them by naming the REST surface:

```go
import "hop.top/kit/go/transport/cmdsurface"

cli.WithAPI(cli.APIConfig{
    Policy: cmdsurface.Policy{
        AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceREST},
    },
})
```

That lifts the transport's ceiling. Your command's **own confirmation
gate still applies**, and there is no TTY behind an HTTP request, so
an unconfirmed destructive command is now refused by the command
instead of by the bridge — `403`, carrying the command's own message:

```json
{"exit_code": 5,
 "stderr": "UNAUTHORIZED: destructive command mytool widget delete refused: --confirm=no (or non-TTY default)\n"}
```

Pass the confirmation as a flag to complete it:

```bash
curl -s -X POST http://127.0.0.1:8080/v1/commands/widget/delete \
  -H 'Content-Type: application/json' \
  -d '{"flags":{"confirm":"yes"},"args":["7"]}'
```

```json
{"exit_code": 0, "stdout": "deleted widget 7\n"}
```

Both steps are required: `Policy` decides whether the transport may
carry the command, and `confirm` satisfies the command's own gate.
A command annotated for typed confirmation additionally needs
`confirm-token`; the refusal message tells the caller the exact token.

Naming a surface widens **that surface only** — permitting destructive
commands over REST does not make them reachable over MCP or the
socket.

### 7. Keep a command off REST

`Hide` takes command patterns and withholds them from REST only —
the CLI and every other surface keep the command:

```go
cli.WithAPI(cli.APIConfig{
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

### 8. Read the OpenAPI document

Set `OpenAPI` to get a full document — request and response schemas,
your declared output schemas, the confirmation flags where a command
is gated:

```go
import "hop.top/kit/go/transport/api"

cli.WithAPI(cli.APIConfig{
    OpenAPI: &api.OpenAPIConfig{Title: "mytool", Version: "1.4.2"},
})
```

```bash
curl -s http://127.0.0.1:8080/openapi.json | jq '.paths | keys'
```

Without `OpenAPI` set, projection still mounts and a minimal
document is served at the same path — enough to find every
operation, its method and its path.

### 9. Put it behind auth

`APIConfig.Auth` gates the projected routes and the discovery
endpoint exactly as it gates your own, and it is what permits a
non-loopback address:

```go
import (
    "net/http"

    "hop.top/kit/go/transport/api"
)

cli.WithAPI(cli.APIConfig{
    Addr: "0.0.0.0:8080",
    Auth: func(r *http.Request) (any, error) {
        claims, err := validateToken(r.Header.Get("Authorization"))
        if err != nil {
            return nil, err
        }
        return api.Claims{Subject: claims.User, Tenant: claims.Org, Scopes: claims.Scopes}, nil
    },
})
```

The projection installs no auth of its own and no second mechanism.
An unauthenticated call gets `401` before the command runs. The
claims you return attribute each call: return an `api.Claims`, a
value implementing `api.Identity`, or a string-keyed map with `sub`
and `tenant`, and the principal and tenant reach the permission gate
and the audit trail as `Meta.Caller` and `Meta.Tenant`.

Forgetting `Auth` on a non-loopback address is refused at `serve`,
exit `2`, with a message naming the fix. The complete walkthrough —
the refusal, the opt-in, a permission gate, the audit trail, request
and trace ids — is
[secure-remote-serving.md](secure-remote-serving.md).

### 10. Run requests in parallel

By default the projection runs one command at a time: every request
runs on the tool's own command tree, and the runner serializes them.
To run requests in parallel, hand kit the function that builds your
root — the one `main` already has — with `cli.WithRootFactory`. Every
request then runs on a tree of its own.

```go
package main

import (
    "context"

    "github.com/spf13/cobra"

    "hop.top/kit/go/console/cli"
)

func newRoot() *cli.Root {
    root := cli.New(cli.Config{Name: "mytool", Version: "1.4.2"},
        cli.WithAPI(cli.APIConfig{}),
        cli.WithRootFactory(newRoot), // a fresh tree per request
    )
    root.Cmd.AddCommand(&cobra.Command{
        Use:         "ping",
        Short:       "Answer pong",
        Annotations: map[string]string{"kit/side-effect": "read"},
        RunE: func(cmd *cobra.Command, _ []string) error {
            cmd.Print("pong")
            return nil
        },
    })
    return root
}

func main() {
    _ = newRoot().Execute(context.Background())
}
```

```bash
mytool serve
```

What you get, and what you owe:

- Requests run concurrently on isolated trees. Nothing is shared
  between them, and no request waits for another.
- Every gate still applies. Kit prepares each tree before it runs, so
  an unconfirmed destructive command is refused with `403`, a
  typed-token command needs its token, and interactive and
  self-hosting commands stay unmounted. Root flags from your own
  `mytool serve` command line reach every request.
- `newRoot` runs once per request, so keep it cheap: open stores and
  clients once, outside it, and make them safe for concurrent use.
  Anything reached through a package-level variable is shared across
  requests, factory or not.
- A `newRoot` that cannot build a valid tree is refused when `serve`
  validates, at exit `2`, before the server binds.

## Option reference

| Option | Default | Effect |
|---|---|---|
| `APIConfig.Addr` | `127.0.0.1:8080` | Listen address. `--addr` overrides it. Non-loopback needs `Auth` or `InsecureRemote`. |
| `APIConfig.Auth` | none | Gates every route, projected and adopter-owned, and permits any address. `--no-auth` disables it on loopback. |
| `APIConfig.InsecureRemote` | `false` | Serve unauthenticated beyond loopback. `services.api.insecure_remote` / `--insecure-remote` set the same. |
| `APIConfig.OpenAPI` | nil | Full spec at `/openapi.json`. Unset still serves a minimal one. |
| `APIConfig.Policy` | zero | Zero withholds all destructive commands. `AllowDestructiveOn: [SurfaceREST]` permits them. |
| `APIConfig.Expose` | empty | Empty mounts the whole tree; a non-empty list is an allow-list. |
| `APIConfig.Hide` | empty | Pattern list withheld from REST, applied after `Expose`. |
| `APIConfig.Handlers` | nil | Your own routes, mounted before the projection. |
| `cli.WithRootFactory(newRoot)` | not set | Run requests in parallel, each on a tree `newRoot` builds (step 10). Unset serializes them on the tool's own tree. |

## Execution facts

Rely on these; they are the
[execution contract](../../contracts/serve-lifecycle.md#execution)
as it applies to REST:

- **A disconnect cancels the command.** The request's context is the
  command's `cmd.Context()`. A command that selects on it stops when
  the client goes away; one that never reads it runs to completion.
- **One command at a time, unless you opt in.** By default the
  projection runs commands in process on the tool's own command tree,
  and the runner serializes them: a second request waits for the
  first to finish. With `cli.WithRootFactory` (step 10) every request
  runs on a tree of its own, in parallel.
- **Each request starts clean.** Flags from one request do not carry
  into the next. Every command starts from the flag state the
  operator's own `mytool serve` command line left, plus only what the
  request carries, on the shared tree and on a factory's alike.
  Standard input is empty.
- **A cancellation is not a failure.** A command that fails while its
  request is canceled is reported as canceled, not as an error of its
  own.

## What the projection does not implement

Absence here is deliberate — a surface that guessed at these would
be lying about what your commands promise:

- **`PUT` and `DELETE`.** Reads are `GET`, everything else is
  `POST`. Kit's vocabulary has no resource identity, so there is no
  target for their semantics.
- **Interactive commands.** A command tiered `interactive` needs a
  terminal and a human; it is never mounted, and appears in
  discovery with `reason: "interactive"`.
- **Self-hosting commands.** `serve` and everything under it, any
  command declaring `kit/network: ingress`, and any command annotated
  `kit/self-hosting: true` would start a server inside the server or
  replace the binary that is serving. They are never mounted, and
  appear in discovery with `reason: "self-hosting"`. Mark your own
  self-modifying commands with the annotation.
- **Forced remote execution.** Commands the policy refuses stay
  refused. There is no override that runs one anyway.
- **Streaming.** A command's output arrives when it finishes. The
  response is `data` where the command declares a schema and its
  default rendering in `stdout` where it does not.
- **A choice of rendering.** `format` is not accepted. A caller who
  wants a table renders `data` itself.

## Related pages

- [api README](../../../go/transport/api/README.md) — full package
  reference: route shape, mapping tables, discovery payload
- [secure-remote-serving.md](secure-remote-serving.md) — auth beyond
  loopback, the permission gate, the audit trail
- [expose-cli-over-mcp.md](expose-cli-over-mcp.md) — the same tree
  as MCP tools, for LLM hosts
- [serve-lifecycle contract](../../contracts/serve-lifecycle.md) —
  how services start, report ready, and stop
- [cmdsurface README](../../../go/transport/cmdsurface/README.md) —
  the bridge, the policy gate, and every other surface
