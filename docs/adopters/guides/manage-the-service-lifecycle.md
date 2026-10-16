# Manage the service lifecycle

Your tool registers services (`service.WithAPI`, `service.WithSocket`,
`mcpserve.With`, `rpcserve.With`, `service.WithService`). Kit gives every
such tool one command group to run and control them:

```sh
mytool service start            # every configured and enabled service
mytool service start api        # exactly one, even when not enabled
mytool service list             # registered services and their state
mytool service status           # what the running instance serves, and where
mytool service stop             # ask the running instance to drain and exit
mytool service restart          # drain it and re-execute it in place, same pid
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
set). `service status`, `service stop` and `service restart` talk to
it.

```sh
$ mytool service status
SERVICE  READY  ADDR            PID
api      true   127.0.0.1:8080  41237
```

`--format json` prints `{"services": [...], "sections": [...]}`; the
sections are your tool's (see below).

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

One control socket per tool per user, unless you scope it (next
section): a second `service start` while one is running is refused
with exit 4 (`CONFLICT`) naming the socket, before it starts anything.
A socket file nobody answers on (an instance that crashed) is replaced
by the next start; a socket something still answers on, or a file that
is not a socket, is never removed.

## One instance per profile

A tool that runs one daemon per profile (or workspace, or tenant) on
one host gives each instance its own control socket with
`service.WithServiceControlScope`. Kit calls your function from each
`service` leaf, after flags are parsed and your pre-run hooks ran, and
uses the scope it returns for `start` (listen) and `stop`, `restart`,
`status` (dial):

```go
root := cli.New(cfg,
    service.WithAPI(service.APIConfig{}),
    service.WithServiceControlScope(func(cmd *cobra.Command) (string, error) {
        return activeProfile(cmd) // "" is the default, unscoped instance
    }),
)
```

```sh
$ MYTOOL_PROFILE=work mytool service start &   # control-work.sock
$ MYTOOL_PROFILE=home mytool service start &   # control-home.sock
$ MYTOOL_PROFILE=work mytool service stop      # drains work only
$ MYTOOL_PROFILE=home mytool service status    # home's services
```

- The socket is `<runtime dir>/<tool>/control-<scope>.sock`, so a
  scope is letters, digits, `.`, `_` and `-` only. Anything else exits
  2 (`USAGE`); map your profile names onto that set. Keep it short: the
  whole path must fit the 103-byte unix socket limit.
- Derive the scope from what a restarted instance sees again: its
  arguments, environment or configuration. `service restart`
  re-executes with the same ones, and the new run must listen where
  the old one did.
- An error your function returns is returned by the command as it is.
- `mytool is not running in control scope work` (exit 3) names the
  scope a client looked in.

Tests that start two instances of your tool at once, in one process or
in parallel test binaries, are two instances too: the second is refused
with exit 4. Point `XDG_RUNTIME_DIR` at a temporary directory per test
binary (a `TestMain` does it once), and give instances that run side
by side distinct scopes. The option is a plain `func(*cli.Root)`, so a
test can apply it to a root it already built:

```go
service.WithServiceControlScope(func(*cobra.Command) (string, error) { return "t2", nil })(root)
```

## Add sections to `service status`

`service status` prints kit's rows, then sections your tool reports
about the running instance: queue depths, the profile it serves,
per-channel health. Register them with
`root.RegisterServiceStatusProvider`, the same `cli.StatusProvider`
type the root `status` takes:

```go
root.RegisterServiceStatusProvider("queue", func(ctx context.Context) (cli.StatusSection, error) {
    return cli.StatusSection{Title: "queue", Priority: 1000,
        Data: map[string]any{"depth": q.Len()}}, nil
})
```

```sh
$ mytool service status
SERVICE  READY  ADDR            PID
api      true   127.0.0.1:8080  41237

SECTION  STATUS  DETAIL
queue    ok      depth=12
```

```sh
$ mytool service status --format json
{"services":[{"service":"api","ready":true,"addr":"127.0.0.1:8080","pid":41237}],
 "sections":[{"title":"queue","data":{"depth":12},"status":"ok","priority":1000}]}
