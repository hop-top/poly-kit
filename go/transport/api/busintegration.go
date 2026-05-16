package api

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// WithEventPublisher enables event publishing on the router. By default
// it publishes "kit.api.request.started" and "kit.api.request.ended"
// events for every request — both conform to the kit 4-segment past-
// tense topic convention (see bus.ValidateTopic).
//
// Topics are configurable via WithTopicPrefix (replace the 3-segment
// prefix) or WithTopics (per-field override). Calling
// WithEventPublisher with no opts preserves backward compatibility for
// adopters that don't care about topic strings.
//
// Note: prior to T-0122 the middleware emitted "api.request.start" and
// "api.request.end". Those non-conformant topics have been removed with
// no back-compat alias — adopters with subscribers MUST update topic
// strings.
func WithEventPublisher(p EventPublisher, opts ...Option) RouterOption {
	cfg := newConfig(opts...)
	return func(r *Router) {
		r.middleware = append(r.middleware, eventMiddleware(p, cfg))
	}
}

func eventMiddleware(p EventPublisher, cfg *config) Middleware {
	startTopic := string(cfg.topics.RequestStart)
	endTopic := string(cfg.topics.RequestEnd)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			_ = p.Publish(r.Context(), startTopic, "api.router",
				map[string]string{
					"method": r.Method,
					"path":   r.URL.Path,
				},
			)

			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)

			_ = p.Publish(context.Background(), endTopic, "api.router",
				map[string]any{
					"method":   r.Method,
					"path":     r.URL.Path,
					"status":   sw.status,
					"duration": fmt.Sprintf("%dms", time.Since(start).Milliseconds()),
				},
			)
		})
	}
}
