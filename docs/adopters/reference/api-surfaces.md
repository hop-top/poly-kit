# api surfaces reference

Reference for `service.Surface` and the unix-socket listen address of
the api service, in
[`go/console/cli/service`](../../../go/console/cli/service/surface.go)
and [`go/transport/api`](../../../go/transport/api/listen.go). To add
a surface step by step, see
[add-a-protocol-surface](../guides/add-a-protocol-surface.md).

A surface is a set of routes beside the REST projection: built from
the same command tree, run through the same executor, served on the
same listener behind the same guard chain. It is the extension point
for a wire contract REST does not express. The dek surface (capability
manifest, plans, jobs) is one: it lives in the dek repository's Go
module `hop.top/dek/go`, not in kit.

## Surface

```go
type Surface interface {
    Name() string
    Mount(router *api.Router, proj api.ProjectionConfig) error
    PublicRoutes() []PublicRoute
}

type PublicRoute struct {
    Method string
    Path   string
}

// APIConfig field
Surfaces []Surface
```

| Member | Contract |
|--------|----------|
| `Name` | Not empty; unique in `Surfaces`; named in start errors. |
| `Mount` | Registers routes on `router`. `proj` carries `Descriptors` (every reflected command), `Executor` (runs one with the permission gate, audit and idempotency of the other surfaces), `ToolName`, `ToolVersion`. Runs once per start, after the REST projection and the adopter's routes. An error, or a panic from a duplicate pattern, fails the start. |
| `PublicRoutes` | Exact routes that skip authentication. Nil: every route guarded. |

A handler reads its caller with `api.ClaimsFromContext(r.Context())`
and `api.IdentityOf(claims)`. A tool with no command root serves no
surfaces.

## Authentication

A surface route meets every block a REST route meets: authentication,
host and origin check, body limit, compression, security headers.
The one exemption is `PublicRoute`, applied per declared route:

- match is the exact method and the exact path; a different method, a
  longer path, a prefix or a pattern stays guarded;
- a declaration with an empty or lower-case method, a relative path,
  `/`, a wildcard (`{`, `}`), a query, a fragment or a trailing slash
  fails validation;
- a route the service already serves (a REST projection route, an
  adopter route) cannot be declared: the start fails;
- a declared route the surface did not mount fails the start.

Health probes answer ahead of the router and are never a surface
route.

## Listen address

`APIConfig.Addr`, `services.api.addr` and `--addr` take either
`host:port` or `unix:/abs/path.sock` (`unix:///abs/path.sock` also).
The rules, for any service that adopts
[`api.Listen`](../../../go/transport/api/listen.go):

- socket file mode `0600`; removed on stop; parent directory created
  `0700` when missing;
- a stale socket file (nothing answers on it) is replaced; a live one
  is an error; a path that is not a socket is an error and is left
  alone;
- a unix address counts as loopback: no `Auth` and no
  `insecure_remote` needed. The file mode is the access control;
- the Host check answers to `localhost`, `127.0.0.1` and `::1`, so a
  client sends `Host: localhost`;
- the path must be absolute and short enough for the OS (about 100
  bytes).

The api service uses it today. The mcp and rpc services still bind
TCP only.

## Command annotations a surface can read

- `kit/long-running` (`cmdmeta.KeyLongRunning`, `"true"`): the command
  may outlast a request. `api.CommandDescriptor.LongRunning` carries
  it; a surface with a job model may run the command as a job. The
  REST projection ignores it.
- The reflected `args_schema` of a command leaves out kit's global
  flags (output, logging, dry-run plumbing).

## Related

- [`transport-api`](transport-api.md) · [`cmdsurface`](cmdsurface.md) ·
  [`socket-wire`](socket-wire.md)
