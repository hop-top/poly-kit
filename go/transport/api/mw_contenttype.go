package api

import "net/http"

// ContentType returns a middleware that sets the Content-Type header
// on the response if it has not already been set.
func ContentType(ct string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if w.Header().Get("Content-Type") == "" {
				w.Header().Set("Content-Type", ct)
			}
			next.ServeHTTP(w, r)
		})
	}
}