```

- The running instance calls the provider, not the client: `q` is the
  daemon's queue. Register it on the root that runs `service start`.
- All providers run at once, each with a 2s deadline. A slow one is
  reported `unavailable`, a failing or panicking one as an `error`
  section; `service status` still exits 0.
- `Sensitive: true` sections are redacted with the root status's
  patterns (`StatusConfig.RedactPatterns` included) before they leave
  the instance. There is no `--show-sensitive` here.
- `sections` is `[]` when you register none. `service restart` prints
  the same object for the new run.
- These sections are separate from the root `status`: providers you
  register with `RegisterStatusProvider` stay there.

## Restart

`service restart` picks up a changed config file, flag or binary
without the service manager seeing the process go away:

```sh
$ mytool service restart
SERVICE  READY  ADDR            PID
api      true   127.0.0.1:8080  41237
```

The instance drains exactly as `service stop` makes it, releases what
the run held, then replaces its own process image: the binary
`os.Executable` names, the arguments and environment it was launched
with, from the directory it was launched in. The pid stays the same,
so systemd or launchd sees no exit. The new run reads configuration
and flags afresh. The client waits for the new instance to answer with
every service ready and prints its status.

| Situation | `service restart` |
|---|---|
| An instance is running | drains, re-executes, waits for readiness, prints the new status, exit 0 |
| No instance is running | `mytool is not running`, exit 3 (`NOT_FOUND`), like `service status`: restart does not start one |
| A `service stop` is already draining | exit 4 (`CONFLICT`) |
| A signal or `service stop` arrives during the drain | the instance exits as a stop; the client reports `not restarted`, exit 1 |
| The new run exits instead of coming up (say, a config key it refuses) | exit 1 as soon as the process is gone; the reason is on the instance's stderr |
| The re-exec fails after the drain | the instance exits 1 with the reason on stderr, so the service manager starts it again; the client reports it, exit 1 |
| Drain plus readiness outlast `--wait` (default 2m) | exit 6 (`TRANSIENT`) |
| Windows (no `exec`) | exit 2 (`USAGE`); use `service stop`, then start it again |

Things to know when your tool is restarted this way:

- Code after `root.Execute` in your `main` does not run on a restart:
  the process image is replaced inside `Execute`, once every service
  has stopped. Release anything your services hold in their `Stop`.
- Files and sockets Go opened are close-on-exec, so listeners free
  their ports for the new run. A descriptor you opened without
  close-on-exec leaks into the new run.
- The launch arguments are re-used as given. A relative `-c` path or
  `-C` directory resolves as it did at launch, because the new run
  starts in the launch directory.

## Add your own leaves

The group is the place for your own lifecycle commands, such as
installing a service unit:

```go
root := cli.New(cfg, service.WithAPI(service.APIConfig{}))

setup := &cobra.Command{
    Use:   "setup",
    Short: "Install the user service unit",
    Long:  "Install and enable a user service unit that runs `mytool service start`.",
    RunE:  runSetup,
}
cli.SetSideEffect(setup, cli.SideEffectWriteLocal)
cli.SetIdempotency(setup, cli.IdempotencyYes)
service.ServiceCommand(root).AddCommand(setup) // mytool service setup
```

`ServiceCommand` returns nil on a tool with no registered service. The
words `start`, `list`, `stop`, `restart` and `status` are kit's. A leaf you add
under `service` is a management command like kit's: it is withheld from
every served surface (REST, socket, MCP, gRPC), exempt from the shape
checks, and left out of `spec coverage`. Mark it with
`kit/self-hosting` if it must never run from a served invocation even
when reflected without the reserved lookup.

`service.ServeCommand(root)` returns `service start` itself, for putting your
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
| `systemctl restart mytool` (new pid) | `mytool service restart` (same pid; signals still work) |

What changes for your code and your tests:

- `service start` no longer has `--list`; use `service list`.
- A test or hook that found the serve command by walking the root for
  a child named `serve` now finds the hidden alias, which carries the
  service flags only once the tree is prepared. Use
  `service.ServeCommand(root)`.
- Served discovery lists `service start`, `service stop`, `service
  restart`, `service status` and `service list` as withheld; `serve` is
  withheld as hidden.
- The output schema that described `serve --list` now sits on `service
  list`.
- Update scripts, unit files, desktop MCP host configs (`"args":
  ["service", "start", "mcp", "--stdio"]`) and docs before the alias is
  removed.
