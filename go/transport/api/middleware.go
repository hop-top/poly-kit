package api

import "net/http"

// Middleware wraps an http.Handler, returning a new http.Handler.
type Middleware func(http.Handler) http.Handler

// Chain composes multiple middlewares into a single Middleware.
// Middlewares execute in the order provided: Chain(a, b, c) runs
// a → b → c → handler.
func Chain(mws ...Middleware) Middleware {
	return func(next http.Handler) http.Handler {
		for i := len(mws) - 1; i >= 0; i-- {
			next = mws[i](next)
		}
		return next
	}
}

// WithMiddleware adds middleware to a Router via RouterOption.
func WithMiddleware(mws ...Middleware) RouterOption {
	return func(r *Router) {
		r.middleware = append(r.middleware, mws...)
	}
}
