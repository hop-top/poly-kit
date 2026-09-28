package observability

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// The caller's span, as a W3C traceparent.
const (
	callerTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	callerSpanID  = "00f067aa0ba902b7"
	callerParent  = "00-" + callerTraceID + "-" + callerSpanID + "-01"
)

// recorder is an in-memory trace pipeline: nothing leaves the process.
func recorder(t *testing.T) (*tracetest.SpanRecorder, trace.TracerProvider) {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	return rec, tp
}

// manualMeter is an in-memory metrics pipeline read on demand.
func manualMeter(t *testing.T) (*sdkmetric.ManualReader, *sdkmetric.MeterProvider) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	return reader, mp
}

func traced(t *testing.T, opts ...Option) *Provider {
	t.Helper()
	p, err := New(context.Background(), Config{Tracing: Signal{Enabled: true}}, opts...)
	require.NoError(t, err)
	return p
}

// spanNamed returns the one ended span called name.
func spanNamed(t *testing.T, rec *tracetest.SpanRecorder, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	var names []string
	for _, s := range rec.Ended() {
		if s.Name() == name {
			return s
		}
		names = append(names, s.Name())
	}
	t.Fatalf("no span %q among %v", name, names)
	return nil
}

// tree is a command tree with one read, one destructive and one
// argument-echoing command, and a hook that sees each run's context.
func tree(onRun func(context.Context)) *cobra.Command {
	root := &cobra.Command{Use: "tool", SilenceUsage: true, SilenceErrors: true}
	hello := &cobra.Command{Use: "hello", Annotations: map[string]string{"kit/side-effect": "read"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if onRun != nil {
				onRun(cmd.Context())
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "hi")
			return nil
		}}
	purge := &cobra.Command{Use: "purge", Annotations: map[string]string{"kit/side-effect": "destructive-shared"},
		RunE: func(*cobra.Command, []string) error { return nil }}
	fail := &cobra.Command{Use: "fail", Annotations: map[string]string{"kit/side-effect": "read"},
		RunE: func(*cobra.Command, []string) error { return errors.New("boom") }}
	root.AddCommand(hello, purge, fail)
	return root
}

func TestDisabledConstructsNothing(t *testing.T) {
	before := exportersBuilt.Load()

	p, err := New(context.Background(), Config{})
	require.NoError(t, err)
	assert.False(t, p.Enabled())
	assert.Nil(t, p.HTTPMiddleware("api"), "no middleware is installed at all")
	assert.Nil(t, p.BridgeOptions("api"))
	ic, err := p.RPCInterceptor()
	require.NoError(t, err)
	assert.Nil(t, ic)
	inner := cmdsurface.InProcessRunner(tree(nil))
	assert.Same(t, inner, p.RunnerMiddleware("api")(inner), "the runner is returned unwrapped")
	require.NoError(t, p.Shutdown(context.Background()))

	// A configuration that names exporters but enables neither signal
	// is still off.
	v := viper.New()
	v.Set("services.all.tracing.exporter", "otlp")
	v.Set("services.all.metrics.endpoint", "http://collector.example:4318")
	v.Set("services.api.tracing.enabled", false)
	s := NewServe()
	stop, err := s.Start(context.Background(), v, "tool", "1.0", []string{"api", "socket"})
	require.NoError(t, err)
	assert.Nil(t, stop)
	assert.Nil(t, s.HTTPMiddleware("api"))
	assert.Nil(t, s.BridgeOptions("socket"))

	assert.Equal(t, before, exportersBuilt.Load(), "no exporter was constructed")
}

