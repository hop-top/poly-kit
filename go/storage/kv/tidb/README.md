# tidb

## What it answers

A `kv` driver over TiDB or any MySQL-compatible server: one table,
`k VARCHAR(512) PRIMARY KEY, v LONGBLOB`. Wrong package for local state
(`go/storage/kv/sqlite`) or for coordination on an etcd cluster
(`go/storage/kv/etcd`).

## Use it when

- `kv.Config{Backend: "tidb", DSN: "user:pass@tcp(host:4000)/db", Table: "kv"}` after a blank import of this package
- shared state on a database you already run; `Table` defaults to `kv.DefaultTable` (`kv`)
- you hold a context: `kv.OpenContext` pings under the network policy, so an offline remote fails at open rather than on the first query

## Contract

- Registered as `"tidb"` via `kv.RegisterBackendContext`; `DSN` is required and parsed by `go-sql-driver/mysql`. The table name is validated before use.
- The dial goes through `netpolicy.GuardDial` via the driver's `DialFunc`, which the MySQL driver calls with the context of the triggering query; every `Get`/`Put`/`Delete`/`List` is therefore guarded, not only the open.
- Keys are capped at 512 bytes by the column. No TTL.
- `List` uses range scans (`k >= ? AND k < successor`); an all-`0xff` prefix scans to the end.
- Not part of the kv-v1 cross-language corpus.
- Tests: integration and regression tests start `mysql:8` via testcontainers and skip under `-short` or when Docker is unhealthy; `offline_test.go` runs without a server.

## Neighbours

- `hop.top/kit/go/core/netpolicy`: `GuardDial`, the seam used here.
- `hop.top/kit/go/storage/kv`: the interface and `Open`.

## See also

- [`doc.go`](../doc.go) in `kv`: why the two network drivers guard differently
