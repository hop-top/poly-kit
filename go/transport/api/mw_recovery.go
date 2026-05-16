package api

import "net/http"

// Recovery returns a middleware that recovers from panics.
// It calls onPanic with the recovered value and the request,
// then writes a 500 Internal Server Error response.
func Recovery(onPanic func(recovered any, r *http.Request)) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					if onPanic != nil {
						onPanic(v, r)
					}
					http.Error(w, http.StatusText(http.StatusInternalServerError),
						http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
