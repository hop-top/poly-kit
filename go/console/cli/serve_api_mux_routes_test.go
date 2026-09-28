package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// muxRoutesFixture is an api service with Auth, a full OpenAPI config
// and a huma operation: every kind of route huma, the projection and
// the adopter register, on and off the router's per-route chain.
func muxRoutesFixture(t *testing.T, keys map[string]any) http.Handler {
	t.Helper()
	r := configFixture(t, APIConfig{
		Auth:    bearer(nil),
		OpenAPI: &api.OpenAPIConfig{Title: "fix", Version: "1.0.0"},
		Resources: func(_ *api.Router, h any) {
			type out struct {
				Body struct {
					Items string `json:"items"`
				}
			}
			huma.Get(h.(huma.API), "/things", func(context.Context, *struct{}) (*out, error) {
				o := &out{}
				o.Body.Items = strings.Repeat("x", 4096)
				return o, nil
			})
		},
	})
	for k, v := range keys {
		r.Viper.Set(k, v)
	}
	return projectionHandler(t, r)
}

func muxServe(h http.Handler, method, target string, hdr map[string]string, body string) *httptest.ResponseRecorder {
	req := loopbackRequest(method, target, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Auth gates every route the router can reach, not only the ones
// registered through Router.Handle: huma registers its spec, docs,
// schemas and operations straight on the mux, and an unmatched path
// matches nothing at all.
func TestAPIAuthCoversMuxDirectRoutes(t *testing.T) {
	h := muxRoutesFixture(t, nil)
	alice := map[string]string{"Authorization": "Bearer alice"}

	for _, p := range []string{
		"/openapi.json", "/openapi.yaml", "/docs", "/schemas/Out.json",
		"/things", "/capabilities", "/no-such-route", "/v1/commands",
	} {
		rec := muxServe(h, http.MethodGet, p, nil, "")
		assert.Equal(t, http.StatusUnauthorized, rec.Code, "GET %s without credentials", p)
	}

	// Credentials open them again; an unknown path is then a 404.
	for p, want := range map[string]int{
		"/openapi.json":  http.StatusOK,
		"/docs":          http.StatusOK,
		"/things":        http.StatusOK,
		"/v1/commands":   http.StatusOK,
		"/no-such-route": http.StatusNotFound,
	} {
		rec := muxServe(h, http.MethodGet, p, alice, "")
		assert.Equal(t, want, rec.Code, "GET %s with credentials", p)
	}

	// Health probes stay outside authentication (slot 7).
	for _, p := range []string{api.LivenessPath, api.ReadinessPath} {
		rec := muxServe(h, http.MethodGet, p, nil, "")
		assert.NotEqual(t, http.StatusUnauthorized, rec.Code, "GET %s is a probe", p)
	}
}

// The body limit (slot 10) runs ahead of authentication and covers
// mux-direct routes and unmatched paths too.
func TestAPIBodyLimitCoversMuxDirectRoutes(t *testing.T) {
	h := muxRoutesFixture(t, map[string]any{"services.api.body_limit.max_bytes": 16})
	big := strings.Repeat("x", 64)
	for _, p := range []string{"/things", "/no-such-route", "/openapi.json"} {
		rec := muxServe(h, http.MethodPost, p, nil, big)
		assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, "POST %s over the cap", p)
	}
}

// Compression (slot 11) covers huma's routes as it covers the
// router's own.
func TestAPICompressionCoversMuxDirectRoutes(t *testing.T) {
	h := muxRoutesFixture(t, map[string]any{
		"services.api.compression.enabled":   true,
		"services.api.compression.min_bytes": 0,
	})
	for _, p := range []string{"/things", "/openapi.json"} {
		rec := muxServe(h, http.MethodGet, p, map[string]string{
			"Authorization":   "Bearer alice",
			"Accept-Encoding": "gzip",
		}, "")
		require.Equal(t, http.StatusOK, rec.Code, p)
		assert.Equal(t, "gzip", rec.Header().Get("Content-Encoding"), "GET %s", p)
	}
}

// routeRecorder is a provider whose HTTP middleware reads back the
// route the router matched, the way the tracing slot names its span.
type routeRecorder struct {
	fakeObservability
	routes []string
}

func (c *routeRecorder) HTTPMiddleware(string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r, route := api.ObserveRoute(r)
			next.ServeHTTP(w, r)
			c.routes = append(c.routes, route())
		})
	}
}

// Moving authentication out of the route must not cost a refused call
// its route name: slot 5 still names it by the route it addressed.
func TestAPIAuthRefusalKeepsItsRouteName(t *testing.T) {
	c := &routeRecorder{}
	r := configFixture(t, APIConfig{Auth: bearer(nil)})
	WithObservability(c)(r)
	h := projectionHandler(t, r)

	rec := muxServe(h, http.MethodGet, "/v1/commands/list", nil, "")
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	rec = muxServe(h, http.MethodGet, "/v1/commands/list", map[string]string{"Authorization": "Bearer alice"}, "")
	require.Equal(t, http.StatusOK, rec.Code)
	rec = muxServe(h, http.MethodGet, "/no-such-route", nil, "")
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	assert.Equal(t, []string{"GET /v1/commands/list", "GET /v1/commands/list", ""}, c.routes)
}

// A refusal on a route outside the projection is audited like one on
// a projected route: no command path, the URL in http_path.
func TestAPIAuthRefusalOnMuxDirectRouteIsAudited(t *testing.T) {
	rec := &auditRecorder{}
	r := configFixture(t, APIConfig{
		Auth:    bearer(nil),
		OpenAPI: &api.OpenAPIConfig{Title: "fix", Version: "1.0.0"},
	})
	WithAuditSinks(rec.spec())(r)
	h := projectionHandler(t, r)

	resp := muxServe(h, http.MethodGet, "/openapi.json", map[string]string{"X-Request-ID": "req-doc"}, "")
	require.Equal(t, http.StatusUnauthorized, resp.Code)
	inv, _, err := rec.last(t)
	assert.ErrorIs(t, err, cmdsurface.ErrAuthRefused)
	assert.Nil(t, inv.Path)
	assert.Equal(t, "/openapi.json", inv.Meta.Extra["http_path"])
	assert.Equal(t, "req-doc", inv.Meta.RequestID)
}
