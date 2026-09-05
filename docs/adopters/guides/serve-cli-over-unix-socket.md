# Serve your CLI over a Unix socket

Run your cobra tree behind a local Unix domain socket: one
newline-delimited JSON request per line, one response per line, no
port and no network.

## Who this is for

Developers building a kit CLI who want another local process — a
daemon, an editor plugin, a shell script, a sidecar — to invoke their
commands without spawning the binary each time. If you want the same
commands reachable by an LLM host, see
[expose-cli-over-mcp.md](expose-cli-over-mcp.md) instead. If you want
protobuf and generated per-service handlers, use
[`go/transport/rpc`](../../../go/transport/rpc/) directly.

## Before you begin

You need:

- A kit project with a cobra root (see
  [create-cli-project.md](create-cli-project.md))
- `hop.top/kit/go/console/cli` importable
- A Unix-like host. Unix domain sockets are not available on Windows.

## What you get

`WithSocket` registers a `socket` service under your tool's `serve`
command. Starting it gives you:

- **One socket file**, owner-only (`0600`), under your tool's runtime
  directory.
- **Every command reachable**, subject to the same safety policy the
  CLI enforces — destructive commands are refused unless you permit
  them.
- **Structured results**: exit code, stdout, stderr, and — for a
  command that declares an output schema — its output decoded into
  `data`.
- **The serve lifecycle**: readiness reporting, ordered shutdown, and
  the socket file unlinked when the service stops.

The socket is **not enabled by default**. A service that starts
listening because you upgraded a dependency is an unrequested
endpoint, so you turn it on deliberately.

## Steps

### 1. Register the service

```go
package main

import (
    "context"
    "log"

    "hop.top/kit/go/console/cli"
)

func main() {
    root := cli.New(cli.Config{Name: "mytool", Version: "1.0.0"},
        cli.WithSocket(cli.SocketConfig{}),
    )

    root.Cmd.AddCommand(widgetCmd()) // your existing commands

    if err := root.Execute(context.Background()); err != nil {
        log.Fatal(err)
    }
}
```

An empty `SocketConfig{}` is the whole configuration for the common
case: the path defaults, and every command is reachable under the
default safety policy.

### 2. Start it

Name the service explicitly. This is the shortest path, and it starts
the socket even though `enabled` is `false`:

```console
$ mytool serve socket
INFO service started service=socket
INFO service ready service=socket address=/run/user/1000/mytool/mytool.sock
```

Override the path for one run:

```console
$ mytool serve socket --socket /tmp/mytool.sock
```

Or configure it, and let `mytool serve` start it alongside your other
services:

```yaml
# ~/.config/mytool/config.yaml
services:
  socket:
    enabled: true
    path: /run/mytool/mytool.sock
```

**Where the socket lands by default.** `<runtime dir>/<tool>/<tool>.sock`,
where the runtime dir is `$XDG_RUNTIME_DIR` when set, and otherwise
your platform's location for ephemeral per-user files — on macOS
`~/Library/Application Support`, and the temp directory when neither
is available. Run `mytool serve socket` and read the `address` field
in the ready line if you want the resolved value.

**Why `0600`.** A Unix socket has no port and is not routable, so the
filesystem permission *is* the access control. Owner-only means a
process running as another local user cannot invoke your commands.

### 3. Talk to it

One JSON object per line in, one per line out. With `socat`:

```console
$ echo '{"path":["widget","list"]}' | socat - UNIX-CONNECT:/tmp/mytool.sock
{"ok":true,"result":{"exit_code":0,"stdout":"widget-1\nwidget-2\n"}}
```

Or with `nc`:

```console
$ echo '{"path":["widget","list"]}' | nc -U /tmp/mytool.sock
{"ok":true,"result":{"exit_code":0,"stdout":"widget-1\nwidget-2\n"}}
```

A minimal Go client — dial, write one request, read one line back:

