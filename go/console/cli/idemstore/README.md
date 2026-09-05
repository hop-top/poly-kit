# idemstore

## What it answers

"Where does the recorded result for `--idempotency-key <key>` live, and how is
it looked up and replayed?" This is the storage backend only. Flag
registration, output capture, and replay are the `RunE` middleware in
`hop.top/kit/go/console/cli` (`Root.WrapRunE`, `WithIdempotencyStore`).

## Use it when

- production CLI needs replay across invocations → `idemstore.OpenSQLite(path, ttl)`, path from `xdg.StateFile("<tool>", "idempotency.db")`
- tests or one-shot processes → `idemstore.Memory()`
- isolated sqlite without a file → `idemstore.OpenSQLite(":memory:", ttl)`
- install on a kit root → `cli.WithIdempotencyStore(store)`; `nil` disables replay
- custom backend → implement `idemstore.Store` (`Lookup`, `Record`, `Close`)

## Quick start

```go
store, err := idemstore.OpenSQLite(":memory:", idemstore.DefaultTTL)
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

## Contract

- `Lookup` returns `(Result{}, false, nil)` for unknown or expired keys and
  `(Result{}, false, err)` only on backend errors.
- `Record` is last-write-wins per key. `Result.Recorded` is stamped with
  `time.Now().UTC()` when zero.
- `Result.Output` is opaque bytes, replayed verbatim to stdout. Redaction
  before recording is the adopter's responsibility; the store never redacts.
- `Result.ExitCode` mirrors the original run's exit code (1 for unstructured
  failures, `output.Error.ExitCode` otherwise).
- SQLite TTL: zero or negative `ttl` becomes `DefaultTTL` (24h). Expired rows
  are hidden from `Lookup` but never purged; GC is out of band.
  `Memory()` enforces no TTL.
- SQLite schema: table `idempotency(key text primary key, exit_code integer,
  output blob, recorded text)`, `recorded` as RFC3339Nano UTC. Parent
  directory is created with mode 0750.
- Persistence is per tool: one database per `<tool>`, no cross-tool replay.
- Middleware behavior: on a `Lookup` error the original command runs anyway
  (over-execute rather than refuse); flag-validation rejections are not
  recorded.

## Neighbours

- `hop.top/kit/go/console/cli`: `--idempotency-key` flag, `WrapRunE` replay
  middleware, `Idempotency` annotations.
- `hop.top/kit/go/storage/sqldb`: sqlite open and pragmas used by
  `OpenSQLite`.
- `hop.top/kit/go/core/xdg`: `StateFile` for the default database location.

## See also

- [Serve lifecycle contract](../../../../docs/contracts/serve-lifecycle.md)
- [Go primitives reference](../../../../docs/adopters/reference/go-primitives.md)
