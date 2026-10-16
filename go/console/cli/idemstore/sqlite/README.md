# sqlite

## What it answers

Where does a tool keep `--idempotency-key` results across invocations?
In one SQLite file per tool, with a TTL. Wrong package for the
`Store` interface and the in-process `Memory()` backend (`idemstore`).

## Use it when

- production CLI needs replay across invocations → `sqlite.Open(path, ttl)`, path from `xdg.StateFile("<tool>", "idempotency.db")`, passed to `cli.WithIdempotencyStore`
- isolated sqlite without a file → `sqlite.Open(":memory:", ttl)`

## Quick start

```go
store, err := sqlite.Open(":memory:", idemstore.DefaultTTL)
if err != nil {
    fmt.Println("open:", err)
    return
}
defer store.Close()
ctx := context.Background()

_, hit, _ := store.Lookup(ctx, "deploy-42")
fmt.Println("hit before record:", hit)

_ = store.Record(ctx, "deploy-42", idemstore.Result{
    ExitCode: 0,
    Output:   []byte("{\"deployed\":true}\n"),
})
r, hit, _ := store.Lookup(ctx, "deploy-42")
fmt.Println("hit after record:", hit)
fmt.Print(string(r.Output))
```

Verified by `example_test.go` in this directory.

## Contract

- `Open` returns an `idemstore.Store`; the `idemstore` contract applies.
- TTL: zero or negative `ttl` becomes `idemstore.DefaultTTL` (24h).
  Expired rows are hidden from `Lookup` and deleted: all of them when the
  store opens, then up to 1000 on the first `Record` after each interval
  (the TTL, at most one hour); a full batch lets the next `Record`
  continue. A failed purge never fails the open or the `Record`.
- Schema: table `idempotency(key text primary key, exit_code integer,
  output blob, recorded text)`, `recorded` as RFC3339Nano UTC. Parent
  directory is created with mode 0750.
- An empty path is an error.

## Neighbours

- `hop.top/kit/go/console/cli/idemstore`: `Store`, `Result`, `DefaultTTL`, `Memory()`.
- `hop.top/kit/go/storage/sqldb`: sqlite open and pragmas used by `Open`.
- `hop.top/kit/go/console/cli/service`: opens one of these as the served transports' default replay store.

## See also

- [Serve lifecycle contract](../../../../../docs/contracts/serve-lifecycle.md)