func TestHTTPRequestAndInvocationContinueTheCallersTrace(t *testing.T) {
	rec, tp := recorder(t)
	p := traced(t, WithTracerProvider(tp))

	var runCtx context.Context
	bridge := cmdsurface.New(tree(func(ctx context.Context) { runCtx = ctx }), p.BridgeOptions("api")...)
	bridge.Expose("*", cmdsurface.SurfaceREST)

	router := api.NewRouter(api.WithMiddleware(api.RequestID(), p.HTTPMiddleware("api")))
	router.Handle(http.MethodPost, "/v1/commands/hello", func(w http.ResponseWriter, r *http.Request) {
		m := api.RequestMetaFrom(r)
		res, err := bridge.Invoke(r.Context(), cmdsurface.Invocation{
			Path: []string{"hello"},
			Meta: cmdsurface.Meta{Surface: cmdsurface.SurfaceREST, RequestID: m.RequestID,
				TraceID: m.TraceID, Traceparent: m.Traceparent, Tracestate: m.Tracestate},
		})
		require.NoError(t, err)
		_, _ = w.Write([]byte(res.Stdout))
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/commands/hello", nil)
	req.Header.Set("traceparent", callerParent)
	req.Header.Set("tracestate", "vendor=x")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	server := spanNamed(t, rec, "POST /v1/commands/hello")
	assert.Equal(t, trace.SpanKindServer, server.SpanKind())
	assert.Equal(t, callerTraceID, server.SpanContext().TraceID().String(), "the caller's trace is continued")
	assert.Equal(t, callerSpanID, server.Parent().SpanID().String(), "the caller's span is the parent")
	assert.True(t, server.Parent().IsRemote())
	assert.Equal(t, "vendor=x", server.SpanContext().TraceState().String())

	inv := spanNamed(t, rec, "invoke hello")
	assert.Equal(t, trace.SpanKindInternal, inv.SpanKind())
	assert.Equal(t, server.SpanContext().SpanID(), inv.Parent().SpanID(), "the invocation is the request's child")
	assert.Equal(t, callerTraceID, inv.SpanContext().TraceID().String())
	assert.Contains(t, inv.Attributes(), AttrService.String("api"))
	assert.Contains(t, inv.Attributes(), AttrSurface.String("rest"))
	assert.Contains(t, inv.Attributes(), AttrCommand.String("hello"))

	require.NotNil(t, runCtx)
	assert.Equal(t, inv.SpanContext().SpanID(), trace.SpanContextFromContext(runCtx).SpanID(),
		"the command runs inside the invocation span")
}

func TestInvocationWithoutRequestSpanParentsOnMeta(t *testing.T) {
	// The socket has no HTTP layer: the invocation span is the first
	// local one, parented on the caller's propagated context.
	rec, tp := recorder(t)
	p := traced(t, WithTracerProvider(tp))
	bridge := cmdsurface.New(tree(nil), p.BridgeOptions("socket")...)
	bridge.Expose("*", cmdsurface.SurfaceSocket)

	_, err := bridge.Invoke(context.Background(), cmdsurface.Invocation{
		Path: []string{"hello"},
		Meta: cmdsurface.Meta{Surface: cmdsurface.SurfaceSocket, Traceparent: callerParent},
	})
	require.NoError(t, err)

	inv := spanNamed(t, rec, "invoke hello")
	assert.Equal(t, trace.SpanKindServer, inv.SpanKind())
	assert.Equal(t, callerTraceID, inv.SpanContext().TraceID().String())
	assert.Equal(t, callerSpanID, inv.Parent().SpanID().String())
}

func TestSubprocessReceivesTheInvocationSpanAsTraceparent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /usr/bin/env")
	}
	// A stale value inherited by the server must not reach the child.
	t.Setenv("TRACEPARENT", "00-11111111111111111111111111111111-2222222222222222-01")

	rec, tp := recorder(t)
	p := traced(t, WithTracerProvider(tp))
	// The subprocess is `env printenv TRACEPARENT`: the leaf path is
	// the first argv word, the argument the second.
	root := &cobra.Command{Use: "tool"}
	root.AddCommand(&cobra.Command{Use: "printenv", Args: cobra.ArbitraryArgs,
		Annotations: map[string]string{"kit/side-effect": "read"}, RunE: func(*cobra.Command, []string) error { return nil }})
	bridge := cmdsurface.New(root,
		append(p.BridgeOptions("socket"), cmdsurface.WithRunner(cmdsurface.SubprocessRunner("/usr/bin/env")))...)
	bridge.Expose("*", cmdsurface.SurfaceSocket)

	res, err := bridge.Invoke(context.Background(), cmdsurface.Invocation{
		Path: []string{"printenv"},
		Args: []string{"TRACEPARENT"},
		Meta: cmdsurface.Meta{Surface: cmdsurface.SurfaceSocket, Traceparent: callerParent},
	})
	require.NoError(t, err)
	require.Equal(t, 0, res.ExitCode, res.Stderr)

	inv := spanNamed(t, rec, "invoke printenv")
	want := fmt.Sprintf("00-%s-%s-01", inv.SpanContext().TraceID(), inv.SpanContext().SpanID())
	assert.Equal(t, want, strings.TrimSpace(res.Stdout), "the child's parent is the invocation span")
}

