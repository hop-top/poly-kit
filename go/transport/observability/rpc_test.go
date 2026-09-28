package observability

import (
	"context"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
