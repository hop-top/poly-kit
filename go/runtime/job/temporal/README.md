# temporal

## What it answers

A `job.Service` where each job is one Temporal workflow (`JobWorkflow`) driven by signals. Wrong package when you need a competing-consumer queue with no server (use `durabletask`) or a test double (use `mock`).

## Use it when

- jobs must live in Temporal with its retries, timers, and visibility → `temporal.New(client)`
- a worker knows the job ID and must claim it directly → `eng.ClaimByID(ctx, id, workerID)`
- config comes from a map → `temporal.ValidateConfig(cfg)` before dialing

## Contract

Wire assumptions:

- One workflow per job, ID `job-<id>`, task queue `job-queue` (`TaskQueue`).
- Operations map to signals: `claim`, `complete`, `fail`, `timeout`, `cancel`, `heartbeat`; `Get` is the `state` query.
- `List` reads visibility through search attributes `JobQueue`, `JobStatus`, `JobType`; register them on the namespace or `List` returns nothing.
- `Claim` finds the next pending job via `List`, then signals it: not a competing-consumer poll.
- `ReleaseStaleClaims` is a no-op; Temporal timers own expiry.
- `ValidateConfig` requires `server_address` (host:port) and `namespace`.

## Run it locally

- `./install.sh` installs the `temporal` CLI (brew, binary, or `temporalio/auto-setup` image fallback).
- `docker compose -f testdata/docker-compose.yml up` starts `temporalio/auto-setup:1.25.2` on `7233` (gRPC) and `8233` (UI) with SQLite persistence.
- `go test ./...` here needs no server: the suite runs on `testsuite.TestWorkflowEnvironment`.

## Neighbours

- `hop.top/kit/go/runtime/job`: interface, poller, backoff.
- `hop.top/kit/go/runtime/job/hatchet`, `hop.top/kit/go/runtime/job/restate`: the other server-backed engines.