// collect reads every metric the reader holds, by instrument name.
func collect(t *testing.T, reader *sdkmetric.ManualReader) map[string]metricdata.Aggregation {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	out := map[string]metricdata.Aggregation{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			out[m.Name] = m.Data
		}
	}
	return out
}

// sumBy totals an int64 counter's points by one attribute's value.
func sumBy(t *testing.T, data metricdata.Aggregation, key attribute.Key) map[string]int64 {
	t.Helper()
	sum, ok := data.(metricdata.Sum[int64])
	require.True(t, ok, "%T is not an int64 sum", data)
	out := map[string]int64{}
	for _, dp := range sum.DataPoints {
		v, _ := dp.Attributes.Value(key)
		out[v.AsString()] += dp.Value
	}
	return out
}

func TestVerdictsAreCountedAndRefusalsByCode(t *testing.T) {
	reader, mp := manualMeter(t)
	p, err := New(context.Background(), Config{Metrics: Signal{Enabled: true}}, WithMeterProvider(mp))
	require.NoError(t, err)

	bridge := cmdsurface.New(tree(nil), p.BridgeOptions("api")...)
	bridge.Expose("*", cmdsurface.SurfaceREST)
	invoke := func(path ...string) {
		_, _ = bridge.Invoke(context.Background(), cmdsurface.Invocation{Path: path,
			Meta: cmdsurface.Meta{Surface: cmdsurface.SurfaceREST}})
	}
	invoke("hello")
	invoke("hello")
	invoke("fail")
	invoke("purge")                 // destructive: refused on REST by the default policy
	invoke("no", "such", "command") // unresolved
	invoke("purge")
	bridge.Audit(context.Background(), cmdsurface.Invocation{Meta: cmdsurface.Meta{Surface: cmdsurface.SurfaceREST, RequestedAt: time.Now()}},
		cmdsurface.Result{}, fmt.Errorf("%w: bad token", cmdsurface.ErrAuthRefused))

	got := collect(t, reader)
	assert.Equal(t, map[string]int64{
		RefusalDestructiveBlocked: 2,
		RefusalUnknownCommand:     1,
		RefusalUnauthenticated:    1,
	}, sumBy(t, got[MetricRefusals], AttrRefusalReason))
	assert.Equal(t, map[string]int64{OutcomeOK: 2, OutcomeError: 1, OutcomeRefused: 4},
		sumBy(t, got[MetricRequests], AttrOutcome))
	assert.Equal(t, map[string]int64{"api": 7}, sumBy(t, got[MetricRequests], AttrService))
	assert.Equal(t, map[string]int64{"hello": 2, "fail": 1, "purge": 2, "": 2},
		sumBy(t, got[MetricRequests], AttrCommand), "an unresolved path never becomes a label")
	assert.Equal(t, map[string]int64{"api": 0}, sumBy(t, got[MetricActive], AttrService),
		"nothing is left in flight")

	hist, ok := got[MetricDuration].(metricdata.Histogram[float64])
	require.True(t, ok)
	var n uint64
	for _, dp := range hist.DataPoints {
		n += dp.Count
	}
	assert.Equal(t, uint64(7), n, "every verdict is timed, refusals included")
}

