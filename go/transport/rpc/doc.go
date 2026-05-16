// Package rpc provides a ConnectRPC server scaffold with interceptors
// mirroring the api/ HTTP middleware patterns. It offers the same
// option-func configuration, graceful lifecycle, and shared auth
// types so both HTTP and RPC servers stay consistent.
//
// # Server
//
// [Server] wraps an http.ServeMux for ConnectRPC handler
// registration. Use [ListenAndServe] for a context-aware lifecycle
// with graceful shutdown:
//
//	srv := rpc.NewServer(
//	    rpc.WithInterceptors(rpc.RequestIDInterceptor()),
//	)
//	path, handler := rpc.RPCResource[Widget](svc,
//	    connect.WithInterceptors(srv.Interceptors()...),
//	)
//	srv.Handle(path, handler)
//	rpc.ListenAndServe(ctx, ":8081", srv)
//
// # RPCResource
//
// [RPCResource] bridges any [api.Service] to a ConnectRPC
// EntityService handler. It uses JSON↔protobuf.Struct conversion
// so entity types need no proto codegen — the generic CRUD proto
// definition handles all entities.
//
// # Interceptors
//
// Interceptors mirror api/ middleware for cross-cutting concerns:
//
//   - [RequestIDInterceptor] — injects X-Request-ID
//   - [AuthInterceptor] — validates auth via shared api.AuthFunc
//   - [LogInterceptor] — logs procedure, duration, errors
//   - [RecoveryInterceptor] — catches panics → connect.CodeInternal
//
// See examples/multiprotocol for a complete server combining REST
// and ConnectRPC.
package rpc
