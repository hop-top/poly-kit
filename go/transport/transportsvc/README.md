# transportsvc

## What it answers

How a transport of your own fronts a kit command tree and gets the
serve lifecycle with it: reflection, the policy path, surface pinning,
readiness, address reporting and ordered idempotent stop, all
centralized. Implement `Transport`; everything else is here. A
transport already shipped is `go/transport/socket`; the lifecycle
contract itself lives in `go/console/serve`.

## Use it when

- front the tree over a transport → implement `Bind` / `Serve` / `Close`, then `NewTransportService(name, root, surface, tr, opts...)`
- reach commands the bridge does not enable by default → `Expose("*")`, then `Hide("widget delete")` for exceptions
- supply a custom `Policy` or `Runner` → `WithBridgeOptions(...)`, or `WithBridgeOptionsFunc(fn)` when the options are known only at Start
- fail fast on bad configuration → `WithValidate(fn)`
- be gated by a policy table, or ordered after another service → `WithClass(sideEffect, network)`, `WithDependsOn(names...)`
- enumerate what the transport exposes, excluded commands included → `TransportService.Bridge()`, `Bridge.NonInvocable()`

## Quick start

```go
type Transport interface {
    Bind(ctx context.Context) (addr string, err error)
    Serve(ctx context.Context, inv Invoker) error
    Close(ctx context.Context) error
}
```

`Bind` acquires everything that can fail deterministically and returns
the resolved address, or `""`. `Serve` accepts work until `ctx` is
canceled or it fails, and is called only after `Bind` succeeds; `nil`
after cancellation is a clean stop. `Close` releases what `Bind`
acquired, **must make `Serve` return**, and may be called more than
once.

## Contract

- `name` must match `^[a-z][a-z0-9-]*$` and must not be `all`, `none`, or `list`. An invalid name, or a nil `Transport`, panics at construction: both are wiring bugs in `main`.
- The `Invoker` a transport receives has the surface already pinned and the policy gate already wired. A transport never reads an annotation or gates a command, and cannot invoke as another surface.
- Errors are the bridge's, mapped by the transport onto its wire format, not decided by it: `ErrUnknownCommand`, `ErrSurfaceNotEnabled`, `ErrDestructiveBlocked`, `ErrPermissionDenied`.
- Meta reaches the bridge unchanged except for `Surface`, which the seam pins. A transport fills `Caller` and `Tenant` only from an identity it verified; the bridge grants nothing on a claim.
- Reflection happens once, at `Start`, so the tree is complete: do not cache leaves.
- Readiness is reported after `Bind` returns nil; `Stop` calls `Close` once.
- Rules apply in order, so `Expose("*")` then `Hide("widget delete")` reaches everything but that leaf. Patterns are `"widget add"`, `"widget *"`, or `"*"`.

## Neighbours

- `go/transport/socket`: a complete transport on this seam.
- `go/console/serve`: the lifecycle contract this seam implements. It cannot live there: the command-tree half reaches `cmdsurface`, which reaches `cmdreflect`, which reaches `go/console/cli`, which registers services back into `serve`. Keeping the contract package free of the transport stack is what keeps that acyclic.
- `go/transport/cmdsurface`: invocations, results, policy, surfaces.

## See also

- [transportsvc reference](../../../docs/adopters/reference/transportsvc.md): the `Transport` and `Invoker` contracts, the constructor, every option, the `TransportService` methods
- [build-a-transport-service.md](../../../docs/adopters/guides/build-a-transport-service.md): the task walkthrough
- [serve lifecycle contract](../../../docs/contracts/serve-lifecycle.md)
