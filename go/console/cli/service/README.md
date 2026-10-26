# service

## What it answers

How a kit CLI serves its command tree under `<tool> service start`
without every CLI that never serves linking the serve plane. This
package is that plane: the `service` command group and its supervisor,
the built-in `api` and `socket` services, and the gates every
kit-shipped transport service shares (authentication modes, TLS and
ACME, the HTTP plane, permission and permission rules, audit sinks,
quota, rate limit, the usage ledger, observability), plus the `token`,
audit and quota commands and the per-invocation root factory.

## Use it when

- serve the tree over REST → `service.WithAPI(service.APIConfig{})`, then `<tool> service start`
- serve it over an owner-only Unix socket → `service.WithSocket(service.SocketConfig{})`
- register a service of your own → `service.WithService(svc)`
- decide who may run what, on every surface → `service.WithPermission`, `service.WithPermissionRules`, `--policy`
- audit served calls → `service.WithAuditSinks`, `service.WithAuditCommand`
- run invocations in parallel on trees of their own → `service.WithRootFactory`
- reach the group, its `start` leaf or its registry → `service.ServiceCommand(root)`, `service.ServeCommand(root)`, `service.ServeRegistry(root)`

## Quick start

```go
root := cli.New(cli.Config{Name: "mytool", Version: version},
    cli.WithStatus(cli.StatusConfig{}),
    service.WithAPI(service.APIConfig{}),
    service.WithSocket(service.SocketConfig{}),
)
```

## Contract

- Normative text: [serve-lifecycle contract](../../../../docs/contracts/serve-lifecycle.md).
- Options keep the shape `func(*cli.Root)` and may come in any order;
  the plane attaches its per-Root state through cli's extension slot
  (`cli.Extend`) and takes part in `cli.New`, `Root.Prepare` and the
  `--policy` exemption through cli's hook interfaces.
- `go/console/cli` does not import this package; a test in
  `go/console/cli` pins that a `cli.Root` alone links none of it.
- `mcpserve`, `rpcserve` and `celpermission` import this package, never
  the reverse; a test here pins that serving links none of them.
- Moving from `cli.WithAPI` and the other names in `go/console/cli`:
  [migrate-to-cli-service.md](../../../../docs/adopters/guides/migrate-to-cli-service.md).
