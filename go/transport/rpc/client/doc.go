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
// # Errors
//
// Every Connect error comes back as an *api.APIError carrying the
// Connect code and its HTTP status. A rate-limit refusal —
// ResourceExhausted with Retry-After — is a [*RateLimitedError]: 429
// rate_limited, errors.Is api.ErrRateLimited, and the server's wait.
//
// Entity ↔ protobuf Struct conversion is handled by kit/rpc helpers
// (EntityToStruct, StructToEntity), making the client generic over any
// type satisfying [api.Entity].
package client
