# durabletask

## What it answers

A `job.Service` on SQLite: one `jobs` table, atomic FIFO claim, priority ordering, backoff scheduling, stale-claim release. No server, pure Go. Wrong package when several worker hosts share one queue (use a server-backed engine: `temporal`, `hatchet`, `restate`).

## Use it when

- a single binary needs jobs that survive restart → `durabletask.New("/var/lib/app/jobs.db")`
- a test needs real SQL semantics without a file → `durabletask.New("")`
- a poller must recover jobs from crashed workers → `svc.ReleaseStaleClaims(ctx)`
- `jobs.db` grows because finished jobs are kept → `svc.Prune(ctx, cutoff)`

## Quick start

```go
ctx := context.Background()
svc, err := durabletask.New("") // "" = in-memory SQLite; pass a file path for durability
if err != nil {
	panic(err)
}
defer svc.Close()

id, err := svc.Enqueue(ctx, job.EnqueueOpts{Queue: "default", Type: "report.build"})
if err != nil {
	panic(err)
}
j, err := svc.Claim(ctx, "default", "worker-1")
if err != nil {
	panic(err)
}
fmt.Println(j.ID == id, j.Status)

if err := svc.Complete(ctx, id, nil); err != nil {
	panic(err)
}
got, _ := svc.Get(ctx, id)
fmt.Println(got.Status)
// true active
// succeeded
```

Verified by `example_test.go` in this directory.

## Contract

- Wire: a SQLite file (or `""` for in-memory) owned by this engine; the schema is created on `New`.
- Claim is a single atomic statement: priority ascending, then `created_at` ascending, within the queue.
- `Heartbeat` extends the claim expiry; `ReleaseStaleClaims` returns expired active jobs to `pending`.
- `EnqueueOpts.ClaimTTL` sets the per-job claim lifetime (zero keeps the 5m default).
- Publishes `job.*` lifecycle events (`JobEvent` payload) when built with `job.WithPublisher`.
- Job ids are TypeIDs: `dtj_` + 26 base32 chars (`hop.top/kit/go/core/id`). Ids are opaque to the engine, so rows from earlier builds (`dtj_` + 8 hex) still load and run.
- `Prune(ctx, olderThan, statuses...) (int, error)` (also `job.Pruner`) deletes finished jobs (`succeeded`, `failed`, `timeout`, `canceled`) whose `updated_at` is strictly before `olderThan` and returns the count. No `statuses` = all finished; listing `pending` or `active` returns `domain.ErrValidation` and deletes nothing. Repeat calls are safe. Kit provides the call only: nothing runs it on a schedule and no retention is applied by default, so finished jobs are kept until the caller prunes.
- Call `Close` to release the connection.

## Run it locally

`install.sh` is a no-op: nothing to install.

## Neighbours

- `hop.top/kit/go/runtime/job/mock`: same semantics, no storage.
- `hop.top/kit/go/storage/sqlstore`: the general SQLite store; this engine keeps its own table.