func TestRateLimitRefusalsAreCountedByCode(t *testing.T) {
	reader, mp := manualMeter(t)
	p, err := New(context.Background(), Config{Metrics: Signal{Enabled: true}}, WithMeterProvider(mp))
	require.NoError(t, err)

	opts := append(p.BridgeOptions("api"), cmdsurface.WithRateLimit(cmdsurface.RateLimit{
		Read: cmdsurface.RateRule{PerMinute: 1, Burst: 1},
	}))
	bridge := cmdsurface.New(tree(nil), opts...)
	bridge.Expose("*", cmdsurface.SurfaceREST)
	for range 3 {
		_, _ = bridge.Invoke(context.Background(), cmdsurface.Invocation{Path: []string{"hello"},
			Meta: cmdsurface.Meta{Surface: cmdsurface.SurfaceREST, Caller: "alice"}})
	}

	got := collect(t, reader)
	assert.Equal(t, map[string]int64{RefusalRateLimited: 2}, sumBy(t, got[MetricRefusals], AttrRefusalReason))
	assert.Equal(t, map[string]int64{OutcomeOK: 1, OutcomeRefused: 2}, sumBy(t, got[MetricRequests], AttrOutcome))
	assert.Equal(t, RefusalRateLimited, RefusalCode(fmt.Errorf("wrapped: %w", &cmdsurface.RateLimitedError{})))
}

func TestInFlightGaugeCountsRunningInvocations(t *testing.T) {
	reader, mp := manualMeter(t)
	p, err := New(context.Background(), Config{Metrics: Signal{Enabled: true}}, WithMeterProvider(mp))
	require.NoError(t, err)

	started, release := make(chan struct{}), make(chan struct{})
	root := tree(func(context.Context) { close(started); <-release })
	bridge := cmdsurface.New(root, p.BridgeOptions("socket")...)
	bridge.Expose("*", cmdsurface.SurfaceSocket)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = bridge.Invoke(context.Background(), cmdsurface.Invocation{Path: []string{"hello"},
			Meta: cmdsurface.Meta{Surface: cmdsurface.SurfaceSocket}})
	}()
	<-started
	assert.Equal(t, map[string]int64{"socket": 1}, sumBy(t, collect(t, reader)[MetricActive], AttrSurface))
	close(release)
	wg.Wait()
	assert.Equal(t, map[string]int64{"socket": 0}, sumBy(t, collect(t, reader)[MetricActive], AttrSurface))
}

func TestHTTPPlaneRefusalIsCountedByCode(t *testing.T) {
	reader, mp := manualMeter(t)
	rec, tp := recorder(t)
	p, err := New(context.Background(), Config{Tracing: Signal{Enabled: true}, Metrics: Signal{Enabled: true}},
		WithTracerProvider(tp), WithMeterProvider(mp))
	require.NoError(t, err)

	// A body limit below the tracing slot refuses and records its code.
	bodyLimit := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > 4 {
				api.RecordRefusal(r, "body_too_large")
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	router := api.NewRouter(api.WithMiddleware(p.HTTPMiddleware("api"), bodyLimit))
	router.Handle(http.MethodPost, "/x", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	for _, body := range []string{"ok", "far too large", "ok"} {
		router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body)))
	}

	got := collect(t, reader)
	assert.Equal(t, map[string]int64{"body_too_large": 1}, sumBy(t, got[MetricHTTPRefusals], AttrRefusalReason))
	assert.Equal(t, map[string]int64{"api": 0}, sumBy(t, got[MetricHTTPActive], AttrService))

	var events []string
	for _, s := range rec.Ended() {
		for _, e := range s.Events() {
			events = append(events, e.Name)
		}
	}
	assert.Equal(t, []string{refusalEventName}, events, "the refused request's span says so")
}

