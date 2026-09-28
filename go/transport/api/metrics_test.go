package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func scrapeBody(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("up 1\n")) }

func serveMetrics(h http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestMetricsRouteAnswersAheadOfNext(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h := MetricsRoute(next, MetricsConfig{Handler: http.HandlerFunc(scrapeBody)})

	rec := serveMetrics(h, http.MethodGet, DefaultMetricsPath)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "up 1\n", rec.Body.String())
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))

	assert.Equal(t, http.StatusTeapot, serveMetrics(h, http.MethodGet, "/metrics/x").Code, "exact path only")
	assert.Equal(t, http.StatusTeapot, serveMetrics(h, http.MethodGet, "/v1/commands").Code)

	rec = serveMetrics(h, http.MethodPost, DefaultMetricsPath)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Equal(t, "GET, HEAD", rec.Header().Get("Allow"))
}

func TestMetricsRouteCustomPath(t *testing.T) {
	next := http.NotFoundHandler()
	h := MetricsRoute(next, MetricsConfig{Path: "/_kit/metrics", Handler: http.HandlerFunc(scrapeBody)})
	assert.Equal(t, http.StatusOK, serveMetrics(h, http.MethodGet, "/_kit/metrics").Code)
	assert.Equal(t, http.StatusNotFound, serveMetrics(h, http.MethodGet, "/metrics").Code)
}

func TestMetricsRouteWithoutHandlerIsNext(t *testing.T) {
	next := http.NotFoundHandler()
	h := MetricsRoute(next, MetricsConfig{})
	assert.Equal(t, http.StatusNotFound, serveMetrics(h, http.MethodGet, DefaultMetricsPath).Code)
}

func TestMetricsRouteAdopterRouteWins(t *testing.T) {
	r := NewRouter()
	r.Handle(http.MethodGet, DefaultMetricsPath, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("adopter"))
	})
	wrapped := http.HandlerFunc(r.ServeHTTP) // hides the router, as a Host check does
	h := MetricsRoute(wrapped, MetricsConfig{Handler: http.HandlerFunc(scrapeBody), Routes: r})
	assert.Equal(t, "adopter", serveMetrics(h, http.MethodGet, DefaultMetricsPath).Body.String())

	// A subtree mount does not claim the path.
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("tree")) })
	h = MetricsRoute(mux, MetricsConfig{Handler: http.HandlerFunc(scrapeBody)})
	assert.Equal(t, "up 1\n", serveMetrics(h, http.MethodGet, DefaultMetricsPath).Body.String())
}