```go
package main

import (
    "bufio"
    "encoding/json"
    "fmt"
    "log"
    "net"

    "hop.top/kit/go/transport/socket"
)

func main() {
    conn, err := net.Dial("unix", "/tmp/mytool.sock")
    if err != nil {
        log.Fatal(err)
    }
    defer conn.Close()

    req := socket.Request{Path: []string{"widget", "list"}}
    if err := json.NewEncoder(conn).Encode(req); err != nil {
        log.Fatal(err)
    }

    line, err := bufio.NewReader(conn).ReadBytes('\n')
    if err != nil {
        log.Fatal(err)
    }

    var resp socket.Response
    if err := json.Unmarshal(line, &resp); err != nil {
        log.Fatal(err)
    }
    if !resp.Ok {
        log.Fatalf("%s: %s", resp.Error.Code, resp.Error.Message)
    }
    fmt.Print(resp.Result.Stdout)
}
```

The connection stays open. Send more requests on it and read one
response per request, in order.

### 4. Get structured output

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
        cli.WithSocket(cli.SocketConfig{}),
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
to have. Call `widget list` with no `format` and the service runs it
as `--format=json` and decodes what it wrote:

```console
$ echo '{"path":["widget","list"]}' | socat - UNIX-CONNECT:/tmp/mytool.sock
{"ok":true,"result":{"exit_code":0,"data":[{"id":"w-1","name":"bolt"}]}}
```

Name a format and you get that rendering on `stdout` instead — the
table the CLI prints, or the JSON text alongside `data`:

```console
$ echo '{"path":["widget","list"],"flags":{"format":"table"}}' | socat - UNIX-CONNECT:/tmp/mytool.sock
{"ok":true,"result":{"exit_code":0,"stdout":"ID   NAME\nw-1  bolt\n"}}
$ echo '{"path":["widget","list"],"flags":{"format":"json"}}' | socat - UNIX-CONNECT:/tmp/mytool.sock
{"ok":true,"result":{"exit_code":0,"stdout":"[\n  {\n    \"id\": \"w-1\",\n    \"name\": \"bolt\"\n  }\n]\n","data":[{"id":"w-1","name":"bolt"}]}}
```

A command without a schema answers with its rendering in `stdout` and
no `data`, whatever format you name. Read the client's `Result.Data`
as the JSON value it is (`[]any` of `map[string]any` here), or
`json.Marshal` it into your own type.

### 5. Call a read command

```console
$ echo '{"path":["widget","get"],"args":["7"]}' \
    | socat - UNIX-CONNECT:/tmp/mytool.sock
{"ok":true,"result":{"exit_code":0,"data":{"id":7,"name":"bolt"}}}
```

`args` are the positional arguments after the command path; `flags`
is keyed by long flag name. `widget get` declares a schema, so it
answers in `data`; add `"flags":{"format":"json"}` to get the JSON
text on `stdout` as well.

### 6. Call a write command

A non-destructive write needs nothing special:

```console
$ echo '{"path":["widget","add"],"args":["bolt"]}' \
    | socat - UNIX-CONNECT:/tmp/mytool.sock
{"ok":true,"result":{"exit_code":0,"stdout":"created widget 8\n"}}
```

When the command itself fails, `ok` is still `true` — the call
completed — and the failure is in the result:

```console
$ echo '{"path":["widget","get"],"args":["999"]}' \
    | socat - UNIX-CONNECT:/tmp/mytool.sock
{"ok":true,"result":{"exit_code":1,"stderr":"GENERIC: widget 999 not found\n"}}
```

`ok:false` means the request never reached your command. `ok:true`
with a non-zero `exit_code` means it ran and failed.

### 7. Permit a destructive command

Destructive commands are refused by default:

```console
$ echo '{"path":["widget","delete"],"args":["7"]}' \
    | socat - UNIX-CONNECT:/tmp/mytool.sock
{"ok":false,"error":{"code":"BLOCKED","message":"cmdsurface: destructive command blocked on this surface: widget delete on rpc"}}
```

Permit them by naming the socket's surface:

```go
package main

import (
    "context"
    "log"

    "hop.top/kit/go/console/cli"
    "hop.top/kit/go/transport/cmdsurface"
)

func main() {
    root := cli.New(cli.Config{Name: "mytool", Version: "1.0.0"},
        cli.WithSocket(cli.SocketConfig{
            Policy: cmdsurface.Policy{
                AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceRPC},
            },
        }),
    )

    root.Cmd.AddCommand(widgetCmd())

    if err := root.Execute(context.Background()); err != nil {
        log.Fatal(err)
    }
}
```

