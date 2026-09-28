package api

import (
	"net/http"
	"strconv"
	"time"
)

// DefaultContentSecurityPolicy is the policy [SecurityHeaders] sets
// on a response whose handler sets none. The api serves JSON, which
// needs no capability a document could use, so it grants nothing and
// forbids framing.
const DefaultContentSecurityPolicy = "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"

// DefaultHSTSMaxAge is the Strict-Transport-Security max-age
// [SecurityHeaders] sends over TLS when HSTSMaxAge is zero.
const DefaultHSTSMaxAge = 365 * 24 * time.Hour

// SecurityHeadersConfig configures [SecurityHeaders].
type SecurityHeadersConfig struct {
	// ContentSecurityPolicy replaces [DefaultContentSecurityPolicy]
	// when non-empty.
	ContentSecurityPolicy string
	// HSTSMaxAge is the Strict-Transport-Security max-age; zero means
	// [DefaultHSTSMaxAge], negative sends no HSTS header.
	HSTSMaxAge time.Duration
}

// SecurityHeaders returns a middleware that sets response headers
// hardening what a browser does with the server's responses:
//
//   - X-Content-Type-Options: nosniff — JSON is never sniffed into
//     HTML or script.
//   - Referrer-Policy: no-referrer — URLs, which may carry ids, do not
//     leak to other sites.
//   - Content-Security-Policy — [DefaultContentSecurityPolicy]. A
//     handler serving a document of its own replaces it with
//     Header().Set, as huma's /docs page does.
//   - Strict-Transport-Security — only when the client reached the
//     service over TLS ([IsHTTPS]): TLS on this server, or https
//     forwarded by a proxy [ClientAddress] trusts. A forwarded-proto
//     header from any other peer is ignored.
//
// Headers are set before the handler runs and the ResponseWriter is
// passed through untouched, so streaming (http.Flusher) and
// WebSocket upgrades (http.Hijacker) behave as without it. A header
// an outer layer already set is left alone.
func SecurityHeaders(cfg SecurityHeadersConfig) Middleware {
	csp := cfg.ContentSecurityPolicy
	if csp == "" {
		csp = DefaultContentSecurityPolicy
	}
	hsts := ""
	switch {
	case cfg.HSTSMaxAge == 0:
		hsts = "max-age=" + strconv.FormatInt(int64(DefaultHSTSMaxAge/time.Second), 10)
	case cfg.HSTSMaxAge > 0:
		hsts = "max-age=" + strconv.FormatInt(int64(cfg.HSTSMaxAge/time.Second), 10)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			setIfEmpty(h, "X-Content-Type-Options", "nosniff")
			setIfEmpty(h, "Referrer-Policy", "no-referrer")
			setIfEmpty(h, "Content-Security-Policy", csp)
			if hsts != "" && IsHTTPS(r) {
				setIfEmpty(h, "Strict-Transport-Security", hsts)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func setIfEmpty(h http.Header, key, value string) {
	if h.Get(key) == "" {
		h.Set(key, value)
	}
}
