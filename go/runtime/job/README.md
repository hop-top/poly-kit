# job

## What it answers

A backend-agnostic queue for work that must outlive the request: enqueue, claim, complete, fail with backoff, cancel, list. It is the wrong package for in-process fan-out (use `hop.top/kit/go/runtime/bus`) and for outbound human notifications (use `hop.top/kit/go/runtime/notify`).

## Use it when

- a command must hand work to a worker and return → `svc.Enqueue(ctx, job.EnqueueOpts{...})`
- a worker loop must claim, route, and complete → `job.Poller` or `job.RunOne(ctx, svc, queue, workerID, handlers)`
- a unit test needs the state machine without storage → `mock.New()`
- a single binary needs durability with no server → `durabletask.New(path)`

## Quick start

```go
ctx := context.Background()
svc := mock.New()

// Enqueue a job.
id, err := svc.Enqueue(ctx, job.EnqueueOpts{
	Queue: "default",
	Type:  "email.send",
	Payload: map[string]string{
		"to":      "user@example.com",
		"subject": "Welcome",
	},
})
if err != nil {
	panic(err)
}

// Claim and process.
j, err := svc.Claim(ctx, "default", "worker-1")
if err != nil {
	panic(err)
}

fmt.Printf("claimed job %s (type=%s)\n", j.ID, j.Type)

// Complete.
if err := svc.Complete(ctx, id, map[string]string{"status": "sent"}); err != nil {
	panic(err)
}

got, _ := svc.Get(ctx, id)
fmt.Printf("final status: %s\n", got.Status)
```

Verified by `example_test.go` in this directory.

## Backends

| Package | Storage | External service | Claim semantics |
|---------|---------|------------------|-----------------|
| [`durabletask/`](durabletask/README.md) | SQLite `jobs` table | none | atomic priority + FIFO, stale-claim release |
| [`hatchet/`](hatchet/README.md) | Hatchet workflow runs | Hatchet server (adapter injects a client) | push-to-pull bridge; local cache when no client |
| [`mock/`](mock/README.md) | in-memory map | none | priority, then FIFO |
| [`restate/`](restate/README.md) | Restate virtual object per job | Restate ingress | local cache; ingress wiring pending |
| [`temporal/`](temporal/README.md) | one Temporal workflow per job | Temporal server | signal by ID; queue claim via visibility List |

## Contract

- State machine: `pending → active | canceled`; `active → succeeded | failed | timeout | pending | canceled`; `failed | timeout → pending` (retry). Every backend enforces it through `domain.StateMachine`.
- Claim order is priority ascending, then `created_at` ascending, within one queue.
- `DefaultBackoff`: 30s initial, 15m max, factor 2, 25% jitter.
- Lifecycle events publish on `job.*` topics (`TopicCreated`, `TopicFailed`, ...) through `domain.EventPublisher`; pass one with `job.WithPublisher`.
- `RunOne` is disabled when `JOB_RUNONE_DISABLE` is set.

## Neighbours

- `hop.top/kit/go/runtime/domain`: state machine and event publisher the queue reuses.
- `hop.top/kit/go/runtime/bus`: in-process, fire-and-forget; no durability.
- `hop.top/kit/go/runtime/notify`: at-least-once delivery to humans, not to workers.

## See also

- [Domain events reference](../../../docs/adopters/reference/domain-events.md)
