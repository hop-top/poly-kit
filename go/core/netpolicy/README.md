# netpolicy

## What it answers

Is this process allowed to reach the network right now, and which hosts. It owns the
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
- a tool must reach only named hosts: `WithAllowHosts(ctx, patterns)` (config key `netpolicy.allow_hosts`; `cli.New` stamps it), `ValidateAllowHosts` for the config, `WithAudit` for refusals
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
- A refusal names method, scheme, host and path only: query, fragment and userinfo never appear, as they may carry credentials. net/http's `*url.Error` still quotes the full URL around it, so keep credentials out of URLs (headers instead).
- Allowlist refusals return `ErrHostNotAllowed` the same way. Entries are an exact host or `*.suffix` (one or more labels below the suffix, never the suffix itself, never an IP literal); hosts and entries compare lower case, no trailing dot, IDNA in ASCII form. An empty list is no list; invalid entries never match. `--offline` wins when both apply.
- Redirects: `http.Client` calls the transport once per hop, so `Guard` judges every redirect target; a refused hop fails with the same error.
- Cross-origin redirect headers: `NewClient(base)` returns an `*http.Client` over `Guard(base)` whose `CheckRedirect` refuses a hop to another origin (scheme, host or port differs) when the original request carries any header beyond `Accept`, `Accept-Encoding`, `Accept-Language`, `User-Agent` and `Content-Type`. `net/http` alone strips only `Authorization`, `Cookie` and a few others cross-origin and forwards `X-Api-Key` or any custom header. The refusal is a `*RedirectError` (`errors.Is(err, ErrCrossOriginHeaders)`), audited as op `redirect`, and nothing reaches the second origin; same-origin hops are unchanged. A client built any other way is not covered: install `CheckRedirect(next)` on it; replacing `CheckRedirect` outright drops the check, and `next` runs only after the check passes (nil `next` keeps the default 10-hop limit). Matches the Rust `GuardedClient`; vectors in `contracts/netpolicy-v1`.
- `WithAudit(ctx, fn)` reports every refusal (offline and allowlist) as a `Refusal{Reason, Op, Dest}` with the same stripped destination; `cli.New` logs them at warn.
- Loopback addresses and unix sockets are exempt: `--offline` means no network, not no self.
- Loopback is `localhost` (exact), 127.0.0.0/8, `::1` and v4-mapped loopback, with or without a port. A portless bracketed host (`[::1]`, the URL host of `http://[::1]/`) is loopback only when the brackets hold an IPv6 address; `[127.0.0.1]` is remote.
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
- [contracts/netpolicy-v1](../../../contracts/netpolicy-v1/README.md): shared loopback, allowlist, dial and refusal vectors (Go and Rust runners)
