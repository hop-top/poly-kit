// Package client provides typed HTTP clients for kit/api services.
//
// # REST Client
//
// [Client] is a generic REST client for a single resource endpoint.
// It implements Create, Get, List, Update, Delete against a base URL:
//
//	c := client.New[Widget]("http://host/api/widgets",
//	    client.WithAuth(token),
//	)
//	w, _ := c.Create(ctx, widget)
//	items, _ := c.List(ctx, client.ListParams{Limit: 20})
//
// # WebSocket Client
//
// [WSClient] wraps a WebSocket connection for pub/sub. Use [DialWS]
// to connect, then Subscribe/Unsubscribe to topics and OnMessage for
// incoming events:
//
//	ws, _ := client.DialWS(ctx, "ws://host/ws")
//	ws.Subscribe(ctx, "widgets.*")
//	ws.OnMessage(func(msg api.WSMessage) { /* ... */ })
//	go ws.Listen(ctx)
//
// # Options
//
//   - [WithHTTPClient]: custom *http.Client (timeouts, transport)
//   - [WithAuth]: Bearer token for Authorization header
package client
