package observability

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// scraped is a Provider with the scrape endpoint on and no exporter.
func scraped(t *testing.T) *Provider {
	t.Helper()
	p, err := New(context.Background(), Config{Metrics: Signal{Enabled: true, Exporter: ExporterNone,
		Interval: DefaultMetricsInterval, Scrape: Scrape{Enabled: true}}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	return p
}

func scrape(t *testing.T, h http.Handler, method string) (*http.Response, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, api.DefaultMetricsPath, nil))
	resp := rec.Result()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, string(body)
}

func TestScrapeEndpointServesTheServeInstruments(t *testing.T) {
	exportersBuilt.Store(0)
	p := scraped(t)
	assert.Zero(t, exportersBuilt.Load(), "exporter none builds no push exporter")

	path, h, allowRemote := p.MetricsEndpoint()
	require.NotNil(t, h)
	assert.Equal(t, api.DefaultMetricsPath, path)
	assert.False(t, allowRemote)

	// One HTTP request through the slot 5 middleware, one invocation.
	router := api.NewRouter(api.WithMiddleware(p.HTTPMiddleware("api")))
	router.Handle(http.MethodGet, "/x", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	bridge := cmdsurface.New(tree(nil), p.BridgeOptions("api")...)
	bridge.Expose("*", cmdsurface.SurfaceREST)
	_, err := bridge.Invoke(context.Background(), cmdsurface.Invocation{Path: []string{"hello"},
		Meta: cmdsurface.Meta{Surface: cmdsurface.SurfaceREST}})
	require.NoError(t, err)

	resp, body := scrape(t, h, http.MethodGet)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, ScrapeContentType, resp.Header.Get("Content-Type"))
	for _, want := range []string{
		"# TYPE kit_serve_requests_total counter\n",
		`kit_serve_requests_total{kit_command="hello",kit_outcome="ok",kit_service="api",kit_surface="rest",otel_scope_name="` + ScopeName + `"} 1`,
		"# TYPE kit_serve_request_duration_seconds histogram\n",
		`kit_serve_request_duration_seconds_bucket{`,
		`le="+Inf"} 1`,
		"# TYPE kit_serve_requests_active gauge\n",
		"# TYPE kit_serve_http_requests_active gauge\n",
		"# TYPE http_server_duration_milliseconds histogram\n", // otelhttp v0.60 semconv
		"# TYPE target_info gauge\n",
	} {
		assert.Contains(t, body, want)
	}

	resp, body = scrape(t, h, http.MethodHead)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, body, "HEAD writes no body")
}

func TestScrapeEndpointIsAbsentUnlessEnabled(t *testing.T) {
	for name, cfg := range map[string]Config{
		"metrics off":  {},
		"scrape off":   {Metrics: Signal{Enabled: true, Exporter: ExporterStdout, Interval: DefaultMetricsInterval}},
		"tracing only": {Tracing: Signal{Enabled: true, Exporter: ExporterStdout, SampleRatio: 1}},
	} {
		t.Run(name, func(t *testing.T) {
			p, err := New(context.Background(), cfg, WithOutput(io.Discard))
			require.NoError(t, err)
			t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
			path, h, _ := p.MetricsEndpoint()
			assert.Empty(t, path)
			assert.Nil(t, h)
		})
	}
	var nilp *Provider
	_, h, _ := nilp.MetricsEndpoint()
	assert.Nil(t, h)

	s := NewServe()
	_, h, _ = s.MetricsEndpoint("api")
	assert.Nil(t, h, "not started")
}

func TestScrapeNeedsTheBuiltMeterProvider(t *testing.T) {
	_, mp := manualMeter(t)
	_, err := New(context.Background(), Config{Metrics: Signal{Enabled: true, Scrape: Scrape{Enabled: true}}},
		WithMeterProvider(mp))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "WithMeterProvider")
}

func TestServeResolvesTheScrapeEndpointPerService(t *testing.T) {
	v := viper.New()
	v.Set("services.all.metrics.enabled", true)
	v.Set("services.all.metrics.exporter", "none")
	v.Set("services.all.metrics.scrape.enabled", "true") // environment values arrive as strings
	v.Set("services.api.metrics.scrape.path", "/_kit/metrics")
	v.Set("services.api.metrics.scrape.allow_remote", true)

	cfg, err := Resolve(v, "api")
	require.NoError(t, err)
	assert.Equal(t, Scrape{Enabled: true, Path: "/_kit/metrics", AllowRemote: true}, cfg.Metrics.Scrape,
		"service keys and services.all merge key by key")

	s := NewServe()
	stop, err := s.Start(context.Background(), v, "tool", "1.0.0", []string{"api", "socket"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = stop(context.Background()) })

	path, h, allowRemote := s.MetricsEndpoint("api")
	require.NotNil(t, h)
	assert.Equal(t, "/_kit/metrics", path)
	assert.True(t, allowRemote)

	path, h, allowRemote = s.MetricsEndpoint("socket")
	require.NotNil(t, h, "services.all reaches every service")
	assert.Equal(t, api.DefaultMetricsPath, path)
	assert.False(t, allowRemote)
}

