package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"hop.top/kit/go/transport/api"
)

// A router wrapped in further middleware hides its routes from
// HealthRoutes; HealthConfig.Routes points it back at the router so
// an adopter's own probe path still wins.
func TestHealthRoutesInspectsRoutesThroughAWrapper(t *testing.T) {
	r := api.NewRouter()
	r.Handle(http.MethodGet, api.LivenessPath, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("adopter"))
	}))
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { r.ServeHTTP(w, req) })

	get := func(h http.Handler) string {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, api.LivenessPath, nil))
		return rec.Body.String()
	}

	if got := get(api.HealthRoutes(wrapped, api.HealthConfig{Routes: r})); got != "adopter" {
		t.Fatalf("with Routes: adopter route shadowed, body %q", got)
	}
	if got := get(api.HealthRoutes(wrapped, api.HealthConfig{})); got == "adopter" {
		t.Fatalf("without Routes the wrapper hides the router; want kit's probe, got adopter route")
	}
}
