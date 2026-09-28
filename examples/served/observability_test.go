package main

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"hop.top/kit/go/transport/observability"
	"hop.top/kit/go/transport/socket"
)

// inMemoryObservability links the served fixture's provider to
// in-memory recorders, so a test reads the trace and metrics the
// operator's configuration produces without exporting anything.
func inMemoryObservability(t *testing.T) (*tracetest.SpanRecorder, *sdkmetric.ManualReader, *observability.Serve) {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		_ = mp.Shutdown(context.Background())
	})
	return rec, reader, observability.NewServe(
		observability.WithTracerProvider(tp), observability.WithMeterProvider(mp))
}

func TestOneTraceFromRESTThroughTheInvocation(t *testing.T) {
	const caller = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	rec, reader, obs := inMemoryObservability(t)
	run := startServe(t, options{observe: obs, config: map[string]any{
		"services.all.tracing.enabled": true,
		"services.api.metrics.enabled": true,
	}}, "api", "--addr", "127.0.0.1:0")
	base := "http://" + run.waitReady(t, "api").Address

	req, err := http.NewRequest(http.MethodGet, base+"/v1/commands/item/list", nil)
	require.NoError(t, err)
	req.Header.Set("traceparent", caller)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	byName := map[string]sdktrace.ReadOnlySpan{}
	for _, s := range rec.Ended() {
		byName[s.Name()] = s
	}
	server, invoke := byName["GET /v1/commands/item/list"], byName["invoke item list"]
	require.NotNil(t, server, "spans: %v", byName)
	require.NotNil(t, invoke, "spans: %v", byName)
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", server.SpanContext().TraceID().String())
	assert.Equal(t, "00f067aa0ba902b7", server.Parent().SpanID().String(), "the caller's span is the parent")
	assert.Equal(t, server.SpanContext().SpanID(), invoke.Parent().SpanID(), "the invocation is the request's child")
	assert.Equal(t, server.SpanContext().TraceID(), invoke.SpanContext().TraceID(), "one trace")

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	names := map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			names[m.Name] = true
		}
	}
	for _, want := range []string{observability.MetricRequests, observability.MetricDuration, observability.MetricActive} {
		assert.True(t, names[want], "%s recorded; got %v", want, names)
	}
}

func TestSocketInvocationIsTracedPerServiceConfiguration(t *testing.T) {
	cases := []struct {
		name   string
		config map[string]any
		traced bool
	}{
		{"services.all enables it", map[string]any{"services.all.tracing.enabled": true}, true},
		{"the service's own key wins", map[string]any{
			"services.all.tracing.enabled":    true,
			"services.socket.tracing.enabled": false,
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := shortSocketPath(t)
			rec, _, obs := inMemoryObservability(t)
			run := startServe(t, options{observe: obs, config: c.config}, "socket", "--socket", path)
			run.waitReady(t, "socket")

			resp := callSocket(t, path, socket.Request{Path: []string{"item", "list"}})
			require.True(t, resp.Ok, "%+v", resp.Error)

			var names []string
			for _, s := range rec.Ended() {
				names = append(names, s.Name())
			}
			if c.traced {
				assert.Equal(t, []string{"invoke item list"}, names)
			} else {
				assert.Empty(t, names)
			}
		})
	}
}

func TestObservabilityIsOffByDefault(t *testing.T) {
	rec, reader, obs := inMemoryObservability(t)
	run := startServe(t, options{observe: obs}, "api", "--addr", "127.0.0.1:0")
	status, _ := httpDo(t, http.MethodGet, "http://"+run.waitReady(t, "api").Address+"/v1/commands/item/list", "")
	require.Equal(t, http.StatusOK, status)

	assert.Empty(t, rec.Ended(), "no span without configuration")
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	assert.Empty(t, rm.ScopeMetrics, "no metric without configuration")
}
