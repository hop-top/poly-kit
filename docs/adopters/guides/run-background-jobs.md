# Run background jobs with the job queue

> Audience: Go tool authors who need work to outlive the request that
> asked for it: enqueue from a command or handler, process in a worker
> loop, retry with backoff. Package README:
> [`go/runtime/job`](../../../go/runtime/job/README.md).

Package `hop.top/kit/go/runtime/job` defines the backend-agnostic
`Service` interface, `Poller`, `RunOne`, `BackoffStrategy` and the state
machine. Engines live in sub-packages.

## Pick an engine

| Package | Storage | Use when |
|---------|---------|----------|
| `job/mock` | in-memory map | unit tests; no persistence |
| `job/durabletask` | SQLite `jobs` table | one binary, jobs survive restart, no server |
| `job/temporal`, `job/hatchet`, `job/restate` | external server | multiple worker hosts; see each sub-package README |

`durabletask.New(path)` takes a file path, or `""` for in-memory SQLite.
Call `Close` when done. Code against `job.Service` so tests swap in
`mock.New()`.

## Enqueue

```go
import (
    "hop.top/kit/go/runtime/job"
    "hop.top/kit/go/runtime/job/durabletask"
)

svc, err := durabletask.New("/var/lib/mytool/jobs.db")
if err != nil { return err }
defer svc.Close()

id, err := svc.Enqueue(ctx, job.EnqueueOpts{
    Queue:   "default",           // required
    Type:    "email.send",        // required; routes to a handler
    Payload: map[string]string{"to": "user@example.com"}, // JSON-marshalled
})
```

`EnqueueOpts` fields: `Queue`, `Type`, `Payload`, `Metadata`,
`Priority`, `MaxAttempts`, `ClaimTTL`, `ScheduledAt`, `Backoff`.

- Missing `Queue` or `Type` returns an error wrapping
  `domain.ErrValidation`.
- `MaxAttempts <= 0` becomes 3.
- `ScheduledAt` in the future delays the claim until then.
- Lower `Priority` number is claimed first; ties go oldest first.
- `ClaimTTL` (default 5m) is how long a claim lasts before it is stale.
  Both `durabletask` and `mock` honor the per-job value; zero keeps the
  5m default. `Heartbeat` extends by the same per-job TTL.

## Process: Poller

`Poller` is the long-running worker loop. `Run` returns `ctx.Err()` when
the context is canceled.

```go
p := &job.Poller{
    Service:  svc,
    Queue:    "default",
    WorkerID: "worker-1",
    Interval: 2 * time.Second, // default 5s, +/-25% jitter
    Handlers: job.HandlerMap{
        "email.send": func(ctx context.Context, j job.Job) error {
            var in struct{ To string }
            if err := json.Unmarshal(j.Payload, &in); err != nil {
                return err
            }
            return send(ctx, in.To)
        },
    },
}
err := p.Run(ctx)
```

Each iteration: release stale claims (throttled by `StaleInterval`,
default 30s), claim, route by `Type`, then `Complete` or `Fail`.

| Outcome | What the poller does |
|---------|----------------------|
| handler returns nil | `Complete` with no result |
| handler returns error | `Fail` with `Retry: true` (retried while attempts remain) |
| no handler for `j.Type` | `Fail` with `Retry: false`: terminal |
| handler panics | recovered; `Fail` with `Retry: true` and a `*job.PanicError` message (`panic: <value>`) |
| claim error or empty queue | sleeps `Interval`, tries again |

Notes: errors from `ReleaseStaleClaims`, `Claim`, `Complete` and `Fail`
do not stop the loop; they are logged on `Poller.Logger` (nil uses
`slog.Default()`). A handler panic is recovered and logged with its
stack; the poller keeps running. `RunOne` recovers the same way and
returns the `*job.PanicError`. Handlers that run longer than
`ClaimTTL` should call `svc.Heartbeat(ctx, j.ID)` or the job is released
and may run twice, so make handlers idempotent. The handler's return
value cannot carry a result; call `svc.Complete(ctx, id, result)`
yourself outside the poller if you need to store one.

## Process once: RunOne

