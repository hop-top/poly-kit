# dek

## What it answers

How a kit command tree is served as the core side of the dek contract
under `/v1/dek`: the hello, a capability manifest generated from the
commands' annotations and flags, and invoke for read, write and
dry-run calls. Each kit CLI serves its own surface; REST over the same
tree is `go/transport/api`, and the executor both share is
`go/transport/cmdsurface`.

## Use it when

- serve it from the api service → `service.APIConfig.Dek` (`service.DekConfig`)
- listen on a unix socket instead of TCP → `service.APIConfig.Addr: "unix:/abs/path.sock"`
- build the manifest without serving → `dek.Manifest(descriptors, dek.Options{})`
- mount on your own router → `dek.New(dek.Config{Projection: pcfg})` then `Core.Mount(router)`, with `pcfg` from `cmdsurface.Projection`
- choose an approval class per command → `Config.Gate`, `Options.Gate`
- read the manifest ETag → `Core.ETag`

## Contract

- Routes: `GET /v1/dek/hello`, `GET /v1/dek/capabilities` (ETag, 304),
  `POST /v1/dek/invoke`. Errors are `{"reason": "..."}`.
- A destructive capability needs a dry run first; any approval class
  other than `free` is refused on a direct call.
- Bodies conform to the contract's JSON Schemas, checked by the tests
  against the copies in `testdata/contract`.

The mapping from annotations to capability fields, the extension
points (apply, proposals, jobs, bus) and the listen address rules are
in [`docs/adopters/reference/dek-surface.md`](../../../docs/adopters/reference/dek-surface.md).
