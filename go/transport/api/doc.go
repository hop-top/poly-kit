// Package api provides an HTTP toolkit for building JSON APIs
// with OpenAPI spec generation and WebSocket pub/sub.
//
// It includes a Router built on Go 1.22+ [net/http.ServeMux], a
// composable Middleware chain, built-in middleware (logging, panic
// recovery, CORS, request ID, content type, auth), request/response
// helpers (Bind, JSON, Error), structured error mapping, and a
// generic ResourceRouter that auto-wires CRUD endpoints for any
// entity type implementing the Entity interface.
//
// # Router
//
// The Router wraps [net/http.ServeMux] and adds middleware support,
// path groups, and handler mounting:
//
//	logger := kitlog.New(viper.GetViper())
//	r := api.NewRouter(api.WithMiddleware(api.Logger(logger.Info)))
//	r.Handle("GET", "/items/{id}", getItem)
//	g := r.Group("/api/v1")
//	g.Handle("POST", "/items", createItem)
//
// [Logger] accepts a [LoggerFunc] matching kit/log
// (charm.land/log/v2)'s bound Info/Warn/Error/Debug method values:
// func(msg any, keyvals ...any). Adopters configure kit/log via viper
// (quiet, no-color, verbose count); the middleware inherits whatever
// level/style the caller's logger has. stdlib slog's `slog.Info` is
// NOT directly assignable — wrap it or migrate to kit/log.
//
// # OpenAPI
//
// Pass [WithOpenAPI] to enable OpenAPI 3.1 spec generation via huma.
// The spec is served at /openapi.json. Use [WithHumaAPI] on a
// [ResourceRouter] to register typed operations into the spec.
//
// Note: huma is an unconditional dependency of this package. All
// importers of api/ pull in huma even when OpenAPI is unused. This
// is acceptable for a framework package — huma is lightweight and
// the coupling avoids a subpackage split.
//
//	r := api.NewRouter(api.WithOpenAPI(api.OpenAPIConfig{
//	    Title: "My API", Version: "1.0.0",
//	}))
//	r.Mount("/widgets", api.ResourceRouter[Widget](svc,
//	    api.WithHumaAPI[Widget](api.HumaAPI(r)),
//	))
//
// # ResourceRouter
//
// ResourceRouter generates a full set of CRUD routes from a Service:
//
//	h := api.ResourceRouter[Widget](widgetSvc,
//	    api.WithPrefix[Widget]("/widgets"),
//	)
//	r.Mount("/api", h)
//
// # WebSocket
//
// [Hub] manages topic-based pub/sub over WebSocket connections.
// [WSHandler] upgrades HTTP requests and registers clients. Use
// [BusAdapter] to bridge an event bus to WebSocket clients:
//
//	hub := api.NewHub()
//	go hub.Run(ctx)
//	r.Handle("GET", "/ws", api.WSHandler(hub))
//
// Clients subscribe via JSON messages with type "subscribe" and a
// dot-delimited topic. Glob patterns (* and **) are supported.
//
// # Capabilities
//
// WithCapabilities enables a GET /capabilities endpoint that returns
// a JSON CapabilitySet describing all registered routes and resources:
//
//	r := api.NewRouter(api.WithCapabilities("myapp", "1.0.0"))
//	r.Handle("GET", "/items", listItems)
//	r.MountResource("/widgets", widgetRouter, "create", "list", "get")
//	// GET /capabilities → { "service": "myapp", "capabilities": [...] }
//
// Resource capabilities are auto-registered via MountResource.
//
// # Middleware
//
// Middleware follows the standard func(http.Handler) http.Handler
// pattern. Chain composes multiple middleware:
//
//	mw := api.Chain(api.RequestID(), api.Logger(log), api.Recovery(onPanic))
//
// See examples/multiprotocol for a complete server combining REST,
// OpenAPI, WebSocket, and ConnectRPC.
package api