That lifts the transport's ceiling. Your command's **own confirmation
gate still applies**, and there is no TTY on a socket, so an
unconfirmed destructive command is now refused by the command instead
of by the bridge:

```console
{"ok":true,"result":{"exit_code":5,"stderr":"UNAUTHORIZED: destructive command mytool widget delete refused: --confirm=no (or non-TTY default)\n"}}
```

Pass the confirmation as a flag to complete it:

```console
$ echo '{"path":["widget","delete"],"args":["7"],"flags":{"confirm":"yes"}}' \
    | socat - UNIX-CONNECT:/tmp/mytool.sock
{"ok":true,"result":{"exit_code":0,"stdout":"deleted widget 7\n"}}
```

Both steps are required: `Policy` decides whether the transport may
carry the command, and `confirm` satisfies the command's own gate.
A command annotated for typed confirmation additionally needs
`confirm-token`; the refusal message tells the caller the exact token.

Naming a surface widens **that surface only** — permitting destructive
commands on the socket does not make them reachable over MCP or REST.

### 8. Restrict which commands are reachable

By default every command the reflector considers invocable is
reachable. Narrow it with `Expose` and `Hide`:

```go
package main

import (
    "context"
    "log"

    "hop.top/kit/go/console/cli"
)

func main() {
    root := cli.New(cli.Config{Name: "mytool", Version: "1.0.0"},
        cli.WithSocket(cli.SocketConfig{
            Expose: []string{"widget *", "status"},
            Hide:   []string{"widget delete"},
        }),
    )

    root.Cmd.AddCommand(widgetCmd())

    if err := root.Execute(context.Background()); err != nil {
        log.Fatal(err)
    }
}
```

Patterns are `"widget add"` (exact), `"widget *"` (everything under
`widget`), or `"*"` (everything). `Hide` applies after `Expose`, so
the pair above reaches every `widget` subcommand except `delete`.

A command that exists but is not exposed here is reported as such,
which is a different answer from one that does not exist:

```console
{"ok":false,"error":{"code":"NOT_ENABLED","message":"cmdsurface: surface not enabled for command: widget delete on rpc"}}
```

### 9. Read errors

| `code` | Meaning | Typical cause |
|---|---|---|
| `NOT_FOUND` | the path resolves to no reachable command | typo; or a hidden, deprecated, or self-hosting command the reflector excluded — `serve` itself answers this |
| `NOT_ENABLED` | the command exists but not on this surface | excluded by your `Expose` / `Hide` patterns |
| `NOT_INVOCABLE` | the command can never run through a transport | an `interactive` command; the message names the reason |
| `BLOCKED` | destructive command refused by policy | `Policy.AllowDestructiveOn` does not name this surface |
| `DENIED` | the permission gate refused this caller | `cli.WithPermission`, or a `--policy` that refuses the class; the message carries the reason |
| `UNAUTHENTICATED` | `SocketConfig.Auth` refused the request | only sent when an authenticator is configured |
| `INVALID` | the request line is malformed | bad JSON, or an empty `path` |
| `INTERNAL` | anything else the runner returned | a bug worth reporting |

A malformed line does not cost you the connection — the error comes
back and the next request on the same connection is served normally.

Command exit codes surface in `result.exit_code`, using the same
[kit exit-code taxonomy](../../../go/console/output/error.go) the CLI uses, so
`2` is a usage error and `5` is unauthorized whether the command was
invoked from a shell or from this socket. A request with the wrong
number of `args`, or a flag the command does not take, is `2` as well,
with the parser's message in `result.stderr`.

### 10. Stop it, and know when it is ready

`serve` reports readiness once the socket is bound, carrying the
resolved path:

```console
INFO service ready service=socket address=/tmp/mytool.sock
```

Wait for that line, or simply retry the connection — the socket file
exists only once the service is accepting work.

On `SIGINT` or `SIGTERM` the service drains in-flight requests,
closes the listener, and **unlinks the socket file**, so a restart is
not blocked by a leftover. A socket file left behind by a crashed
process is reclaimed automatically at the next start; a socket a live
process is still listening on is refused, rather than silently stolen.

