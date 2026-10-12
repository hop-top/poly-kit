# Manage the service lifecycle

Your tool registers services (`cli.WithAPI`, `cli.WithSocket`,
`mcpserve.With`, `rpcserve.With`, `cli.WithService`). Kit gives every
such tool one command group to run and control them:

```sh
mytool service start            # every configured and enabled service
mytool service start api        # exactly one, even when not enabled
mytool service list             # registered services and their state
mytool service status           # what the running instance serves, and where
mytool service stop             # ask the running instance to drain and exit
```

The group appears as soon as one service is registered; a tool with no
services has no `service` command. The root `status` is unrelated: it
stays the tool's own status (profile, environment, auth, effective
configuration).

## Start, list

`service start` is the supervisor and selector the
[serve-lifecycle contract](../../contracts/serve-lifecycle.md) defines.
Every per-service flag lands on it: `--addr`, `--socket`, `--mcp-addr`,
`--stdio`, `--rpc-addr`, `--enable`, `--disable` and the three timeout
flags. `--policy` given to it names the policy the services enforce on
the calls they carry; it does not gate starting them.

`service list` prints, per registered service, whether it is
configured, enabled for a bare `service start`, and ready in this
process.

## Status and stop

While `service start` runs it listens on a control socket at
`<runtime dir>/<tool>/control.sock` (`$XDG_RUNTIME_DIR/<tool>/` when
set). `service status` and `service stop` talk to it.

```sh
$ mytool service status
SERVICE  READY  ADDR            PID
api      true   127.0.0.1:8080  41237
```

| Situation | `service status` | `service stop` |
|---|---|---|
| An instance is running | its services, exit 0 | drains it, waits, `result: stopped`, exit 0 |
| No instance is running | `mytool is not running`, exit 3 (`NOT_FOUND`) | `result: not_running`, exit 0 |
| The drain outlasts `--wait` (default 90s) | — | exit 6 (`TRANSIENT`); the instance keeps draining |

`service stop` stops the instance the way the first `SIGINT` or
`SIGTERM` does: every service is stopped in reverse start order within
its budgets. Nothing is killed.

Only the instance's own user can use the control socket. It is created
`0600` in a `0700` directory, and on Linux, macOS and FreeBSD each
connection's peer uid, as the kernel reports it, must match the
instance's uid.

One control socket per tool per user: a second `service start` while
one is running keeps serving, logs `service control socket unavailable`
on stderr, and is reachable only by signal. A socket file nobody
answers on (an instance that crashed) is replaced by the next start.

## Add your own leaves

The group is the place for your own lifecycle commands, such as
installing a service unit:

```go
root := cli.New(cfg, cli.WithAPI(cli.APIConfig{}))

setup := &cobra.Command{
    Use:   "setup",
    Short: "Install the user service unit",
    Long:  "Install and enable a user service unit that runs `mytool service start`.",
    RunE:  runSetup,
}
cli.SetSideEffect(setup, cli.SideEffectWriteLocal)
cli.SetIdempotency(setup, cli.IdempotencyYes)
root.ServiceCommand().AddCommand(setup) // mytool service setup
```

`ServiceCommand` returns nil on a tool with no registered service. The
words `start`, `list`, `stop` and `status` are kit's. A leaf you add
under `service` is a management command like kit's: it is withheld from
every served surface (REST, socket, MCP, gRPC), exempt from the shape
checks, and left out of `spec coverage`. Mark it with
`kit/self-hosting` if it must never run from a served invocation even
when reflected without the reserved lookup.

`root.ServeCommand()` returns `service start` itself, for putting your
own flags beside the built-in ones.

## Migrate from `serve`

Earlier releases mounted the lifecycle at the root as `serve`. That
spelling still works for one release: hidden, identical in behavior and
flags, and printing a `DEPRECATION` notice to stderr (stdout is
untouched, so scripts parsing it keep working).

| Before | Now |
|---|---|
| `mytool serve` | `mytool service start` |
| `mytool serve api --addr :9000` | `mytool service start api --addr :9000` |
| `mytool serve --list` | `mytool service list` |
| `kill -TERM <pid>` | `mytool service stop` (signals still work) |
| — | `mytool service status` |

What changes for your code and your tests:

- `service start` no longer has `--list`; use `service list`.
- A test or hook that found the serve command by walking the root for
  a child named `serve` now finds the hidden alias, which carries the
  service flags only once the tree is prepared. Use
  `root.ServeCommand()`.
- Served discovery lists `service start`, `service stop`, `service
  status` and `service list` as withheld; `serve` is withheld as hidden.
- The output schema that described `serve --list` now sits on `service
  list`.
- Update scripts, unit files, desktop MCP host configs (`"args":
  ["service", "start", "mcp", "--stdio"]`) and docs before the alias is
  removed.
