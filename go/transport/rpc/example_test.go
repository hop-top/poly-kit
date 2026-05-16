package rpc_test

import (
	"fmt"
	"time"

	"connectrpc.com/connect"

	"hop.top/kit/go/transport/rpc"
)

func ExampleNewServer() {
	srv := rpc.NewServer(
		rpc.WithReadTimeout(10*time.Second),
		rpc.WithWriteTimeout(30*time.Second),
		rpc.WithShutdownTimeout(60*time.Second),
		rpc.WithInterceptors(
			rpc.RequestIDInterceptor(),
			rpc.LogInterceptor(func(msg string, args ...any) {
				fmt.Println(msg)
			}),
			rpc.RecoveryInterceptor(func(v any) {
				fmt.Println("panic recovered:", v)
			}),
		),
	)

	// Server is ready for handler registration via srv.Handle.
	_ = srv
}

func ExampleRPCResource() {
	srv := rpc.NewServer(
		rpc.WithInterceptors(rpc.RequestIDInterceptor()),
	)

	// Mount a service using RPCResource (requires a concrete
	// api.Service[T] implementation; shown here as a pattern):
	//
	//   path, handler := rpc.RPCResource[MyEntity](
	//       myService,
	//       connect.WithInterceptors(srv.Interceptors()...),
	//   )
	//   srv.Handle(path, handler)
	//
	// Then serve:
	//   rpc.ListenAndServe(ctx, ":8080", srv)

	_ = srv
	_ = connect.WithInterceptors // reference to show the import
}
