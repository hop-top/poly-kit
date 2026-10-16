// Package service is kit's serve plane: the `service` command group and
// its supervisor, the built-in api and socket services, and the gates
// every kit-shipped transport service shares — authentication modes,
// TLS and ACME, the HTTP plane (CORS, body limit, timeouts,
// compression), permission and permission rules, audit sinks, quota,
// rate limit, the usage ledger and observability — plus the `token`,
// audit and quota commands and the per-invocation root factory.
//
// A CLI that serves imports it and passes its options to cli.New:
//
//	root := cli.New(cfg,
//		service.WithAPI(service.APIConfig{}),
//		service.WithSocket(service.SocketConfig{}),
//	)
//
// A CLI that does not links none of it: go/console/cli never imports
// this package. The plane attaches to a Root through cli's extension
// slot ([cli.Extend]) and receives the Root's lifecycle through cli's
// hook interfaces, so its options keep their shape and work in any
// order. The other kit services — go/console/cli/mcpserve and
// go/console/cli/rpcserve — register through [WithService] and share
// these gates.
package service
