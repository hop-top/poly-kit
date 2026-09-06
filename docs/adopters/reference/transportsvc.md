# transportsvc reference

API reference for
[`go/transport/transportsvc`](../../../go/transport/transportsvc/README.md),
the registration seam for transport services: front a kit command tree
over any transport and get the serve lifecycle with it. The
`Transport` and `Invoker` contracts, the constructor, the options, the
`TransportService` methods, and why the seam lives where it does. The
task walkthrough is
[build-a-transport-service.md](../guides/build-a-transport-service.md).

## What it centralizes

A transport service is a `serve.Service` whose work is projecting the
command tree onto one transport. MCP, RPC, SSE, a bus consumer, and
the built-in socket service are the same shape and differ only in how
requests arrive and leave. That difference is `Transport`; the rest is
here.

| Centralized | Consequence for a transport author |
|---|---|
| reflection of the command tree, once, at `Start` | you get the complete tree; do not cache leaves |
| the policy path | you never read an annotation or gate a command |
| surface pinning | you cannot invoke as another surface, and need not set one |
| readiness | reported after `Bind` returns nil |
| address | `Bind`'s return value reaches the supervisor |
| ordered idempotent stop | `Stop` calls `Close` once |

## Transport

```go
type Transport interface {
    Bind(ctx context.Context) (addr string, err error)
    Serve(ctx context.Context, inv Invoker) error
    Close(ctx context.Context) error
}
```

| Method | Contract |
|---|---|
| `Bind` | acquire everything that can fail deterministically; return the resolved address, or `""` when there is none |
| `Serve` | accept work until `ctx` is canceled or it fails; `nil` after cancellation is a clean stop; called only after `Bind` succeeds |
| `Close` | release what `Bind` acquired; **must make `Serve` return**; may be called more than once |

## Invoker

```go
type Invoker func(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error)
```

The one call a transport makes into the command tree, with the
surface already pinned and the policy gate already wired.

Errors are the bridge's, and a transport maps them onto its wire
format rather than deciding them:

| Error | Meaning |
|---|---|
| `cmdsurface.ErrUnknownCommand` | the path does not resolve |
| `cmdsurface.ErrSurfaceNotEnabled` | the leaf is not exposed on this surface |
| `cmdsurface.ErrDestructiveBlocked` | destructive leaf refused by policy |
| `cmdsurface.ErrPermissionDenied` | the permission gate refused this caller; the message carries its reason |

The Meta a transport passes reaches the bridge unchanged except for
`Surface`, which the seam pins. A transport fills `Caller` and
`Tenant` only from an identity it verified; without an authenticator
it records what the caller claimed, and the bridge grants nothing on
that basis.

## Constructor

```go
func NewTransportService(
    name string,
    root *cobra.Command,
    surface cmdsurface.Surface,
    tr Transport,
    opts ...TransportOption,
) *TransportService
```

`name` must match `^[a-z][a-z0-9-]*$` and must not be `all`, `none`,
or `list`. An invalid name, or a nil `Transport`, panics at
construction — both are wiring bugs in `main`.

## Options

| Option | Use this when |
|---|---|
| `Expose(pattern)` | the transport should reach commands the bridge does not enable by default; usually `Expose("*")` |
| `Hide(pattern)` | carving an exception out of a broader `Expose` |
| `WithBridgeOptions(...)` | you need a custom `cmdsurface.Policy` or `Runner` |
| `WithBridgeOptionsFunc(fn)` | the options are known only at Start — a permission gate built from a parsed flag, audit sinks registered after the service; applied after `WithBridgeOptions` |
| `WithValidate(fn)` | configuration can be detectably wrong before binding |
| `WithClass(sideEffect, network)` | the service should be gated by a policy table |
| `WithDependsOn(names...)` | another service must start first |

Patterns are `"widget add"` (exact), `"widget *"` (subtree), or `"*"`
(everything). Rules apply in order, so `Expose("*")` then
`Hide("widget delete")` reaches everything but that leaf.

## TransportService

Implements `serve.Service` plus every optional declaration the
supervisor consults: `Validator`, `Addressed`, `Classified`,
`Dependent`.

| Method | Returns |
|---|---|
| `Name()` | the registered identifier |
| `Start(ctx, ready)` | reflects, binds, reports ready, serves |
| `Ready()` | whether the transport is bound and serving |
| `Addr()` | the address `Bind` resolved, or `""` |
| `Stop(ctx)` | closes the transport; a second call is a no-op |
| `Validate()` | the `WithValidate` hook, or `nil` |
| `Class()` | the `WithClass` values, or two empty strings |
| `DependsOn()` | the `WithDependsOn` names |
| `Bridge()` | the bridge built at `Start`, or `nil` before it |

`Bridge()` is how a capability endpoint enumerates what the transport
exposes, including excluded commands and their reasons via
`Bridge.NonInvocable()`.

## Why it lives here

The lifecycle contract is in
[`go/console/serve`](../../../go/console/serve/), and this seam implements
it. It cannot live there: the command-tree half reaches `cmdsurface`,
which reaches `cmdreflect`, which reaches `go/console/cli`, which
registers services back into `serve`. Keeping the contract package
free of the transport stack is what keeps that acyclic.

## Related pages

- [build-a-transport-service.md](../guides/build-a-transport-service.md):
  the task walkthrough
- [socket wire reference](socket-wire.md): a complete transport on
  this seam
- [serve lifecycle contract](../../contracts/serve-lifecycle.md)
- [cmdsurface reference](cmdsurface.md): invocations, results, policy,
  surfaces