func TestInvalidScrapeConfigurationIsRefused(t *testing.T) {
	on := map[string]any{"services.api.metrics.enabled": true}
	with := func(extra map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range on {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	cases := map[string]map[string]any{
		"scrape without metrics":   {"services.api.metrics.scrape.enabled": true},
		"exporter none unread":     with(map[string]any{"services.api.metrics.exporter": "none"}),
		"tracing exporter none":    {"services.api.tracing.enabled": true, "services.api.tracing.exporter": "none"},
		"unknown scrape key":       with(map[string]any{"services.api.metrics.scrape.enabeld": true}),
		"unknown scrape key (all)": with(map[string]any{"services.all.metrics.scrape.port": 9090}),
		"scalar scrape block":      with(map[string]any{"services.api.metrics.scrape": true}),
		"relative path":            with(map[string]any{"services.api.metrics.scrape.enabled": true, "services.api.metrics.scrape.path": "metrics"}),
		"trailing slash":           with(map[string]any{"services.api.metrics.scrape.enabled": true, "services.api.metrics.scrape.path": "/metrics/"}),
		"root path":                with(map[string]any{"services.api.metrics.scrape.enabled": true, "services.api.metrics.scrape.path": "/"}),
		"non-boolean allow_remote": with(map[string]any{"services.api.metrics.scrape.enabled": true, "services.api.metrics.scrape.allow_remote": "maybe"}),
	}
	for name, keys := range cases {
		t.Run(name, func(t *testing.T) {
			v := viper.New()
			for k, val := range keys {
				v.Set(k, val)
			}
			_, err := NewServe().Start(context.Background(), v, "tool", "", []string{"api"})
			require.Error(t, err)
			t.Log(err)
		})
	}
}

func TestExpositionFormat(t *testing.T) {
	scope := instrumentation.Scope{Name: "s", Version: "1"}
	attrs := attribute.NewSet(
		attribute.String("a.b", `q"uo\te`+"\n"),
		attribute.String("a_b", "x"), // collides with a.b once translated
		attribute.Int("9lives", 9),
	)
	rm := metricdata.ResourceMetrics{
		Resource: resource.NewSchemaless(attribute.String("service.name", "tool")),
		ScopeMetrics: []metricdata.ScopeMetrics{{Scope: scope, Metrics: []metricdata.Metrics{
			{Name: "req.count", Unit: "{request}", Description: "Requests.\nAll of them.",
				Data: metricdata.Sum[int64]{IsMonotonic: true, Temporality: metricdata.CumulativeTemporality,
					DataPoints: []metricdata.DataPoint[int64]{{Attributes: attrs, Value: 3}}}},
			{Name: "in.flight", Data: metricdata.Sum[int64]{IsMonotonic: false,
				DataPoints: []metricdata.DataPoint[int64]{{Value: -1}}}},
			{Name: "lat", Unit: "s", Data: metricdata.Histogram[float64]{
				DataPoints: []metricdata.HistogramDataPoint[float64]{{
					Bounds: []float64{0.1, 1}, BucketCounts: []uint64{1, 2, 3}, Count: 6, Sum: 7.5}}}},
			{Name: "size", Unit: "By", Data: metricdata.Gauge[float64]{
				DataPoints: []metricdata.DataPoint[float64]{{Value: 1.5}}}},
			// Translates to the counter's name with another type: left out.
			{Name: "req.count.total", Data: metricdata.Gauge[int64]{
				DataPoints: []metricdata.DataPoint[int64]{{Value: 99}}}},
		}}},
	}
	var b strings.Builder
	writeExposition(&b, &rm)
	const sc = `otel_scope_name="s",otel_scope_version="1"`
	want := `# HELP target_info Target metadata
# TYPE target_info gauge
target_info{service_name="tool"} 1
# TYPE in_flight gauge
in_flight{` + sc + `} -1
# TYPE lat_seconds histogram
lat_seconds_bucket{` + sc + `,le="0.1"} 1
lat_seconds_bucket{` + sc + `,le="1"} 3
lat_seconds_bucket{` + sc + `,le="+Inf"} 6
lat_seconds_sum{` + sc + `} 7.5
lat_seconds_count{` + sc + `} 6
# HELP req_count_total Requests.\nAll of them.
# TYPE req_count_total counter
req_count_total{a_b="q\"uo\\te\n;x",key_9lives="9",` + sc + `} 3
# TYPE size_bytes gauge
size_bytes{` + sc + `} 1.5
`
	assert.Equal(t, want, b.String())
}
