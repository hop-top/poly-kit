# restate

## What it answers

A `job.Service` adapter for Restate that models each job as a virtual object keyed by job ID and talks to the ingress HTTP API. Wrong package when no server exists (use `durabletask` or `mock`).

## Use it when

- jobs must run as Restate virtual objects → `restate.New("http://localhost:8080")`
- config comes from a map → `restate.ValidateConfig(cfg)` before dialing

## Contract

Wire assumptions:

- `New` takes the ingress endpoint URL; the adapter builds an `ingress.Client` from `github.com/restatedev/sdk-go/ingress`.
- Job IDs are generated locally and become the virtual object key.
- Current state lives in a local cache for synchronous reads; the ingress calls each method maps to are documented on the method's doc comment (`go doc ./restate Engine`) and their wiring is pending.
- `Claim` selects from the local cache; queue-based claiming on the server side needs a queue-manager service (see the `Claim` doc comment).
- `ValidateConfig` requires `endpoint` (valid URL).

## Run it locally

- `./install.sh` installs `restate-server` (brew on macOS, `docker.io/restatedev/restate:1.3.1` otherwise).
- `docker compose -f testdata/docker-compose.yml up` exposes ingress on `8080` and admin on `9070`.
- `go test ./...` here needs no server: tests run against the local cache.

## Neighbours

- `hop.top/kit/go/runtime/job`: interface, poller, backoff.
- `hop.top/kit/go/runtime/job/temporal`, `hop.top/kit/go/runtime/job/hatchet`: the other server-backed engines.
