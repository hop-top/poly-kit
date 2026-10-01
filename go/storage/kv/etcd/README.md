# etcd

## What it answers

A `kv` driver over an etcd cluster, every key namespaced under a prefix.
Wrong package for single-process state (`go/storage/kv/sqlite`) or for
bulk values on a SQL server (`go/storage/kv/tidb`).

## Use it when

- `kv.Config{Backend: "etcd", Endpoints: []string{"127.0.0.1:2379"}, Prefix: "app/"}` after a blank import of this package
- coordination data that several hosts read and write
- you hold a context: `kv.OpenContext` checks every endpoint against the network policy before the client exists
- the cluster has auth enabled: set `Username` and `Password` (and `TLS` for `https://`), never userinfo in an endpoint

```go
pw, err := secrets.Get(ctx, "etcd-password") // any secret.Store
if err != nil {
	return err
}
store, err := kv.OpenContext(ctx, kv.Config{
	Backend:   "etcd",
	Endpoints: []string{"https://etcd.example:2379"},
	Prefix:    "app/",
	Username:  "app",
	Password:  string(pw.Value),
	TLS:       &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
})
```

Direct callers pass the same as options: `etcd.NewContext(ctx, eps, "app/", etcd.WithAuth(user, pw), etcd.WithTLS(cfg))`.

## Contract

- Registered as `"etcd"` via `kv.RegisterBackendContext`; `Endpoints` is required, `Prefix` defaults to empty. Pulls in gRPC, protobuf and zap; a binary that never imports this package does not link them.
- Credentials: `Username`/`Password` (`etcd.WithAuth`) map to the client's own fields, the only ones it sends; it never reads URL userinfo. Set both or neither: the client ignores either alone, so a lone one is rejected. With credentials the client authenticates before the open returns, so a wrong password fails the open and an unreachable cluster blocks it until `ctx` ends. Neither value appears in an error, and printing a `kv.Config` (`%v`, `%#v`, `slog`) redacts `Password`, the `DSN` password and endpoint userinfo. No environment fallback: resolve the password (from `secret.Store`, env, a file) before building the Config.
- `TLS` (`etcd.WithTLS`) secures `https://` and `unixs://` endpoints, and `host:port` and `unix://` ones when set; `https://` without it uses the system roots. The client secures every endpoint as the first one asks, so a list mixing TLS and plaintext endpoints is rejected, as is `TLS` with an `http://` endpoint (the client would drop it silently).
- Offline handling differs from `tidb`: gRPC dials on its own background context and `clientv3.New` returns before connecting, so endpoints are checked with `netpolicy.CheckDial` at open time. `http(s)://` (scheme case-insensitive), `unix(s)://` and bare `host:port` forms are all reduced to a dial target; for `http(s)://` that is the URL host (`host:port`), so userinfo neither leaks into a refusal nor hides a loopback host.
- Endpoint forms are the client's own: bare `host:port`, `http(s)://`, `unix(s)://`. Any other scheme (`grpc://`, `dns:///`) is rejected at open on any context: the client would dial it verbatim as a TCP address, which never connects, and log it. Userinfo in any endpoint (`user:pass@host:2379`, `https://user:pass@host:2379`) is rejected too, the error naming only `host:2379` and pointing at `Username`/`Password`. Socket paths pass unchanged; an `@` there is a file or abstract name.
- No TTL: `*Store` is a `kv.Store` only.
- Not part of the kv-v1 cross-language corpus.
- Tests: `etcd_integration_test.go` starts an etcd container via testcontainers and skips under `-short` or when Docker is unhealthy; `TestEtcdIntegrationAuth` there enables auth in the container and opens with right, wrong and no credentials. `offline_test.go`, `credentials_open_test.go`, `credentials_test.go` and `dialtarget_test.go` run without a server.

## Neighbours

- `hop.top/kit/go/core/netpolicy`: `CheckDial`, the seam used here.
- `hop.top/kit/go/storage/kv`: the interface and `Open`.
- `hop.top/kit/go/storage/secret`: where the password should come from.

## See also

- [`doc.go`](../doc.go) in `kv`: why the two network drivers guard differently
