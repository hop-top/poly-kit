// Package client provides a typed ConnectRPC client for kit entity services.
//
// [Client] wraps the generated crudv1connect service client and handles
// automatic marshaling between Go entities and protobuf Struct messages.
// It mirrors the REST client API (Create, Get, List, Update, Delete) but
// communicates over ConnectRPC (HTTP/2 + protobuf):
//
//	c := client.New[Widget]("http://host",
//	    client.WithAuth(token),
//	)
//	w, _ := c.Create(ctx, widget)
//	items, _ := c.List(ctx, client.ListParams{Limit: 20})
//
// # Options
//
//   - [WithHTTPClient]: custom *http.Client (e.g. h2c transport)
//   - [WithAuth]: Bearer token sent via connect interceptor headers
//
// Entity ↔ protobuf Struct conversion is handled by kit/rpc helpers
// (EntityToStruct, StructToEntity), making the client generic over any
// type satisfying [api.Entity].
package client
