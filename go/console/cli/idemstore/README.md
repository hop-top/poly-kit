# idemstore

## What it answers

"Where does the recorded result for `--idempotency-key <key>` live, and how is
it looked up and replayed?" This is the storage backend only. Flag
registration, output capture, and replay are the `RunE` middleware in
`hop.top/kit/go/console/cli` (`Root.WrapRunE`, `WithIdempotencyStore`).

## Use it when

- production CLI needs replay across invocations → `sqlite.Open(path, ttl)` from `hop.top/kit/go/console/cli/idemstore/sqlite`, path from `xdg.StateFile("<tool>", "idempotency.db")`
- tests or one-shot processes → `idemstore.Memory()`
- isolated sqlite without a file → `sqlite.Open(":memory:", ttl)`
- install on a kit root → `cli.WithIdempotencyStore(store)`; `nil` disables replay
- custom backend → implement `idemstore.Store` (`Lookup`, `Record`, `Close`)
- served transports' replay store → `service.WithServeIdempotencyStore(store)`; default `serve-idempotency.db` in the tool's state dir

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

## Contract

- `Lookup` returns `(Result{}, false, nil)` for unknown or expired keys and `(Result{}, false, err)` only on backend errors.
- `Record` is last-write-wins per key. `Result.Recorded` is stamped with `time.Now().UTC()` when zero.
- `Result.Output` is opaque bytes, replayed verbatim to stdout. Redaction
  before recording is the adopter's responsibility; the store never redacts.
- `Result.ExitCode` mirrors the original run's exit code (1 for unstructured failures, `output.Error.ExitCode` otherwise).
- TTL and schema of the sqlite `Store`: see [`sqlite/`](sqlite/README.md).
  `Memory()` enforces no TTL.
- Persistence is per tool: one database per `<tool>`, no cross-tool replay.
- Middleware behavior: on a `Lookup` error the original command runs anyway (over-execute rather than refuse); flag-validation rejections are not recorded.
- Keys are scoped by caller. A key typed on the command line is stored as
  typed. A key that reaches a served command (the `Idempotency-Key` header,
  the socket's `idempotency_key`, or the flag set in a request) is stored as
  `served:<hash>` of the key and the caller's scope
  (`cmdsurface.ScopeIdempotencyKey`), so one caller's key never replays
  another caller's output and served keys never meet local ones. The scope is
  the tenant and principal the transport verified; for a caller it did not
  verify, the service, claimed caller and, without one, the client host.
  Unverified callers from one host share a scope: authenticate the service
  to scope per caller.

## Neighbours

- `hop.top/kit/go/console/cli`: `--idempotency-key` flag, `WrapRunE` replay middleware, `Idempotency` annotations.
- `hop.top/kit/go/console/cli/idemstore/sqlite`: the sqlite `Store` (`sqlite.Open`). A separate package, so a CLI that never opens it links no SQLite.
- `hop.top/kit/go/storage/sqldb`: sqlite open and pragmas used by `sqlite.Open`.
- `hop.top/kit/go/core/xdg`: `StateFile` for the default database location.

## See also

- [Serve lifecycle contract](../../../../docs/contracts/serve-lifecycle.md)
- [Go primitives reference](../../../../docs/adopters/reference/go-primitives.md)
