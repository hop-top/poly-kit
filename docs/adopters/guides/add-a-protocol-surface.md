# Add a protocol surface to the api service

Serve a second wire contract from the same command tree: your own
routes on the api service's listener, run through the same executor as
REST.

## Who this is for

Developers whose clients speak a contract REST does not express
(a capability manifest, plans, jobs) and who want it served with the
auth, host checks and permission gate the api service already has. If
plain REST is enough, see [expose-cli-over-rest.md](expose-cli-over-rest.md).

## Steps

1. Implement `service.Surface`: a `Name`, a `Mount` that registers
   routes, and `PublicRoutes`.
2. In `Mount`, read what the REST projection was built from off the
   `api.ProjectionConfig` argument: `Descriptors` for the manifest,
   `Executor` to run a command, `ToolName` and `ToolVersion` for a
   hello.
3. Register it: `service.WithAPI(service.APIConfig{Surfaces: []service.Surface{mySurface}})`.
4. Declare in `PublicRoutes` only the routes a client must reach
   before it holds a credential, by exact method and path. Return nil
   when there are none.

```go
type hello struct{}

func (hello) Name() string { return "hello" }

func (hello) PublicRoutes() []service.PublicRoute {
    return []service.PublicRoute{{Method: "GET", Path: "/v1/hello"}}
}

func (hello) Mount(r *api.Router, p api.ProjectionConfig) error {
    r.Handle("GET", "/v1/hello", func(w http.ResponseWriter, _ *http.Request) {
        api.JSON(w, http.StatusOK, map[string]string{"tool": p.ToolName, "version": p.ToolVersion})
    })
    return nil
}
```

A working surface with a guarded route that runs a command is in
[`serve_api_surface_test.go`](../../../go/console/cli/service/serve_api_surface_test.go).

## What the service guarantees

- Every route of a surface is guarded like a REST route, except the
  declared public ones.
- The exemption is exact: method and path. It cannot cover a REST
  route or an adopter route, and the start fails if it tries.
- The surface mounts after the projection and the adopter's routes;
  those win a path collision.

## Reference

[api-surfaces](../reference/api-surfaces.md). The dek surface is
published in the dek repository's Go module `hop.top/dek/go`.
