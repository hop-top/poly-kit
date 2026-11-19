# dek

## What it answers

How a kit command tree is served as the core side of the dek contract
under `/v1/dek`: the hello, a capability manifest generated from the
commands' annotations and flags, invoke for read, write and
dry-run calls, `whoami`, plans applied by id, and background jobs. Each kit CLI serves its own surface; REST over the same
tree is `go/transport/api`, and the executor both share is
`go/transport/cmdsurface`.

## Use it when

- serve it from the api service → `service.APIConfig.Dek` (`service.DekConfig`)
- listen on a unix socket instead of TCP → `service.APIConfig.Addr: "unix:/abs/path.sock"`
- build the manifest without serving → `dek.Manifest(descriptors, dek.Options{})`
- mount on your own router → `dek.New(dek.Config{Projection: pcfg})` then `Core.Mount(router)`, with `pcfg` from `cmdsurface.Projection`
- choose an approval class per command → `Config.Gate`, `Options.Gate`
- read the manifest ETag → `Core.ETag`
- keep plans or jobs somewhere durable → `Config.Plans` (`PlanStore`), `Config.Jobs` (`JobStore`)
- name the caller yourself → `Config.Principal`

## Contract

- Routes: `GET /v1/dek/hello`, `/whoami`, `/capabilities` (ETag, 304),
  `POST /v1/dek/invoke`, `POST /v1/dek/plans/{id}/apply`,
  `GET /v1/dek/jobs[/{id}]`. Errors are `{"reason": "..."}`.
- A destructive capability needs a dry run first and is applied by plan
  id, after a fresh dry run agrees with the reviewed plan; classes
  beyond `free` and `apply` are blocked.
- A command annotated `kit/long-running` runs as a job.
- Advertises contract 1.33.
- Bodies conform to the contract's JSON Schemas, checked by the tests
  against the copies in `testdata/contract`.

The mapping from annotations to capability fields, the extension
points (apply, proposals, jobs, bus) and the listen address rules are
in [`docs/adopters/reference/dek-surface.md`](../../../docs/adopters/reference/dek-surface.md).