## Option reference

| Field | Type | Default | Meaning |
|---|---|---|---|
| `Path` | `string` | runtime dir (see step 2) | socket path; `services.socket.path` and `--socket` override it |
| `Expose` | `[]string` | every invocable command | patterns the socket may reach |
| `Hide` | `[]string` | none | patterns carved out of `Expose` |
| `Policy` | `cmdsurface.Policy` | zero value | safety gate; zero behaves as `cmdsurface.DefaultPolicy()` |
| `Auth` | `socket.Authenticator` | none | verifies each request; its identity replaces the claimed `caller` and `tenant` |

Configuration precedence is flag, then config file, then
`SocketConfig`, then the default:

| Source | Key |
|---|---|
| flag | `--socket <path>` |
| config / env | `services.socket.path`, `MYTOOL_SERVICES_SOCKET_PATH` |
| code | `SocketConfig.Path` |
| default | `<runtime dir>/<tool>/<tool>.sock` |

A path longer than your platform's `sockaddr_un` limit (103 bytes
here) is refused at startup with exit `2` and a message naming the
limit, rather than surfacing as a kernel `invalid argument`.

## Execution facts

Rely on these; they are the
[execution contract](../../contracts/serve-lifecycle.md#execution)
as it applies to the socket:

- **One command at a time.** Requests on a connection are answered in
  order, and across connections the service runs commands in process
  on the tool's own command tree, one at a time. A request waits for
  the one running.
- **Each request starts clean.** Flags from one request do not carry
  into the next. Every command starts from the flag state the
  operator's own `mytool serve socket` command line left, plus only
  what the request carries. Standard input is empty: a destructive
  command with no `confirm` flag is refused, never prompted on the
  operator's terminal.
- **Hanging up cancels.** A request runs with its connection's
  context. Close the connection mid-command and the command's context
  is canceled; a command that honors its context stops, and its result
  is discarded either way. Stopping the service cancels every command
  in flight, and a command that fails while canceled is reported as
  canceled.

## What the service does not implement

Stated plainly, so you can decide what to put in front of it:

- **No authentication by default.** Access control is the socket
  file's permission. `SocketConfig.Auth` adds a per-request
  authenticator — it receives the connection, so it can ask the
  kernel who the peer is — but kit ships none.
- **Caller identity is provenance, not a credential.** Without an
  authenticator, the `caller` and `tenant` fields are recorded as
  claimed and travel to audit sinks with `request_id`, `trace_id`, and
  `idempotency_key`. Nothing is granted on their basis, and a client
  may claim any value. A permission gate that decides on
  `Meta.Caller` over an unauthenticated socket is trusting the
  caller's word; see
  [secure-remote-serving.md](secure-remote-serving.md#5-wire-a-permission-policy).
- **No remote access.** A Unix socket is local by construction. There
  is no listen address, no TLS, and no way to reach it from another
  host without you forwarding it yourself.
- **No protobuf.** The wire format is JSON. For protobuf over
  ConnectRPC, use [`go/transport/rpc`](../../../go/transport/rpc/).
- **No streaming.** One request, one response. Long-running commands
  return when they finish.
- **No interactive or self-hosting commands.** A command tiered
  `interactive` is refused by the bridge's gate, before the policy
  gate, with `NOT_INVOCABLE` — there is no terminal on a socket — and
  the runner refuses it again as a backstop. `serve`, any command
  declaring `kit/network: ingress`, and any command annotated
  `kit/self-hosting: true` are never reachable at all: they are not
  leaves, so a call answers `NOT_FOUND`.
- [socket package README](../../../go/transport/socket/README.md) —
  wire protocol, error codes, config keys
- [transportsvc README](../../../go/transport/transportsvc/README.md)
  — the seam this service is built on
- [build-a-transport-service.md](build-a-transport-service.md) — put
  your own transport on that seam
- [secure-remote-serving.md](secure-remote-serving.md) — the
  permission gate and the audit trail, shared with the api service
- [serve lifecycle contract](../../contracts/serve-lifecycle.md) —
  normative registration, readiness, and shutdown rules
- [expose-cli-over-mcp.md](expose-cli-over-mcp.md) — the same commands
  as MCP tools