For short-lived CLIs that should drain a job opportunistically on
startup:

```go
err := job.RunOne(ctx, svc, "default", "cli", handlers)
```

`RunOne` returns nil without doing anything when `JOB_RUNONE_DISABLE`
is set to any value, or when a cooldown marker file (in the OS temp
dir) is under 10 seconds old, or when the queue is empty. Otherwise it
claims one job, runs its handler and completes or fails it. A failing
handler returns the handler's error as well as failing the job.

## Retries and backoff

`Fail(ctx, id, job.FailOpts{Error: "...", Retry: true})` returns the job
to `pending` with `NextRunAt` set while `Attempts < MaxAttempts`; past
that, or with `Retry: false`, the job ends `failed`. Each `Claim`
increments `Attempts`.

`job.DefaultBackoff()`: 30s initial, 15m cap, factor 2, 25% jitter. Set
`EnqueueOpts.Backoff` to override; zero `Initial`, `Max`, `Factor` take
defaults, but `Jitter` zero means no jitter. `Compute(attempt)` returns
the delay for a 0-indexed attempt.

## Inspect and control

| Call | Effect |
|------|--------|
| `Get(ctx, id)` | one `*Job` (status, attempts, error, timestamps) |
| `List(ctx, job.JobQuery{Queue, Status, Type, Limit})` | matching jobs |
| `Cancel(ctx, id)` | pending or active job to `canceled` |
| `Timeout(ctx, id)` | mark active job `timeout` |
| `Heartbeat(ctx, id)` | extend an active claim |
| `ReleaseStaleClaims(ctx)` | expired active jobs back to `pending`; returns count |

Status values: `StatusPending`, `StatusActive`, `StatusSucceeded`,
`StatusFailed`, `StatusTimeout`, `StatusCancelled` (wire value
`canceled`). Allowed transitions: `pending` to `active` or `canceled`;
`active` to `succeeded`, `failed`, `timeout`, `pending`, `canceled`;
`failed` and `timeout` back to `pending`. Anything else returns an
error matching `domain.ErrInvalidTransition`; unknown IDs match
`domain.ErrNotFound`. `Claim` returns `(nil, nil)` when nothing is
ready.

## Events

Pass `job.WithPublisher(pub)` to an engine constructor with any
`domain.EventPublisher`. The `mock` and `durabletask` engines publish
two families of events:

- Lifecycle events on the `job.*` topics, source `kit.runtime.job`,
  payload `job.JobEvent` (`JobID`, `Queue`, `Type`, `Status`,
  `WorkerID`, `Error`). Fire-and-forget: a publisher error never fails
  the operation.
- The state machine's `kit.runtime.state.pre_transitioned` (synchronous,
  a returned error vetoes the transition) and `post_transitioned`
  events with a `From`/`To` payload.

| Call | Topic |
|------|-------|
| `Enqueue` | `job.created` |
| `Claim` | `job.claimed` |
| `Heartbeat` | `job.heartbeat` |
| `Complete` | `job.succeeded` |
| `Fail`, retry scheduled | `job.retried` |
| `Fail`, terminal | `job.failed`; also `job.dead_letter` when `Retry` was requested but attempts ran out |
| `Timeout` | `job.timeout` |
| `Cancel` | `job.cancelled` |
| `ReleaseStaleClaims` | `job.stale_released`, once per released job |

The `temporal`, `hatchet` and `restate` engines do not publish the
`job.*` lifecycle events. Topic grammar and catalog:
[domain events](../reference/domain-events.md).

## Testing

```go
svc := mock.New(job.WithNowFunc(func() time.Time { return fixed }))
```

`mock.New` accepts `WithNowFunc` to control backoff and stale-claim
timing and `WithPublisher` to observe events; it honors
`EnqueueOpts.ClaimTTL`, so stale-claim tests can use short TTLs. IDs are `job_<n>`.

## See also

- [Storage abstractions](../concepts/storage-abstractions.md)
- [Domain events](../reference/domain-events.md)
- Engine READMEs: [`durabletask`](../../../go/runtime/job/durabletask/README.md),
  [`mock`](../../../go/runtime/job/mock/README.md)
