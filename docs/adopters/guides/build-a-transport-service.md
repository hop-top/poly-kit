# Build a transport service

Put your own transport — TCP, an in-memory channel, a message queue,
anything that carries requests — in front of your command tree, and
get the whole serve lifecycle for free.

## Who this is for

Developers who want their kit CLI reachable over a transport kit does
not ship. If the built-in Unix socket is what you want, use
[serve-cli-over-unix-socket.md](serve-cli-over-unix-socket.md)
instead — this guide is for writing a new one.

## Before you begin

You need:

- A kit project with a cobra root (see
  [create-cli-project.md](create-cli-project.md))
- `hop.top/kit/go/transport/transportsvc` and
  `hop.top/kit/go/console/serve` importable
- Familiarity with the [serve
  lifecycle](../../contracts/serve-lifecycle.md) — services, the
  registry, readiness

## What you get

Implement three methods and the seam supplies the rest:

| You write | The seam handles |
|---|---|
| `Bind` — acquire the endpoint, return its address | reflecting the command tree, once, at start |
| `Serve` — accept requests, call the invoker | the safety policy on every invocation |
| `Close` — release what `Bind` acquired | readiness reporting and the bound address |
| | ordered, idempotent stop within the shutdown budget |

You never read a cobra annotation, resolve a command path, or decide
whether a destructive command may run. Those are the same for every
transport, so they are decided once.

## Steps

### 1. Implement the transport

A TCP transport, complete. Substitute your own framing for the
newline-delimited JSON:

```go
package tcptransport

import (
    "bufio"
    "context"
    "encoding/json"
    "errors"
    "net"
    "sync"

    "hop.top/kit/go/transport/cmdsurface"
    "hop.top/kit/go/transport/transportsvc"
)

type Transport struct {
    Addr string

    mu sync.Mutex
    ln net.Listener
}

// Bind acquires the listener. Everything that can fail
// deterministically must fail here, not later.
func (t *Transport) Bind(context.Context) (string, error) {
    ln, err := net.Listen("tcp", t.Addr)
    if err != nil {
        return "", err
    }
    t.mu.Lock()
    t.ln = ln
    t.mu.Unlock()
    // Return the resolved address: for ":0" the kernel picked the
    // port, and this is the only place it becomes knowable.
    return ln.Addr().String(), nil
}

// Serve accepts until ctx is canceled or Close is called.
func (t *Transport) Serve(ctx context.Context, inv transportsvc.Invoker) error {
    t.mu.Lock()
    ln := t.ln
    t.mu.Unlock()

    // Close on cancellation too, so Accept never blocks a shutdown
    // that did not go through Stop.
    stop := context.AfterFunc(ctx, func() { _ = t.Close(context.Background()) })
    defer stop()

    for {
        conn, err := ln.Accept()
        if err != nil {
            if errors.Is(err, net.ErrClosed) {
                return nil // a closed listener is a clean stop
            }
            return err
        }
        go t.handle(ctx, conn, inv)
    }
}

func (t *Transport) handle(ctx context.Context, conn net.Conn, inv transportsvc.Invoker) {
    defer conn.Close()

    sc := bufio.NewScanner(conn)
    enc := json.NewEncoder(conn)

    for sc.Scan() {
        var req cmdsurface.Invocation
        if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
            _ = enc.Encode(map[string]string{"error": err.Error()})
            continue
        }
        // One call. The seam has already pinned the surface and will
        // apply the policy gate.
        res, err := inv(ctx, req)
        if err != nil {
            _ = enc.Encode(map[string]string{"error": err.Error()})
            continue
        }
        _ = enc.Encode(res)
    }
}

// Close must make Serve return.
func (t *Transport) Close(context.Context) error {
    t.mu.Lock()
    ln := t.ln
    t.mu.Unlock()
    if ln == nil {
        return nil
    }
    err := ln.Close()
    if errors.Is(err, net.ErrClosed) {
        return nil // already closed; Close is idempotent
    }
    return err
}
```

### 2. Register it

`example.com/mytool/internal/tcptransport` below is a placeholder for
wherever you put step 1 — substitute your own module path.

```go
package main

import (
    "context"
    "log"

    "example.com/mytool/internal/tcptransport"
    "hop.top/kit/go/console/cli"
    "hop.top/kit/go/transport/cmdsurface"
    "hop.top/kit/go/transport/transportsvc"
)

func main() {
    root := cli.New(cli.Config{Name: "mytool", Version: "1.0.0"})
    root.Cmd.AddCommand(widgetCmd())

    tcp := &tcptransport.Transport{Addr: "127.0.0.1:9000"}

    cli.WithService(transportsvc.NewTransportService(
        "tcp",                  // the CLI word, config key, and event payload value
        root.Cmd,               // the tree to project
        cmdsurface.SurfaceRPC,  // the surface this transport invokes as
        tcp,
        transportsvc.Expose("*"),
    ))(root)

    if err := root.Execute(context.Background()); err != nil {
        log.Fatal(err)
    }
}
```

