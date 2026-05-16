package api

import (
	"fmt"
	"net/http"
	"time"
)

// LoggerFunc is the function shape accepted by [Logger]. It matches the
// bound Info/Warn/Error/Debug method values of kit/log
// (charm.land/log/v2.Logger): the first argument is `any` so callers
// may pass formatted messages as well as plain strings, with key/value
// pairs trailing.
//
// stdlib slog's `slog.Info` shape (func(msg string, args ...any)) is
// NOT directly assignable to LoggerFunc. Adopters previously passing
// slog.Info should switch to a kit/log logger:
//
//	logger := kitlog.New(viper.GetViper())
//	api.Logger(logger.Info)
type LoggerFunc func(msg any, keyvals ...any)

// Logger returns a middleware that logs each request using the provided
// log function. It records method, path, status code, and duration.
//
// fn matches kit/log's bound Info method value
// (`func(msg any, keyvals ...any)`). To capture this middleware's
// output in tests, configure the kit/log logger with a custom
// [io.Writer] via charm/log's SetOutput.
func Logger(fn LoggerFunc) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)
			fn("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.status,
				"duration", fmt.Sprintf("%dms", time.Since(start).Milliseconds()),
			)
		})
	}
}

// statusWriter wraps http.ResponseWriter to capture the status code.
type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.wroteHeader = true
	}
	return w.ResponseWriter.Write(b)
}
