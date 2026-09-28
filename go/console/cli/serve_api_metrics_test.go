package cli

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

// scrapeProvider is a provider that serves a metrics endpoint, the way
// kit's observability provider does once configuration enables it.
type scrapeProvider struct {
	fakeObservability
	on          bool
	path        string
	allowRemote bool
}

func (s *scrapeProvider) MetricsEndpoint(service string) (string, http.Handler, bool) {
	if !s.on {
		return "", nil, false
	}
	return s.path, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("kit_serve_requests_total{kit_service=\"" + service + "\"} 1\n"))
	}), s.allowRemote
}

func metricsRoot(t *testing.T, cfg APIConfig, p ServeObservability) *Root {
	t.Helper()
	var opts []func(*Root)
	if p != nil {
		opts = append(opts, WithObservability(p))
	}
	return healthRoot(t, cfg, opts...)
}

func serveVia(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAPIMetrics_AnsweredAtSlot7(t *testing.T) {
	deny := func(*http.Request) (any, error) { return nil, errors.New("no") }
	h := projectionHandler(t, metricsRoot(t, APIConfig{Auth: deny}, &scrapeProvider{on: true, path: api.DefaultMetricsPath}))

	rec := serveVia(h, loopbackRequest(http.MethodGet, "/metrics", nil))
	assert.Equal(t, http.StatusOK, rec.Code, "no Authorization needed")
	assert.Contains(t, rec.Body.String(), `kit_serve_requests_total{kit_service="api"} 1`)
	assert.Equal(t, "api", rec.Header().Get("X-Observed"), "slot 5 wraps it")
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"), "slot 6 wraps it")

	byIP := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	byIP.Host = "10.1.2.3:8080" // a scraper addressing the pod by IP
	assert.Equal(t, http.StatusOK, serveVia(h, byIP).Code, "the Host check does not see a scrape")

	other := httptest.NewRequest(http.MethodGet, "/v1/commands", nil)
	other.Host = "10.1.2.3:8080"
	assert.Equal(t, http.StatusForbidden, serveVia(h, other).Code, "everything else is still Host checked")
	assert.Equal(t, http.StatusUnauthorized, serveVia(h, loopbackRequest(http.MethodGet, "/v1/commands", nil)).Code,
		"and authenticated")
}

func TestAPIMetrics_Absent(t *testing.T) {
	cases := map[string]ServeObservability{
		"no provider linked":        nil,
		"provider without endpoint": &fakeObservability{},
		"endpoint off":              &scrapeProvider{},
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			h := projectionHandler(t, metricsRoot(t, APIConfig{}, p))
			assert.Equal(t, http.StatusNotFound, serveVia(h, loopbackRequest(http.MethodGet, "/metrics", nil)).Code)
		})
	}
}

func TestAPIMetrics_AdopterRouteWins(t *testing.T) {
	cfg := APIConfig{Handlers: func(r *api.Router) {
		r.Handle(http.MethodGet, "/metrics", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("adopter"))
		})
	}}
	h := projectionHandler(t, metricsRoot(t, cfg, &scrapeProvider{on: true, path: "/metrics"}))
	assert.Equal(t, "adopter", serveVia(h, loopbackRequest(http.MethodGet, "/metrics", nil)).Body.String())
}

func TestAPIMetrics_CustomPath(t *testing.T) {
	h := projectionHandler(t, metricsRoot(t, APIConfig{}, &scrapeProvider{on: true, path: "/_kit/metrics"}))
	assert.Equal(t, http.StatusOK, serveVia(h, loopbackRequest(http.MethodGet, "/_kit/metrics", nil)).Code)
	assert.Equal(t, http.StatusNotFound, serveVia(h, loopbackRequest(http.MethodGet, "/metrics", nil)).Code)
}

// remoteMetricsRoot binds beyond loopback with both exposure opt-ins,
// so the only gate left to trip is the metrics endpoint's.
func remoteMetricsRoot(t *testing.T, p ServeObservability) *Root {
	t.Helper()
	allow := func(*http.Request) (any, error) { return "caller", nil }
	r := metricsRoot(t, APIConfig{Addr: "0.0.0.0:0", Auth: allow}, p)
	r.Viper.Set("services.api.insecure_no_policy", true)
	return r
}

func TestAPIMetrics_RemoteBindNeedsTheOptIn(t *testing.T) {
	for _, scope := range []string{"api", "all"} {
		t.Run(scope, func(t *testing.T) {
			r := remoteMetricsRoot(t, &scrapeProvider{})
			require.NoError(t, apiSvc(t, r).Validate(), "no endpoint requested, nothing to refuse")

			r.Viper.Set("services."+scope+".metrics.scrape.enabled", true)
			err := apiSvc(t, r).Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "services."+scope+".metrics.scrape.enabled")
			assert.Contains(t, err.Error(), "services.api.metrics.scrape.allow_remote: true")
			assert.Contains(t, err.Error(), "127.0.0.1")

			r.Viper.Set("services."+scope+".metrics.scrape.allow_remote", true)
			assert.NoError(t, apiSvc(t, r).Validate())
		})
	}

	// Loopback needs no opt-in.
	r := metricsRoot(t, APIConfig{}, &scrapeProvider{})
	r.Viper.Set("services.api.metrics.scrape.enabled", true)
	assert.NoError(t, apiSvc(t, r).Validate())
}

func TestAPIMetrics_ProviderWithoutEndpointIsRefused(t *testing.T) {
	r := metricsRoot(t, APIConfig{}, &fakeObservability{})
	r.Viper.Set("services.api.metrics.scrape.enabled", true)
	err := apiSvc(t, r).Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "serves no metrics endpoint")
}

func TestAPIMetrics_ScrapeWithoutProviderIsRefused(t *testing.T) {
	r := New(Config{Name: "tool", DisableValidate: true}, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
	r.Viper.Set("services.api.metrics.scrape.enabled", true)
	_, err := r.startObservability(t.Context(), []string{APIServiceName})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "services.api.metrics.scrape.enabled")
}

func TestAPIMetrics_RemoteBindFailsClosedOnTheProvidersWord(t *testing.T) {
	// Validation read allow_remote as true, the provider did not: the
	// handler is not built.
	r := remoteMetricsRoot(t, &scrapeProvider{on: true, path: "/metrics"})
	_, err := apiSvc(t, r).buildHandler(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "allow_remote")

	r = remoteMetricsRoot(t, &scrapeProvider{on: true, path: "/metrics", allowRemote: true})
	_, err = apiSvc(t, r).buildHandler(t.Context())
	assert.NoError(t, err)
}
