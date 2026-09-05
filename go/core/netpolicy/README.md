# netpolicy

## What it answers

Is this process allowed to reach the network right now. It owns the
`--offline` marker and enforces it inside the transport, beneath every
`http.Client`, so no caller can route around it. Path policy (which files
a tool may touch) is `core/scope`; per-action side-effect gating is
`runtime/sideeffect`.

## Use it when

- your client uses the default transport: nothing to do, `cli.New` calls `Install()`
- your client sets its own `Transport`: wrap it with `Guard(base)`
- a library accepts an injected dial function (SMTP, MySQL, gRPC, websocket): wrap it with `GuardDial(base)`
- a hook has no `net.Conn` to return (driver `Connector`, connect callback): call `CheckDial(ctx, network, addr)`
- a sink carries diagnostics only (telemetry, crash reports): `ObservabilityTransport(base)`, never for user traffic
- you need the decision without a transport: `IsOffline(ctx)`, `WithOffline(ctx, true)`

## Quick start

```go
client := &http.Client{Transport: netpolicy.Guard(http.DefaultTransport)}

ctx := netpolicy.WithOffline(context.Background(), true)
req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com/", nil)
_, err := client.Do(req)
fmt.Println("refused:", errors.Is(err, netpolicy.ErrOffline))
// Output: refused: true
```

More seams (raw `net.Dialer`, TLS over a dialed conn, `database/sql`,
websocket) are in `example_dialer_test.go`.

## Contract

- Refusals return `ErrOffline`; match with `errors.Is` (net/http wraps it in `*url.Error`).
- Loopback addresses and unix sockets are exempt: `--offline` means no network, not no self.
- `Guard` and `Install` are idempotent. `Install` mutates `http.DefaultTransport`; call it once at start-up, never concurrently with in-flight requests.
- Not covered, and not coverable from here: dependencies that call `net.Dial` with no dialer hook, callers holding a `*net.Dialer` (wrap `DialContext` at the call site), transports captured before `Install` ran. For those `--offline` stays advisory; consult `IsOffline` yourself.

## Neighbours

- `go/console/cli`: re-exports `WithOffline` / `IsOffline` and runs `Install` in `cli.New`.
- `go/core/scope`: filesystem path policy.
- `go/runtime/sideeffect`, `go/runtime/policy`: per-action authority decisions.
- `go/storage/kv/tidb`, `go/storage/kv/etcd`, `go/runtime/notify/sinks/email`, `go/transport/api/client`, `go/runtime/bus`: kit's own egress, each routed through a seam above.

## See also

- [cli-parity-guide.md](../../../docs/adopters/guides/cli-parity-guide.md), "Global Flags"
- [architecture.md](../../../docs/contributors/architecture/architecture.md), security surface map
