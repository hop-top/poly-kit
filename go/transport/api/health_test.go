package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

// healthUnder returns the health wrapper over a router whose every
// route is refused by auth, so a 200 from a health route proves it
// never reached the chain.
func healthUnder(cfg api.HealthConfig) (http.Handler, *atomic.Int32) {
	var reached atomic.Int32
	deny := func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			reached.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
		})
	}
	router := api.NewRouter(api.WithMiddleware(deny))
	router.Handle(http.MethodGet, "/v1/things", func(http.ResponseWriter, *http.Request) {})
	return api.HealthRoutes(router, cfg), &reached
}

func check(name string, ready *atomic.Bool) api.ReadinessCheck {
	return api.ReadinessCheck{Name: name, Ready: ready.Load}
}

func decodeHealth(t *testing.T, rec *httptest.ResponseRecorder) api.HealthStatus {
	t.Helper()
	var st api.HealthStatus
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &st), rec.Body.String())
	return st
}

func TestHealthRoutes_LivenessIsOKAndSkipsTheChain(t *testing.T) {
	var up atomic.Bool
	h, reached := healthUnder(api.HealthConfig{Checks: []api.ReadinessCheck{check("api", &up)}})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, api.LivenessPath, nil))

	assert.Equal(t, http.StatusOK, rec.Code, "liveness does not consult readiness")
	assert.Equal(t, api.HealthStatus{Status: "ok"}, decodeHealth(t, rec))
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	assert.Zero(t, reached.Load(), "health must answer before the router's middleware")
}

func TestHealthRoutes_ReadinessFollowsChecks(t *testing.T) {
	var api1, dep atomic.Bool
	h, reached := healthUnder(api.HealthConfig{
		Checks: []api.ReadinessCheck{check("api", &api1), check("db", &dep)},
		Detail: true,
	})
	get := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, api.ReadinessPath, nil))
		return rec
	}

	rec := get()
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, api.HealthStatus{Status: "unavailable", Failing: []string{"api", "db"}}, decodeHealth(t, rec))

	api1.Store(true)
	rec = get()
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, []string{"db"}, decodeHealth(t, rec).Failing)

	dep.Store(true)
	rec = get()
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, api.HealthStatus{Status: "ok"}, decodeHealth(t, rec))
	assert.Zero(t, reached.Load())
}

func TestHealthRoutes_NoDetailHidesCheckNames(t *testing.T) {
	var down atomic.Bool
	h, _ := healthUnder(api.HealthConfig{Checks: []api.ReadinessCheck{check("db", &down)}})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, api.ReadinessPath, nil))

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, api.HealthStatus{Status: "unavailable"}, decodeHealth(t, rec))
	assert.NotContains(t, rec.Body.String(), "db")
}

func TestHealthRoutes_PathPrefix(t *testing.T) {
	h, reached := healthUnder(api.HealthConfig{PathPrefix: "/_kit"})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_kit"+api.LivenessPath, nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Zero(t, reached.Load())

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, api.LivenessPath, nil))
	assert.Equal(t, http.StatusNotFound, rec.Code,
		"an unprefixed path falls through to the router, which serves nothing there")
}

func TestHealthRoutes_HeadAndMethodRefusal(t *testing.T) {
	h, reached := healthUnder(api.HealthConfig{})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, api.ReadinessPath, nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Body.String(), "HEAD carries no body")

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, api.LivenessPath, nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Equal(t, "GET, HEAD", rec.Header().Get("Allow"))
	assert.Zero(t, reached.Load())
}

func TestHealthRoutes_OtherPathsReachTheRouter(t *testing.T) {
	h, reached := healthUnder(api.HealthConfig{})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/things", nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, int32(1), reached.Load())
}

func TestHealthRoutes_GiveWayToAnAdopterRouteAtTheSamePath(t *testing.T) {
	router := api.NewRouter()
	router.Handle(http.MethodGet, "/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	h := api.HealthRoutes(router, api.HealthConfig{})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, api.LivenessPath, nil))
	assert.Equal(t, http.StatusTeapot, rec.Code, "the adopter's own /healthz wins")

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, api.ReadinessPath, nil))
	assert.Equal(t, http.StatusOK, rec.Code, "the route the adopter did not claim is still served")
}

func TestHealthRoutes_ASubtreeMountDoesNotClaimTheProbes(t *testing.T) {
	router := api.NewRouter()
	router.Mount("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	h := api.HealthRoutes(router, api.HealthConfig{})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, api.LivenessPath, nil))
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestHealthRoutes_GiveWayOnAPlainServeMux(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	h := api.HealthRoutes(mux, api.HealthConfig{})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, api.ReadinessPath, nil))
	assert.Equal(t, http.StatusTeapot, rec.Code)
}
