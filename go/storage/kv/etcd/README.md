# etcd

## What it answers

A `kv` driver over an etcd cluster, every key namespaced under a prefix.
Wrong package for single-process state (`go/storage/kv/sqlite`) or for
bulk values on a SQL server (`go/storage/kv/tidb`).

## Use it when

- `kv.Config{Backend: "etcd", Endpoints: []string{"127.0.0.1:2379"}, Prefix: "app/"}` after a blank import of this package
- coordination data that several hosts read and write
- you hold a context: `kv.OpenContext` checks every endpoint against the network policy before the client exists

## Contract

- Registered as `"etcd"` via `kv.RegisterBackendContext`; `Endpoints` is required, `Prefix` defaults to empty. Pulls in gRPC, protobuf and zap; a binary that never imports this package does not link them.
- Offline handling differs from `tidb`: gRPC dials on its own background context and `clientv3.New` returns before connecting, so endpoints are checked with `netpolicy.CheckDial` at open time. `http(s)://`, `unix(s)://` and bare `host:port` forms are all reduced to a dial target; unrecognised forms are still checked as TCP.
- No TTL: `*Store` is a `kv.Store` only.
- Not part of the kv-v1 cross-language corpus.
- Tests: `etcd_integration_test.go` starts an etcd container via testcontainers and skips under `-short` or when Docker is unhealthy; `offline_test.go` runs without a server.

## Neighbours

- `hop.top/kit/go/core/netpolicy`: `CheckDial`, the seam used here.
- `hop.top/kit/go/storage/kv`: the interface and `Open`.

## See also

- [`doc.go`](../doc.go) in `kv`: why the two network drivers guard differently
