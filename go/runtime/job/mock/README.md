# mock

## What it answers

The in-memory `job.Service` for tests: a map behind a mutex, full state-machine enforcement, no I/O. Wrong package for anything that must survive a process restart (use `hop.top/kit/go/runtime/job/durabletask`).

## Use it when

- a handler test needs a queue that behaves like production → `mock.New()`
- a test must control time for backoff or stale claims → `mock.New(job.WithNowFunc(fixed))`
- a test must observe lifecycle events → `mock.New(job.WithPublisher(pub))`

## Quick start

```go
ctx := context.Background()
svc := mock.New()

id, err := svc.Enqueue(ctx, job.EnqueueOpts{Queue: "default", Type: "email.send"})
if err != nil {
	panic(err)
}
j, err := svc.Claim(ctx, "default", "worker-1")
if err != nil {
	panic(err)
}
fmt.Println(j.ID, j.Status)

if err := svc.Complete(ctx, id, nil); err != nil {
	panic(err)
}
got, _ := svc.Get(ctx, id)
fmt.Println(got.Status)
// job_1 active
// succeeded
```

Verified by `example_test.go` in this directory.

## Contract

- IDs are `job_<n>`, sequential per engine instance.
- Claim order: priority ascending, then `created_at` ascending.
- `Fail` with `Retry: true` returns the job to `pending` with a backoff-computed `NextRunAt` while attempts remain.
- Safe for concurrent use; no persistence.

## Neighbours

- `hop.top/kit/go/runtime/job`: the interface, poller, and backoff this package implements.
- `hop.top/kit/go/runtime/job/durabletask`: same semantics on SQLite.
