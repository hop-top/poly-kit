package api

import (
	"net/http"
	"strconv"
	"strings"
)

// CORSConfig holds CORS policy settings.
type CORSConfig struct {
	AllowOrigins     []string
	AllowMethods     []string
	AllowHeaders     []string
	AllowCredentials bool
	MaxAge           int // seconds
}

// CORS returns a middleware that sets CORS headers and handles
// preflight OPTIONS requests.
func CORS(cfg CORSConfig) Middleware {
	origins := make(map[string]struct{}, len(cfg.AllowOrigins))
	for _, o := range cfg.AllowOrigins {
		origins[o] = struct{}{}
	}
	methods := strings.Join(cfg.AllowMethods, ", ")
	headers := strings.Join(cfg.AllowHeaders, ", ")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				_, wildcard := origins["*"]
				_, match := origins[origin]
				if wildcard || match {
					w.Header().Set("Access-Control-Allow-Origin", origin)
				}
			}

			if cfg.AllowCredentials {
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}

			// Preflight.
			if r.Method == http.MethodOptions {
				if methods != "" {
					w.Header().Set("Access-Control-Allow-Methods", methods)
				}
				if headers != "" {
					w.Header().Set("Access-Control-Allow-Headers", headers)
				}
				if cfg.MaxAge > 0 {
					w.Header().Set("Access-Control-Max-Age", strconv.Itoa(cfg.MaxAge))
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