func TestResolveIsPerKeyServiceBeforeAll(t *testing.T) {
	v := viper.New()
	v.Set("services.all.tracing.enabled", true)
	v.Set("services.all.tracing.exporter", "stdout")
	v.Set("services.all.tracing.sample_ratio", "0.25") // environment values arrive as strings
	v.Set("services.api.tracing.endpoint", "http://collector:4318")
	v.Set("services.socket.tracing.enabled", "false")
	v.Set("services.api.metrics.enabled", true)
	v.Set("services.api.metrics.interval", "15s")

	api, err := Resolve(v, "api")
	require.NoError(t, err)
	assert.Equal(t, Signal{Enabled: true, Exporter: ExporterStdout, Endpoint: "http://collector:4318",
		SampleRatio: 0.25, Interval: DefaultMetricsInterval}, api.Tracing, "service key and services.all merge key by key")
	assert.Equal(t, Signal{Enabled: true, Exporter: ExporterOTLP, SampleRatio: 1, Interval: 15 * time.Second}, api.Metrics)

	sock, err := Resolve(v, "socket")
	require.NoError(t, err)
	assert.False(t, sock.Tracing.Enabled, "the service's own key wins over services.all")
	assert.Equal(t, ExporterStdout, sock.Tracing.Exporter, "unset service keys still inherit")
	assert.False(t, sock.Metrics.Enabled, "metrics default off")

	none, err := Resolve(nil, "api")
	require.NoError(t, err)
	assert.False(t, none.Enabled())
}

func TestInvalidConfigurationIsRefused(t *testing.T) {
	cases := map[string]map[string]any{
		"unknown key":           {"services.api.tracing.enabeld": true},
		"unknown key in all":    {"services.all.metrics.endpont": "x"},
		"unknown exporter":      {"services.api.tracing.exporter": "zipkin"},
		"ratio out of range":    {"services.all.tracing.sample_ratio": 2},
		"non-boolean enabled":   {"services.api.metrics.enabled": "sometimes"},
		"non-positive interval": {"services.api.metrics.interval": "0s"},
		"scalar block":          {"services.api.tracing": true},
	}
	for name, keys := range cases {
		t.Run(name, func(t *testing.T) {
			v := viper.New()
			for k, val := range keys {
				v.Set(k, val)
			}
			_, err := NewServe().Start(context.Background(), v, "tool", "", []string{"api"})
			assert.Error(t, err)
		})
	}
}

func TestServeSharesOneProviderPerConfigurationAndFlushesOnStop(t *testing.T) {
	var out bytes.Buffer
	v := viper.New()
	v.Set("services.all.tracing.enabled", true)
	v.Set("services.all.tracing.exporter", "stdout")
	v.Set("services.worker.tracing.enabled", false)

	s := NewServe(WithOutput(&out))
	stop, err := s.Start(context.Background(), v, "tool", "1.2.3", []string{"api", "socket", "worker"})
	require.NoError(t, err)
	require.NotNil(t, stop)
	require.NotNil(t, s.Provider("api"))
	assert.Same(t, s.Provider("api"), s.Provider("socket"), "identical configuration, one provider")
	assert.Nil(t, s.Provider("worker"))

	bridge := cmdsurface.New(tree(nil), s.BridgeOptions("socket")...)
	bridge.Expose("*", cmdsurface.SurfaceSocket)
	_, err = bridge.Invoke(context.Background(), cmdsurface.Invocation{Path: []string{"hello"},
		Meta: cmdsurface.Meta{Surface: cmdsurface.SurfaceSocket}})
	require.NoError(t, err)

	require.NoError(t, stop(context.Background()))
	assert.Contains(t, out.String(), `"Name":"invoke hello"`, "stop flushed the batch")
	assert.Contains(t, out.String(), `"Value":"tool"`, "the tool names the service")
}