Now `mytool serve tcp` starts it, `mytool serve --list` shows it, and
`services.tcp.enabled: true` includes it in `mytool serve`.

The name must match `^[a-z][a-z0-9-]*$` and must not be `all`, `none`,
or `list`. An invalid name panics at construction — it is a wiring
bug, and a service that cannot be named cannot be selected or
configured.

### 3. Options

| Option | Use this when |
|---|---|
| `Expose(pattern)` | your transport should reach commands the bridge does not enable by default — usually `Expose("*")` |
| `Hide(pattern)` | you want an exception carved out of a broader `Expose` |
| `WithBridgeOptions(...)` | you need a custom `cmdsurface.Policy` (to permit destructive commands) or a custom `Runner` |
| `WithValidate(fn)` | your configuration can be wrong in a way you can detect before binding — a malformed address, a missing file |
| `WithClass(sideEffect, network)` | your tool runs a policy table and this service should be gated by it |
| `WithDependsOn(names...)` | your transport needs another service started first |

`WithValidate` is worth wiring: it runs before anything binds, so a
bad address is a usage error at exit `2` rather than a start failure a
second later.

### 4. Test it

Drive the service directly — no CLI needed:

```go
package tcptransport_test

import (
    "context"
    "net"
    "testing"

    "github.com/stretchr/testify/require"

    "example.com/mytool/internal/tcptransport"
    "hop.top/kit/go/transport/cmdsurface"
    "hop.top/kit/go/transport/transportsvc"
)

func TestTransportServesCommands(t *testing.T) {
    tr := &tcptransport.Transport{Addr: "127.0.0.1:0"}
    svc := transportsvc.NewTransportService(
        "tcp", testRoot(), cmdsurface.SurfaceRPC, tr,
        transportsvc.Expose("*"),
    )

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    ready := make(chan struct{})
    errCh := make(chan error, 1)
    go func() { errCh <- svc.Start(ctx, func() { close(ready) }) }()
    <-ready

    // Addr is resolved only after Bind, which is why ":0" works.
    conn, err := net.Dial("tcp", svc.Addr())
    require.NoError(t, err)
    defer conn.Close()

    // ... write a request, assert on the response ...

    require.NoError(t, svc.Stop(context.Background()))
    require.NoError(t, <-errCh, "Close must make Serve return")
}
```

Assert on `svc.Addr()` after readiness, and on `<-errCh` after
`Stop` — a `Serve` that does not return is the most common bug in a
new transport, and that one line catches it.

## Rules the seam enforces

These are consequences for you as the author, not background theory:

- **Reflection happens at `Start`, not construction.** You get the
  complete tree, including commands mounted after
  `NewTransportService` was called. Do not cache leaves yourself.
- **Your surface is pinned.** Whatever `Meta.Surface` a request
  arrives with, the seam overwrites it with the surface you
  registered. You cannot widen your own reach by setting it, and you
  do not need to set it.
- **Ready is reported after `Bind` returns nil.** Put every failure
  that can be detected up front inside `Bind`. Anything you defer to
  `Serve` reports as a runtime crash instead of a start failure.
- **`Close` must make `Serve` return.** The supervisor bounds `Stop`
  and abandons it when it overruns; a `Serve` that keeps running
  holds your endpoint open after `serve` has reported it stopped.
- **`Close` may be called more than once**, including concurrently
  with cancellation. Make it idempotent.
- **`Serve` returning `nil` after cancellation is a clean stop.**
  Return an error only for a real failure, or every rolling restart
  looks like a crash.

## What the seam does not do

- **No framing.** You choose the wire format.
- **No authentication.** If your transport is reachable beyond the
  local host, authenticate before calling the invoker, and put the
  principal in `Meta.Caller` for audit sinks.
- **No streaming.** `Invoker` is request/response. For incremental
  output, reach the runner through the bridge — see
  [`Bridge.Runner`](../../../go/transport/cmdsurface/bridge.go).
- **No retries or backpressure.** Concurrency and load shedding are
  yours.

## Related pages

- [transportsvc README](../../../go/transport/transportsvc/README.md)
  — full seam API reference
- [socket package](../../../go/transport/socket/) — a complete
  transport on this seam, ~250 lines
- [serve lifecycle contract](../../contracts/serve-lifecycle.md) —
  normative rules for services and transport services
- [cmdsurface README](../../../go/transport/cmdsurface/README.md) —
  invocations, results, policy, surfaces
