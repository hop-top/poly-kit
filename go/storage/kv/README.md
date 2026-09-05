# kv

key-value persistence abstractions and drivers.

## Backends

| Backend  | Driver package | Config fields        | TTL |
|----------|----------------|----------------------|-----|
| `sqlite` | `kv/sqlite`    | `Path`               | yes |
| `badger` | `kv/badger`    | `Path`               | yes |
| `etcd`   | `kv/etcd`      | `Endpoints`, `Prefix`| no  |
| `tidb`   | `kv/tidb`      | `DSN`, `Table`       | no  |

`kv.Open` dispatches on `Config.Backend` through a registry that each
driver populates from its own `init`. Importing the driver is what makes
its name valid:

```go
import (
    "hop.top/kit/go/storage/kv"
    _ "hop.top/kit/go/storage/kv/etcd"
)

store, err := kv.Open(kv.Config{
    Backend:   "etcd",
    Endpoints: []string{"127.0.0.1:2379"},
    Prefix:    "app/",
})
```

## Network policy

`kv.OpenContext` honors the `--offline` policy carried by its context,
including on the initial connect: a remote `tidb` or `etcd` is refused,
while loopback, unix sockets and the local file backends stay reachable.

```go
store, err := kv.OpenContext(ctx, kv.Config{
    Backend:   "etcd",
    Endpoints: []string{"etcd.internal:2379"},
})
```

Drivers opt in by registering with `kv.RegisterBackendContext` instead of
`kv.RegisterBackend`; all four shipped drivers do. `kv.Open` keeps its
signature and supplies a background context, so it connects without
consulting the policy — prefer `OpenContext` wherever a context is at
hand. A third-party driver registered through the older `kv.Opener` still
works, because `OpenContext` falls back to it, but connects unpoliced.

The two network drivers reach the policy by different seams. `tidb` routes
the MySQL driver's `Config.DialFunc` through `netpolicy.GuardDial`. `etcd`
cannot use that seam — gRPC dials on its own background context, so the
marker never arrives, and `clientv3.New` returns before connecting at all —
so its endpoints are checked with `netpolicy.CheckDial` at open time.

Registration is by import rather than by build tag so a binary carries
only the dependencies of the backends it opens — importing `kv` alone
pulls neither BadgerDB nor etcd's gRPC stack. Naming a backend whose
driver is absent reports the package to import; `kv.Backends()` lists
what the current binary has registered.

## Keys bind as TEXT

The SQLite driver declares `key TEXT PRIMARY KEY`, and every implementation
must bind keys as TEXT rather than BLOB. This is a correctness requirement,
not a style preference.

SQLite treats TEXT and BLOB as distinct storage classes and compares
storage class before value, so a key written as a BLOB never equals the
same bytes written as TEXT. Nothing raises an error when this goes wrong.
Instead, reads become silent misses, `INSERT OR REPLACE` writes a shadow
row beside the one it should have replaced, and prefix scans return
disjoint sets.

Two consequences are easy to miss:

- **The column declaration is not what carries the contract; the bind type
  is.** The table is created with `CREATE TABLE IF NOT EXISTS`, so whichever
  process opens the file first wins and any other implementation's
  declaration is inert. Declaring `TEXT` proves nothing about what a peer
  actually binds.
- **Keys are arbitrary byte sequences.** Go models them as `string`, which
  admits bytes that are not valid UTF-8. A port whose string type cannot
  hold those bytes must take a byte slice and bind it as TEXT without UTF-8
  validation rather than reaching for BLOB.

TEXT also gives the ordering callers rely on: the default `BINARY`
collation is `memcmp` over stored bytes, which matches Go string
comparison, so ordered scans agree across languages even for non-UTF-8
keys. Note that `List` itself issues no `ORDER BY`; its result is a set.

## Cross-language gate

A test suite that round-trips within a single language cannot catch a
binding mismatch, because both sides agree with themselves. The gate that
actually crosses the boundary is driven from the shared corpus in
[`contracts/kv-v1/keys.json`](../../../contracts/kv-v1/keys.json):
`sqlite/crosslang_test.go` writes the corpus and has another
implementation read it back, and vice versa.

Because it needs more than one toolchain present, it runs in the parity
job rather than in `go test ./...`:

```bash
make test-parity-kv
```

The cross-process cases are gated behind the `KV_CROSSLANG` environment
variable, so a plain `go test ./...` stays free of any other toolchain.
The remaining cases in that file — including the one pinning the key
column's storage class — always run.
