package observability

import (
	"context"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace"

	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1"
	"hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1/cmdsurfacev1connect"
	"hop.top/kit/go/transport/rpc"
)

func TestRPCCallAndInvocationContinueTheCallersTrace(t *testing.T) {
	rec, tp := recorder(t)
	p := traced(t, WithTracerProvider(tp))
	ic, err := p.RPCInterceptor()
	require.NoError(t, err)
	require.NotNil(t, ic)

	bridge := cmdsurface.New(tree(nil), p.BridgeOptions("rpc")...)
	bridge.Expose("*", cmdsurface.SurfaceRPC)
	srv := rpc.NewServer(rpc.WithInterceptors(ic))
	require.NoError(t, cmdsurface.MountRPC(bridge, srv))
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	req := connect.NewRequest(&cmdsurfacev1.Invocation{Path: []string{"hello"}})
	req.Header().Set("traceparent", callerParent)
	_, err = cmdsurfacev1connect.NewCommandsClient(ts.Client(), ts.URL).Invoke(context.Background(), req)
	require.NoError(t, err)

	server := spanNamed(t, rec, "cmdsurface.v1.Commands/Invoke")
	assert.Equal(t, trace.SpanKindServer, server.SpanKind())
	assert.Equal(t, callerTraceID, server.SpanContext().TraceID().String())
	assert.Equal(t, callerSpanID, server.Parent().SpanID().String(), "the caller's span is the parent")

	inv := spanNamed(t, rec, "invoke hello")
	assert.Equal(t, server.SpanContext().SpanID(), inv.Parent().SpanID(), "the invocation is the call's child")
	assert.Contains(t, inv.Attributes(), AttrSurface.String("rpc"))
}

// TestRPCMetricsFollowRPCSemconv pins the RPC instrument and attribute
// names adopters build dashboards on; an otelconnect upgrade that moves
// them fails here rather than only in someone's alerts.
func TestRPCMetricsFollowRPCSemconv(t *testing.T) {
	reader, mp := manualMeter(t)
	p, err := New(context.Background(), Config{Metrics: Signal{Enabled: true}}, WithMeterProvider(mp))
	require.NoError(t, err)
	ic, err := p.RPCInterceptor()
	require.NoError(t, err)

	bridge := cmdsurface.New(tree(nil), p.BridgeOptions("rpc")...)
	bridge.Expose("*", cmdsurface.SurfaceRPC)
	srv := rpc.NewServer(rpc.WithInterceptors(ic))
	require.NoError(t, cmdsurface.MountRPC(bridge, srv))
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	req := connect.NewRequest(&cmdsurfacev1.Invocation{Path: []string{"hello"}})
	_, err = cmdsurfacev1connect.NewCommandsClient(ts.Client(), ts.URL).Invoke(context.Background(), req)
	require.NoError(t, err)

	metrics := collect(t, reader)
	_, legacy := metrics["rpc.server.duration"]
	assert.False(t, legacy, "pre-1.43 RPC semconv name rpc.server.duration still emitted")
	data, ok := metrics["rpc.server.call.duration"]
	require.True(t, ok, "rpc.server.call.duration missing; got %v", keys(metrics))
	hist, ok := data.(metricdata.Histogram[float64])
	require.True(t, ok, "%T is not a float64 histogram", data)
	require.NotEmpty(t, hist.DataPoints)

	attrs := hist.DataPoints[0].Attributes
	system, _ := attrs.Value("rpc.system.name")
	assert.Equal(t, "connectrpc", system.AsString())
	method, _ := attrs.Value("rpc.method")
	assert.Equal(t, "cmdsurface.v1.Commands/Invoke", method.AsString())
	assert.False(t, attrs.HasValue("rpc.service"), "rpc.service folded into rpc.method")
}

func keys(m map[string]metricdata.Aggregation) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
