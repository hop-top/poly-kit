package api

import (
	"context"
	"net/http"
)

type claimsKey struct{}

// AuthFunc validates a request and returns claims on success.
type AuthFunc func(r *http.Request) (claims any, err error)

// Auth returns a middleware that calls fn to authenticate each request.
// On success, claims are stored in the request context. On error, a
// 401 JSON response is written.
func Auth(fn AuthFunc) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, err := fn(r)
			if err != nil {
				Error(w, http.StatusUnauthorized, &APIError{
					Status:  http.StatusUnauthorized,
					Code:    "unauthorized",
					Message: err.Error(),
				})
				return
			}
			ctx := context.WithValue(r.Context(), claimsKey{}, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ClaimsFromContext extracts auth claims stored by the Auth middleware.
func ClaimsFromContext(ctx context.Context) any {
	return ctx.Value(claimsKey{})
}
