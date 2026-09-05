# hatchet

## What it answers

A `job.Service` adapter for Hatchet that bridges Hatchet's push dispatch onto the pull-based `Claim`. The Hatchet SDK is not imported: the adapter takes a `HatchetClient` you implement over `github.com/hatchet-dev/hatchet/sdks/go`. Wrong package when no server exists (use `durabletask` or `mock`).

## Use it when

- jobs must run as Hatchet workflow runs → wrap the SDK client in `HatchetClient`, then `hatchet.New(client)`
- you scaffold or test the wiring without a server → `hatchet.New(nil)` (local-only cache)
- config comes from a map → `hatchet.ValidateConfig(cfg)` before dialing

## Contract

Wire assumptions:

- `Enqueue` → `TriggerRun(ctx, queue, input)`; `Cancel` → `CancelRun`; `Get` → `GetRun`. Everything else works on the adapter's local cache.
- `Claim` reads a per-queue channel fed by the adapter's internal worker; with a nil client it selects from the cache by priority, then FIFO.
- `Heartbeat` and `ReleaseStaleClaims` are local-cache operations; Hatchet owns liveness on the server side.
- `ValidateConfig` requires `server_url` (valid URL) and `api_token`.
- The mapping from each method to the SDK call is documented on the method's doc comment (`go doc ./hatchet Engine`).

## Run it locally

- `./install.sh` pulls `ghcr.io/hatchet-dev/hatchet:v0.53.1` (Docker is the only option).
- `docker compose -f testdata/docker-compose.yml up` starts Hatchet on `8080` and `7070` with Postgres and RabbitMQ.
- `go test ./...` here needs no server: tests use a stub `HatchetClient` or `New(nil)`.

## Neighbours

- `hop.top/kit/go/runtime/job`: interface, poller, backoff.
- `hop.top/kit/go/runtime/job/temporal`, `hop.top/kit/go/runtime/job/restate`: the other server-backed engines.
