# Migrate to go/console/cli/service

You're here because your kit tool serves — it passes `WithAPI`,
`WithSocket`, `WithService…`, `mcpserve.With` or `rpcserve.With` to
`cli.New` — and after upgrading kit the compiler reports
`undefined: cli.WithAPI` or `root.ServeCommand undefined`. The serve
plane moved out of `go/console/cli` into its own package. This page is
the whole migration: one rewrite per moved name, plus an import.

The same release moved the SQLite bus adapter, the SQLite idempotency
store and peer discovery into opt-in packages. If the compiler reports
`undefined: bus.NewSQLiteAdapter`, `undefined: idemstore.OpenSQLite`,
`undefined: cli.WithPeers` or `root.Mesh undefined`, see
[The SQLite bus adapter](#the-sqlite-bus-adapter),
[The SQLite idempotency store](#the-sqlite-idempotency-store) and
[Peer discovery](#peer-discovery).

## What moved, and why

A CLI that never serves no longer links the HTTP API, the socket,
authentication, TLS, audit, quota, rate limit or the transport bridge,
and one that uses neither the SQLite stores nor peers links no SQLite
and no mDNS. On darwin/arm64, a CLI built on `cli.Root` alone shrank
from 31.3 MB to 18.6 MB (21.0 MB to 12.6 MB stripped) and from 74
linked modules to 53: the serve plane accounts for 5.3 MB and 10
modules of that, the SQLite stores and peers for 7.4 MB and 11
modules. A CLI that serves links what it linked before, less mDNS if
it does not use peers.

The serve options, types and constants moved to
`hop.top/kit/go/console/cli/service` with the same names, and the three
`Root` methods that reached the serve plane became functions of that
package:

| Before | After |
|--------|-------|
| `cli.WithAPI(cli.APIConfig{…})` | `service.WithAPI(service.APIConfig{…})` |
| `cli.WithSocket`, `cli.WithService`, every other serve option, type and constant | `service.<same name>` |
| `root.ServeCommand()` | `service.ServeCommand(root)` |
| `root.ServeRegistry()` | `service.ServeRegistry(root)` |
| `root.ServiceCommand()` | `service.ServiceCommand(root)` |

`cli` keeps no forwarders: a forwarder would make `cli` import the
package it is meant to stop linking.

Nothing else changes. `cli.New`, `cli.Root`, `Root.Prepare`,
`cli.SetCacheTTL`, `cli.SetWithholdFrom` and
`Root.RegisterServiceStatusProvider` keep their names and signatures,
and the `service` commands, flags, configuration keys and wire
behaviour are identical.

## Rewrite your code

Run these from your module root; each line moves one name:

```sh
gofmt -w -r 'cli.ConfigureServeHTTP -> service.ConfigureServeHTTP' .
gofmt -w -r 'cli.IsLoopbackAddr -> service.IsLoopbackAddr' .
gofmt -w -r 'cli.ResolveServeHTTPListener -> service.ResolveServeHTTPListener' .
gofmt -w -r 'cli.ResolveServeTLS -> service.ResolveServeTLS' .
gofmt -w -r 'cli.ResolveServeVerifier -> service.ResolveServeVerifier' .
gofmt -w -r 'cli.ServeBridgeOptions -> service.ServeBridgeOptions' .
gofmt -w -r 'cli.ServeBridgeOptionsExposed -> service.ServeBridgeOptionsExposed' .
gofmt -w -r 'cli.ServeBridgeOptionsFor -> service.ServeBridgeOptionsFor' .
gofmt -w -r 'cli.ServeHTTPHandler -> service.ServeHTTPHandler' .
gofmt -w -r 'cli.ServePolicyConfigured -> service.ServePolicyConfigured' .
gofmt -w -r 'cli.ValidateServeBridge -> service.ValidateServeBridge' .
gofmt -w -r 'cli.ValidateServeHTTPListener -> service.ValidateServeHTTPListener' .
gofmt -w -r 'cli.WithAPI -> service.WithAPI' .
gofmt -w -r 'cli.WithAPIKeys -> service.WithAPIKeys' .
gofmt -w -r 'cli.WithAuditCommand -> service.WithAuditCommand' .
gofmt -w -r 'cli.WithAuditSinks -> service.WithAuditSinks' .
gofmt -w -r 'cli.WithObservability -> service.WithObservability' .
gofmt -w -r 'cli.WithOwnServiceConfig -> service.WithOwnServiceConfig' .
gofmt -w -r 'cli.WithPermission -> service.WithPermission' .
gofmt -w -r 'cli.WithPermissionRules -> service.WithPermissionRules' .
gofmt -w -r 'cli.WithQuotaCommand -> service.WithQuotaCommand' .
gofmt -w -r 'cli.WithRootFactory -> service.WithRootFactory' .
gofmt -w -r 'cli.WithServeIdempotencyStore -> service.WithServeIdempotencyStore' .
gofmt -w -r 'cli.WithServeRateLimit -> service.WithServeRateLimit' .
gofmt -w -r 'cli.WithService -> service.WithService' .
gofmt -w -r 'cli.WithServiceBus -> service.WithServiceBus' .
gofmt -w -r 'cli.WithServiceControlScope -> service.WithServiceControlScope' .
gofmt -w -r 'cli.WithServiceOverride -> service.WithServiceOverride' .
gofmt -w -r 'cli.WithServicePolicy -> service.WithServicePolicy' .
gofmt -w -r 'cli.WithServices -> service.WithServices' .
gofmt -w -r 'cli.WithSocket -> service.WithSocket' .
gofmt -w -r 'cli.WithTokenCheck -> service.WithTokenCheck' .
gofmt -w -r 'cli.WithUsageStore -> service.WithUsageStore' .
gofmt -w -r 'cli.APIConfig -> service.APIConfig' .
gofmt -w -r 'cli.APIKeysConfig -> service.APIKeysConfig' .
gofmt -w -r 'cli.AuditVerifyRow -> service.AuditVerifyRow' .
gofmt -w -r 'cli.PermissionRuleCompiler -> service.PermissionRuleCompiler' .
gofmt -w -r 'cli.QuotaRow -> service.QuotaRow' .
gofmt -w -r 'cli.ServeCORS -> service.ServeCORS' .
gofmt -w -r 'cli.ServeExposure -> service.ServeExposure' .
gofmt -w -r 'cli.ServeHTTPListener -> service.ServeHTTPListener' .
gofmt -w -r 'cli.ServeHTTPRefusals -> service.ServeHTTPRefusals' .
gofmt -w -r 'cli.ServeHTTPSettings -> service.ServeHTTPSettings' .
gofmt -w -r 'cli.ServeMetricsEndpoint -> service.ServeMetricsEndpoint' .
gofmt -w -r 'cli.ServeObservability -> service.ServeObservability' .
gofmt -w -r 'cli.ServeTLS -> service.ServeTLS' .
gofmt -w -r 'cli.ServiceControlScope -> service.ServiceControlScope' .
gofmt -w -r 'cli.SocketConfig -> service.SocketConfig' .
gofmt -w -r 'cli.APIServiceName -> service.APIServiceName' .
gofmt -w -r 'cli.AuthModeAPIKey -> service.AuthModeAPIKey' .
gofmt -w -r 'cli.AuthModeJWKS -> service.AuthModeJWKS' .
gofmt -w -r 'cli.AuthModeJWT -> service.AuthModeJWT' .
gofmt -w -r 'cli.AuthModeMTLS -> service.AuthModeMTLS' .
gofmt -w -r 'cli.AuthModeOIDC -> service.AuthModeOIDC' .
gofmt -w -r 'cli.AuthModePeer -> service.AuthModePeer' .
gofmt -w -r 'cli.DefaultAPIAddr -> service.DefaultAPIAddr' .
gofmt -w -r 'cli.DefaultAPIKeyBackend -> service.DefaultAPIKeyBackend' .
gofmt -w -r 'cli.ReasonWithheldByConfig -> service.ReasonWithheldByConfig' .
gofmt -w -r 'cli.ServiceCommandName -> service.ServiceCommandName' .
gofmt -w -r 'cli.SocketServiceName -> service.SocketServiceName' .
gofmt -w -r 'x.ServeCommand() -> service.ServeCommand(x)' .
gofmt -w -r 'x.ServeRegistry() -> service.ServeRegistry(x)' .
gofmt -w -r 'x.ServiceCommand() -> service.ServiceCommand(x)' .
```

`gofmt -r` does not edit imports. Add

```go
import "hop.top/kit/go/console/cli/service"
```

with `goimports -w` or by hand, and remove the `cli` import from any
file that no longer uses it.

- If a file imports `cli` under another name, use that name on the
  left of each rule: `gofmt -w -r 'kitcli.WithAPI -> service.WithAPI' .`
- If a file declares its own package-level `service` (a type,
  variable, constant or function), import the package under an alias,
  `import kitservice "hop.top/kit/go/console/cli/service"`, and use
  `kitservice.` on the right of each rule. A local variable or
  parameter named `service` only shadows the import inside its scope.
- In the last three lines `x` is a wildcard for the receiver. If one of
  your own types has a method named `ServeCommand`, `ServeRegistry` or
  `ServiceCommand`, the compiler reports the call the rule rewrote;
  undo that one.

The compiler names anything the lines miss, such as a method value
(`root.ServeCommand` passed without a call).

## Projects created by `kit init`

A project generated from the `cli-go` template needs these edits in
`cmd/root.go`: `cli.WithAPI(cli.APIConfig{})` and
`cli.WithSocket(cli.SocketConfig{})` become `service.WithAPI(…)` and
`service.WithSocket(…)`, and the import block gains
`"hop.top/kit/go/console/cli/service"`. The rules above make both
edits; a project generated after the move already has them. The
SQLite and peer moves need no edit there: the template uses none of
their names.

## The SQLite bus adapter

The compiler reports `undefined: bus.NewSQLiteAdapter`. The SQLite
adapter moved out of `go/runtime/bus` into
`hop.top/kit/go/runtime/bus/sqlite`, so a program that only publishes
in process links no SQLite. Three of its four names lost the `SQLite`
prefix, which the package name now carries:

| Before | After |
|--------|-------|
| `bus.NewSQLiteAdapter` | `sqlite.NewAdapter` |
| `bus.SQLiteAdapter` | `sqlite.Adapter` |
| `bus.SQLiteOption` | `sqlite.Option` |
| `bus.WithPollInterval` | `sqlite.WithPollInterval` |

```sh
gofmt -w -r 'bus.NewSQLiteAdapter -> sqlite.NewAdapter' .
gofmt -w -r 'bus.SQLiteAdapter -> sqlite.Adapter' .
gofmt -w -r 'bus.SQLiteOption -> sqlite.Option' .
gofmt -w -r 'bus.WithPollInterval -> sqlite.WithPollInterval' .
```

Then add the import, `"hop.top/kit/go/runtime/bus/sqlite"`. The bus
itself, `bus.New` and `bus.WithAdapter`, stays where it was:

```go
// before
import "hop.top/kit/go/runtime/bus"

a, err := bus.NewSQLiteAdapter(path, bus.WithPollInterval(time.Second))
b := bus.New(bus.WithAdapter(a))

// after
import (
	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/runtime/bus/sqlite"
)

a, err := sqlite.NewAdapter(path, sqlite.WithPollInterval(time.Second))
b := bus.New(bus.WithAdapter(a))
```

A file that also imports another package named `sqlite`
(`hop.top/kit/go/storage/kv/sqlite`, for one) imports one of them
under an alias, `import bussqlite "hop.top/kit/go/runtime/bus/sqlite"`,
and uses `bussqlite.` on the right of the rules.

## The SQLite idempotency store

The compiler reports `undefined: idemstore.OpenSQLite`. The sqlite
`Store` moved out of `go/console/cli/idemstore` into
`hop.top/kit/go/console/cli/idemstore/sqlite` and is now `sqlite.Open`.
It still returns an `idemstore.Store`; `idemstore.Store`,
`idemstore.Result`, `idemstore.DefaultTTL` and `idemstore.Memory()`
stay where they were.

```sh
gofmt -w -r 'idemstore.OpenSQLite -> sqlite.Open' .
```

```go
// before
import "hop.top/kit/go/console/cli/idemstore"

store, err := idemstore.OpenSQLite(path, idemstore.DefaultTTL)

// after
import (
	"hop.top/kit/go/console/cli/idemstore"
	"hop.top/kit/go/console/cli/idemstore/sqlite"
)

store, err := sqlite.Open(path, idemstore.DefaultTTL)
```

A file that imports another package named `sqlite` aliases one of
them, as for the bus adapter. A tool that serves needs no edit: the
served transports' default replay store already opens through the new
package.

## Peer discovery

The compiler reports `undefined: cli.WithPeers` or
`root.Mesh undefined (type *cli.Root has no field or method Mesh)`.
Peer discovery moved out of `go/console/cli` into
`hop.top/kit/go/console/cli/peers`, so a CLI that does not discover
peers links neither mDNS nor the peer store's SQLite. The option and
its config are renamed, and the three `Root` fields became an
accessor:

| Before | After |
|--------|-------|
| `cli.WithPeers(cli.PeerConfig{…})` | `peers.With(peers.Config{…})` |
| `cli.PeerConfig` | `peers.Config` |
| `root.Mesh` | `peers.Of(root).Mesh` |
| `root.PeerRegistry` | `peers.Of(root).Registry` |
| `root.PeerTrust` | `peers.Of(root).Trust` |

The option and the config type are one rule each:

```sh
gofmt -w -r 'cli.WithPeers -> peers.With' .
gofmt -w -r 'cli.PeerConfig -> peers.Config' .
```

Then add the import, `"hop.top/kit/go/console/cli/peers"`.
`peers.With` still requires `cli.WithIdentity`, in either order, and
`peers.Config` keeps its fields (`Discovery`, `Service`, `DataDir`).

The field accesses have no rule: a `gofmt -r` pattern cannot tell a
`*cli.Root` from any other value with a `Mesh` field. Rewrite each one
the compiler names:

```go
// before
root := cli.New(cfg, cli.WithIdentity(cli.IdentityConfig{}), cli.WithPeers(cli.PeerConfig{}))
mesh, reg, trust := root.Mesh, root.PeerRegistry, root.PeerTrust

// after
root := cli.New(cfg, cli.WithIdentity(cli.IdentityConfig{}), peers.With(peers.Config{}))
p := peers.Of(root)
mesh, reg, trust := p.Mesh, p.Registry, p.Trust
```

`peers.Of` never returns nil, so a "peers configured" check stays on
the field. The tempting rewrite, `peers.Of(root) != nil`, is always
true:

```go
// before
if root.Mesh != nil && !noPeer {

// after: not peers.Of(root) != nil
if peers.Of(root).Mesh != nil && !noPeer {
```

The `peer` commands, their flags and output, the registry file and
its location are unchanged. If initialization fails (no
`cli.WithIdentity`, or the store does not open), the root's
`PersistentPreRunE` still returns the error after `--chdir` ran; its
text now names the new option: `peer: peers.With requires
cli.WithIdentity`.

A package named `peers` clashes with a local variable of that name in
the scope that uses it, such as `peers := root.Mesh.Peers()`; rename
the variable.

## Planes outside kit

The serve plane attaches to a `Root` through an extension slot that is
public API, so a package of your own can do the same: declare a key
with `cli.NewExtensionKey`, attach per-Root state from your options
with `cli.Extend`, read it with `cli.Extension`, and implement any of
`cli.OptionsHook`, `cli.InitHook`, `cli.PrepareHook` and
`cli.PolicyExemption` on that state to take part in `New`, `Prepare`
and the `--policy` exemption. See `go/console/cli/ext.go`.

A command that wants to know who it runs for, without linking the
bridge, reads `admitted.CallerFrom(cmd.Context())` from
`hop.top/kit/go/transport/cmdsurface/admitted`;
`cmdsurface.AdmittedMeta` still returns the full `Meta`.
