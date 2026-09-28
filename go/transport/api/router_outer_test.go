package api_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

// tokenAuth admits a request carrying "Authorization: Bearer ok".
func tokenAuth() api.Middleware {
	return api.Auth(func(r *http.Request) (any, error) {
		if r.Header.Get("Authorization") != "Bearer ok" {
			return nil, errors.New("missing bearer token")
		}
		return api.Claims{Subject: "ok"}, nil
	})
}

// outerRouter registers a route of every kind: through Handle, through
// huma (spec, docs, schemas and an operation), /capabilities, and none
// at all for an unmatched path.
func outerRouter(t *testing.T) *api.Router {
	t.Helper()
	r := api.NewRouter(
		api.WithOpenAPI(api.OpenAPIConfig{Title: "t", Version: "1"}),
		api.WithCapabilities("t", "1"),
		api.WithOuterMiddleware(tokenAuth()),
	)
	r.Handle(http.MethodGet, "/handled", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	type out struct {
		Body struct {
			OK bool `json:"ok"`
		}
	}
	huma.Get(api.HumaAPI(r), "/op", func(context.Context, *struct{}) (*out, error) {
		o := &out{}
		o.Body.OK = true
		return o, nil
	})
	return r
}

func TestWithOuterMiddlewareCoversEveryRoute(t *testing.T) {
	r := outerRouter(t)
	for _, p := range []string{
		"/handled", "/op", "/openapi.json", "/docs", "/capabilities", "/nowhere",
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		assert.Equal(t, http.StatusUnauthorized, rec.Code, "GET %s without credentials", p)
	}
	for p, want := range map[string]int{
		"/handled":      http.StatusOK,
		"/op":           http.StatusOK,
		"/openapi.json": http.StatusOK,
		"/capabilities": http.StatusOK,
		"/nowhere":      http.StatusNotFound,
	} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		req.Header.Set("Authorization", "Bearer ok")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		assert.Equal(t, want, rec.Code, "GET %s with credentials", p)
	}
}

func TestWithOuterMiddlewareRunsBeforePerRouteMiddleware(t *testing.T) {
	var order []string
	mark := func(name string) api.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	r := api.NewRouter(
		api.WithMiddleware(mark("route")),
		api.WithOuterMiddleware(mark("outer-1"), mark("outer-2")),
	)
	r.Handle(http.MethodGet, "/x", func(w http.ResponseWriter, _ *http.Request) {
		order = append(order, "handler")
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	assert.Equal(t, []string{"outer-1", "outer-2", "route", "handler"}, order)

	order = nil
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/nowhere", nil))
	assert.Equal(t, []string{"outer-1", "outer-2"}, order,
		"an unmatched path passes the outer chain and no per-route one")
}

// A refusal by outer middleware ends the request before the mux
// dispatches it, yet the route it addressed is still reported to an
// observer, so a refused call is measured by route like any other.
func TestWithOuterMiddlewareRefusalReportsRoute(t *testing.T) {
	r := outerRouter(t)
	assert.Equal(t, "GET /handled", observeRoute(t, r, http.MethodGet, "/handled"))
	assert.Equal(t, "GET /op", observeRoute(t, r, http.MethodGet, "/op"))
	assert.Empty(t, observeRoute(t, r, http.MethodGet, "/nowhere"))
}

func TestRouterWithoutOuterMiddlewareIsUnchanged(t *testing.T) {
	r := api.NewRouter(api.WithOpenAPI(api.OpenAPIConfig{Title: "t", Version: "1"}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}
