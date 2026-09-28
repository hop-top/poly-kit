package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"hop.top/kit/go/transport/api"
)

// observeRoute wraps h the way slot-5 middleware does: outside the
// router, behind a layer that copies the request, and returns the
// pattern it read back.
func observeRoute(t *testing.T, h http.Handler, method, target string) string {
	t.Helper()
	var got string
	outer := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, route := api.ObserveRoute(r)
		// A layer in between copies the request, as the Host check does.
		h.ServeHTTP(w, r.WithContext(r.Context()))
		got = route()
	})
	outer.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, target, nil))
	return got
}

func TestObserveRouteReportsTheMatchedPattern(t *testing.T) {
	router := api.NewRouter()
	router.Handle(http.MethodGet, "/v1/items/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	assert.Equal(t, "GET /v1/items/{id}", observeRoute(t, router, http.MethodGet, "/v1/items/7"))
	assert.Empty(t, observeRoute(t, router, http.MethodGet, "/nowhere"), "an unmatched path reports no route")
}

func TestObserveRouteOuterRouterWins(t *testing.T) {
	inner := api.NewRouter()
	inner.Handle(http.MethodGet, "/list", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	outer := api.NewRouter()
	outer.Mount("/items", inner)

	assert.Equal(t, "/items/", observeRoute(t, outer, http.MethodGet, "/items/list"))
}

func TestRouterWithoutObserverIsUnaffected(t *testing.T) {
	router := api.NewRouter()
	router.Handle(http.MethodGet, "/ok", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ok", nil))
	assert.Equal(t, http.StatusNoContent, rec.Code)
}
